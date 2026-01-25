// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// WebClient is a client for claude.ai web interface using session cookies.
type WebClient struct {
	HTTPClient     *http.Client
	SessionKey     string
	OrganizationID string
	BaseURL        string
	Log            zerolog.Logger
	Metrics        *Metrics
}

// Ensure WebClient implements MessageClient interface.
var _ MessageClient = (*WebClient)(nil)

// WebClientOption is a function that configures a WebClient.
type WebClientOption func(*WebClient)

// WithWebHTTPClient sets the HTTP client.
func WithWebHTTPClient(httpClient *http.Client) WebClientOption {
	return func(c *WebClient) {
		c.HTTPClient = httpClient
	}
}

// WithOrganizationID sets the organization ID.
func WithOrganizationID(orgID string) WebClientOption {
	return func(c *WebClient) {
		c.OrganizationID = orgID
	}
}

// WithWebBaseURL sets the base URL for the web client.
func WithWebBaseURL(baseURL string) WebClientOption {
	return func(c *WebClient) {
		c.BaseURL = baseURL
	}
}

// NewWebClient creates a new claude.ai web client.
func NewWebClient(sessionKey string, log zerolog.Logger, opts ...WebClientOption) *WebClient {
	client := &WebClient{
		HTTPClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		SessionKey: sessionKey,
		BaseURL:    "https://claude.ai",
		Log:        log,
		Metrics:    NewMetrics(),
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// webConversation represents a conversation in claude.ai.
type webConversation struct {
	UUID           string    `json:"uuid"`
	Name           string    `json:"name"`
	Summary        string    `json:"summary"`
	Model          string    `json:"model"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	OrganizationID string    `json:"organization_id"`
}

// webOrganization represents an organization in claude.ai.
type webOrganization struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// webChatRequest represents a chat request to claude.ai.
type webChatRequest struct {
	Prompt        string   `json:"prompt"`
	Timezone      string   `json:"timezone"`
	Attachments   []string `json:"attachments"`
	RenderingMode string   `json:"rendering_mode,omitempty"`
}

// webStreamResponse represents a streaming response from claude.ai.
type webStreamResponse struct {
	Completion   string `json:"completion"`
	StopReason   string `json:"stop_reason"`
	Model        string `json:"model"`
	LogID        string `json:"log_id"`
	MessageLimit struct {
		Type string `json:"type"`
	} `json:"messageLimit"`
}

// GetOrganizations fetches the user's organizations.
func (c *WebClient) GetOrganizations(ctx context.Context) ([]webOrganization, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/organizations", nil)
	if err != nil {
		return nil, err
	}

	c.setHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get organizations: status %d", resp.StatusCode)
	}

	var orgs []webOrganization
	if err := json.NewDecoder(resp.Body).Decode(&orgs); err != nil {
		return nil, err
	}

	return orgs, nil
}

// CreateConversation creates a new conversation.
func (c *WebClient) CreateConversation(ctx context.Context, name string) (*webConversation, error) {
	if c.OrganizationID == "" {
		if err := c.fetchOrganizationID(ctx); err != nil {
			return nil, fmt.Errorf("failed to get organization ID: %w", err)
		}
	}

	payload := map[string]string{
		"uuid": generateUUID(),
		"name": name,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/organizations/%s/chat_conversations", c.BaseURL, c.OrganizationID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	c.setHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to create conversation: status %d", resp.StatusCode)
	}

	var conv webConversation
	if err := json.NewDecoder(resp.Body).Decode(&conv); err != nil {
		return nil, err
	}

	return &conv, nil
}

// CreateMessageStream sends a message and returns streaming events.
func (c *WebClient) CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error) {
	if c.OrganizationID == "" {
		if err := c.fetchOrganizationID(ctx); err != nil {
			return nil, fmt.Errorf("failed to get organization ID: %w", err)
		}
	}

	// Create a conversation for this message exchange
	conv, err := c.CreateConversation(ctx, "Bridge conversation")
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	// Build prompt from messages
	prompt := c.buildPromptFromMessages(req.Messages, req.System)

	chatReq := webChatRequest{
		Prompt:      prompt,
		Timezone:    "UTC",
		Attachments: []string{},
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/organizations/%s/chat_conversations/%s/completion",
		c.BaseURL, c.OrganizationID, conv.UUID)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to send message: status %d", resp.StatusCode)
	}

	return c.streamWebMessages(ctx, resp, req.Model, conv.UUID)
}

// streamWebMessages processes the SSE stream from claude.ai.
func (c *WebClient) streamWebMessages(ctx context.Context, resp *http.Response, model, convID string) (<-chan StreamEvent, error) {
	eventChan := make(chan StreamEvent, StreamEventBufferSize)

	go func() {
		defer close(eventChan)
		defer resp.Body.Close()

		var fullCompletion strings.Builder
		scanner := bufio.NewScanner(resp.Body)

		// Send message_start event
		eventChan <- StreamEvent{
			Type: "message_start",
			Message: &CreateMessageResponse{
				ID:    convID,
				Model: model,
				Role:  "assistant",
			},
		}

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var streamResp webStreamResponse
			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				c.Log.Warn().Err(err).Str("data", data).Msg("Failed to parse stream data")
				continue
			}

			// Send content delta for new text
			newText := strings.TrimPrefix(streamResp.Completion, fullCompletion.String())
			if newText != "" {
				fullCompletion.WriteString(newText)
				eventChan <- StreamEvent{
					Type: "content_block_delta",
					Delta: &ContentDelta{
						Type: "text_delta",
						Text: newText,
					},
				}
			}

			// Check for stop reason
			if streamResp.StopReason != "" {
				eventChan <- StreamEvent{
					Type: "message_stop",
				}
			}
		}

		if err := scanner.Err(); err != nil {
			c.Log.Error().Err(err).Msg("Scanner error")
			eventChan <- StreamEvent{
				Type: "error",
				Error: &StreamError{
					Type:    "scanner_error",
					Message: err.Error(),
				},
			}
		}
	}()

	return eventChan, nil
}

// CreateMessage sends a message and waits for the complete response.
func (c *WebClient) CreateMessage(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResponse, error) {
	stream, err := c.CreateMessageStream(ctx, req)
	if err != nil {
		return nil, err
	}

	var responseText strings.Builder
	var response *CreateMessageResponse

	for event := range stream {
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				response = event.Message
			}
		case "content_block_delta":
			if event.Delta != nil {
				responseText.WriteString(event.Delta.Text)
			}
		case "error":
			if event.Error != nil {
				return nil, fmt.Errorf("%s: %s", event.Error.Type, event.Error.Message)
			}
		}
	}

	if response == nil {
		response = &CreateMessageResponse{
			Role:  "assistant",
			Model: req.Model,
		}
	}

	response.Content = []Content{
		{Type: "text", Text: responseText.String()},
	}

	return response, nil
}

// Validate checks if the session cookie is valid.
func (c *WebClient) Validate(ctx context.Context) error {
	orgs, err := c.GetOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("invalid session: %w", err)
	}
	if len(orgs) == 0 {
		return fmt.Errorf("no organizations found")
	}

	// Store the first organization ID
	c.OrganizationID = orgs[0].UUID
	c.Log.Debug().Str("org_id", c.OrganizationID).Msg("Found organization")

	return nil
}

// GetMetrics returns the metrics collector.
func (c *WebClient) GetMetrics() *Metrics {
	return c.Metrics
}

// GetClientType returns the client type identifier.
func (c *WebClient) GetClientType() string {
	return ClientTypeWeb
}

// setHeaders sets common headers for claude.ai requests.
func (c *WebClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf("sessionKey=%s", c.SessionKey))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
}

// fetchOrganizationID fetches and stores the organization ID.
func (c *WebClient) fetchOrganizationID(ctx context.Context) error {
	orgs, err := c.GetOrganizations(ctx)
	if err != nil {
		return err
	}
	if len(orgs) == 0 {
		return fmt.Errorf("no organizations found")
	}
	c.OrganizationID = orgs[0].UUID
	return nil
}

// buildPromptFromMessages converts API-style messages to a single prompt.
func (c *WebClient) buildPromptFromMessages(messages []Message, systemPrompt string) string {
	var prompt strings.Builder

	if systemPrompt != "" {
		prompt.WriteString("System: ")
		prompt.WriteString(systemPrompt)
		prompt.WriteString("\n\n")
	}

	for _, msg := range messages {
		for _, content := range msg.Content {
			if content.Type == "text" {
				if msg.Role == "user" {
					prompt.WriteString("Human: ")
				} else if msg.Role == "assistant" {
					prompt.WriteString("Assistant: ")
				}
				prompt.WriteString(content.Text)
				prompt.WriteString("\n\n")
			}
		}
	}

	return strings.TrimSpace(prompt.String())
}

// generateUUID generates a simple UUID for conversations.
func generateUUID() string {
	// Simple UUID generation - in production, use a proper UUID library
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
