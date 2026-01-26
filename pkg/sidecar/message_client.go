// Package sidecar provides a client for the Claude Agent SDK sidecar.
package sidecar

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"go.mau.fi/mautrix-claude/pkg/claudeapi"
)

// MessageClient implements claudeapi.MessageClient using the sidecar.
// This allows using Pro/Max subscriptions via the Agent SDK instead of API credits.
type MessageClient struct {
	client  *Client
	metrics *claudeapi.Metrics
	log     zerolog.Logger
}

// Ensure MessageClient implements claudeapi.MessageClient
var _ claudeapi.MessageClient = (*MessageClient)(nil)

// NewMessageClient creates a new sidecar-backed MessageClient.
func NewMessageClient(baseURL string, timeout time.Duration, log zerolog.Logger) *MessageClient {
	return &MessageClient{
		client:  NewClient(baseURL, timeout, log),
		metrics: claudeapi.NewMetrics(),
		log:     log.With().Str("client_type", "sidecar").Logger(),
	}
}

// CreateMessageStream sends a message and returns a channel of streaming events.
// Note: The sidecar currently returns complete responses, so we simulate streaming
// by sending the complete response as a single event.
func (m *MessageClient) CreateMessageStream(ctx context.Context, req *claudeapi.CreateMessageRequest) (<-chan claudeapi.StreamEvent, error) {
	events := make(chan claudeapi.StreamEvent, 10)

	go func() {
		defer close(events)

		startTime := time.Now()
		m.metrics.TotalRequests.Add(1)

		// Extract portal ID from context or generate one
		portalID := "default"
		if pid, ok := ctx.Value(portalIDKey).(string); ok {
			portalID = pid
		}

		// Extract user credentials from context
		var userID, credentialsJSON string
		if uid, ok := ctx.Value(userIDKey).(string); ok {
			userID = uid
		}
		if creds, ok := ctx.Value(credentialsJSONKey).(string); ok {
			credentialsJSON = creds
		}

		// Extract message text from request
		messageText := extractMessageText(req.Messages)
		if messageText == "" {
			m.metrics.FailedRequests.Add(1)
			events <- claudeapi.StreamEvent{
				Type: "error",
				Error: &claudeapi.StreamError{
					Type:    "invalid_request",
					Message: "empty message",
				},
			}
			return
		}

		// Send message_start event
		events <- claudeapi.StreamEvent{
			Type: "message_start",
			Message: &claudeapi.CreateMessageResponse{
				ID:    fmt.Sprintf("sidecar_%d", time.Now().UnixNano()),
				Model: req.Model,
				Usage: &claudeapi.Usage{},
			},
		}

		// Call sidecar
		var systemPrompt *string
		if req.System != "" {
			systemPrompt = &req.System
		}
		var model *string
		if req.Model != "" {
			model = &req.Model
		}

		resp, err := m.client.Chat(ctx, portalID, userID, credentialsJSON, messageText, systemPrompt, model)
		if err != nil {
			m.metrics.FailedRequests.Add(1)
			events <- claudeapi.StreamEvent{
				Type: "error",
				Error: &claudeapi.StreamError{
					Type:    "sidecar_error",
					Message: err.Error(),
				},
			}
			return
		}

		// Send content as a single block
		events <- claudeapi.StreamEvent{
			Type: "content_block_delta",
			Delta: &claudeapi.ContentDelta{
				Type: "text_delta",
				Text: resp.Response,
			},
		}

		// Track tokens if available from sidecar
		// Note: Sidecar returns combined total, we track as output tokens only
		// since we don't have input/output breakdown from Agent SDK
		if resp.TokensUsed != nil && *resp.TokensUsed > 0 {
			m.metrics.TotalOutputTokens.Add(int64(*resp.TokensUsed))
		}

		// Send message_delta with usage
		events <- claudeapi.StreamEvent{
			Type: "message_delta",
			Usage: &claudeapi.Usage{
				OutputTokens: estimateTokens(resp.Response),
			},
		}

		// Send message_stop
		events <- claudeapi.StreamEvent{
			Type: "message_stop",
		}

		// Record successful request
		outputTokens := estimateTokens(resp.Response)
		m.metrics.RecordRequest(req.Model, time.Since(startTime), 0, outputTokens)
	}()

	return events, nil
}

// CreateMessage sends a message and returns the complete response.
func (m *MessageClient) CreateMessage(ctx context.Context, req *claudeapi.CreateMessageRequest) (*claudeapi.CreateMessageResponse, error) {
	startTime := time.Now()
	m.metrics.TotalRequests.Add(1)

	// Extract portal ID from context
	portalID := "default"
	if pid, ok := ctx.Value(portalIDKey).(string); ok {
		portalID = pid
	}

	// Extract user credentials from context
	var userID, credentialsJSON string
	if uid, ok := ctx.Value(userIDKey).(string); ok {
		userID = uid
	}
	if creds, ok := ctx.Value(credentialsJSONKey).(string); ok {
		credentialsJSON = creds
	}

	// Extract message text
	messageText := extractMessageText(req.Messages)
	if messageText == "" {
		m.metrics.FailedRequests.Add(1)
		return nil, fmt.Errorf("empty message")
	}

	// Call sidecar
	var systemPrompt *string
	if req.System != "" {
		systemPrompt = &req.System
	}
	var model *string
	if req.Model != "" {
		model = &req.Model
	}

	resp, err := m.client.Chat(ctx, portalID, userID, credentialsJSON, messageText, systemPrompt, model)
	if err != nil {
		m.metrics.FailedRequests.Add(1)
		return nil, err
	}

	outputTokens := estimateTokens(resp.Response)
	m.metrics.RecordRequest(req.Model, time.Since(startTime), 0, outputTokens)

	return &claudeapi.CreateMessageResponse{
		ID:      resp.SessionID,
		Type:    "message",
		Role:    "assistant",
		Model:   req.Model,
		Content: []claudeapi.Content{{Type: "text", Text: resp.Response}},
		Usage: &claudeapi.Usage{
			OutputTokens: outputTokens,
		},
		StopReason: "end_turn",
	}, nil
}

// Validate checks if the sidecar is healthy and authenticated.
func (m *MessageClient) Validate(ctx context.Context) error {
	health, err := m.client.Health(ctx)
	if err != nil {
		// HTTP error (503 when not authenticated, connection errors, etc.)
		return fmt.Errorf("sidecar unavailable (Claude Code may not be authenticated): %w", err)
	}
	if !health.Authenticated {
		return fmt.Errorf("sidecar running but Claude Code not authenticated - bridge admin must configure valid credentials")
	}
	if health.Status != "healthy" {
		return fmt.Errorf("sidecar unhealthy: %s", health.Status)
	}
	m.log.Info().Int("sessions", health.Sessions).Bool("authenticated", health.Authenticated).Msg("Sidecar is healthy")
	return nil
}

// GetMetrics returns the metrics collector.
func (m *MessageClient) GetMetrics() *claudeapi.Metrics {
	return m.metrics
}

// GetClientType returns the client type identifier.
func (m *MessageClient) GetClientType() string {
	return "sidecar"
}

// ClearSession clears the conversation history for a portal.
func (m *MessageClient) ClearSession(ctx context.Context, portalID string) error {
	return m.client.DeleteSession(ctx, portalID)
}

// GetSessionStats gets statistics about a session.
func (m *MessageClient) GetSessionStats(ctx context.Context, portalID string) (*SessionStats, error) {
	return m.client.GetSession(ctx, portalID)
}

// Context key for portal ID and user credentials
type contextKey string

const (
	portalIDKey        contextKey = "portal_id"
	userIDKey          contextKey = "user_id"
	credentialsJSONKey contextKey = "credentials_json"
)

// WithPortalID returns a context with the portal ID set.
func WithPortalID(ctx context.Context, portalID string) context.Context {
	return context.WithValue(ctx, portalIDKey, portalID)
}

// WithUserCredentials returns a context with user ID and credentials JSON set.
func WithUserCredentials(ctx context.Context, userID, credentialsJSON string) context.Context {
	ctx = context.WithValue(ctx, userIDKey, userID)
	ctx = context.WithValue(ctx, credentialsJSONKey, credentialsJSON)
	return ctx
}

// extractMessageText extracts the text content from the last user message.
func extractMessageText(messages []claudeapi.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			for _, content := range messages[i].Content {
				if content.Type == "text" && content.Text != "" {
					return content.Text
				}
			}
		}
	}
	return ""
}

// estimateTokens provides a rough estimate of token count.
// Assumes ~4 characters per token (rough average for English text).
func estimateTokens(text string) int {
	return len(text) / 4
}
