// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	RetryConfig    RetryConfig
	// AllCookies contains all cookies needed for authentication (sessionKey + cf_clearance etc.)
	// If set, this is used instead of just SessionKey
	AllCookies string
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
		SessionKey:  sessionKey,
		BaseURL:     "https://claude.ai",
		Log:         log,
		Metrics:     NewMetrics(),
		RetryConfig: DefaultRetryConfig(),
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
	var orgs []webOrganization
	var lastErr error

	for attempt := 0; attempt <= c.RetryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			c.Metrics.RecordRetry()
			c.Log.Debug().Int("attempt", attempt).Msg("Retrying get organizations request")
		}

		req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/organizations", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		c.setHeaders(req)

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			if !c.RetryConfig.ShouldRetry(attempt, lastErr) {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, lastErr); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = c.parseWebError(resp)
			resp.Body.Close()
			if !c.RetryConfig.ShouldRetry(attempt, lastErr) {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, lastErr); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			continue
		}

		if err := json.NewDecoder(resp.Body).Decode(&orgs); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		resp.Body.Close()
		return orgs, nil
	}

	if lastErr != nil {
		c.Metrics.RecordError(lastErr)
	}
	return nil, lastErr
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
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var conv *webConversation
	var lastErr error

	for attempt := 0; attempt <= c.RetryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			c.Metrics.RecordRetry()
			c.Log.Debug().Int("attempt", attempt).Msg("Retrying create conversation request")
		}

		url := fmt.Sprintf("%s/api/organizations/%s/chat_conversations", c.BaseURL, c.OrganizationID)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		c.setHeaders(req)

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			if !c.RetryConfig.ShouldRetry(attempt, lastErr) {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, lastErr); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			continue
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			lastErr = c.parseWebError(resp)
			resp.Body.Close()
			if !c.RetryConfig.ShouldRetry(attempt, lastErr) {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, lastErr); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			continue
		}

		conv = &webConversation{}
		if err := json.NewDecoder(resp.Body).Decode(conv); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		resp.Body.Close()
		return conv, nil
	}

	if lastErr != nil {
		c.Metrics.RecordError(lastErr)
	}
	return nil, lastErr
}

// CreateMessageStream sends a message and returns streaming events.
func (c *WebClient) CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error) {
	startTime := time.Now()

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
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var lastErr error

	for attempt := 0; attempt <= c.RetryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			c.Metrics.RecordRetry()
			c.Log.Debug().Int("attempt", attempt).Str("model", req.Model).Msg("Retrying streaming request")
		}

		url := fmt.Sprintf("%s/api/organizations/%s/chat_conversations/%s/completion",
			c.BaseURL, c.OrganizationID, conv.UUID)

		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		c.setHeaders(httpReq)
		httpReq.Header.Set("Accept", "text/event-stream")

		c.Log.Debug().
			Str("url", url).
			Str("model", req.Model).
			Msg("Sending streaming request")

		resp, err := c.HTTPClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			if !c.RetryConfig.ShouldRetry(attempt, lastErr) {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, lastErr); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = c.parseWebError(resp)
			resp.Body.Close()
			c.Log.Debug().
				Int("status_code", resp.StatusCode).
				Err(lastErr).
				Msg("API returned error")
			if !c.RetryConfig.ShouldRetry(attempt, lastErr) {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, lastErr); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}
			continue
		}

		// Success - record metrics and return stream
		c.Metrics.RecordRequest(req.Model, time.Since(startTime), 0, 0)
		return c.streamWebMessages(ctx, resp, req.Model, conv.UUID)
	}

	if lastErr != nil {
		c.Metrics.RecordError(lastErr)
	}
	return nil, lastErr
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
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Referer", "https://claude.ai/")

	// Use full cookie string if provided, otherwise just sessionKey
	if c.AllCookies != "" {
		req.Header.Set("Cookie", c.AllCookies)
	} else {
		req.Header.Set("Cookie", fmt.Sprintf("sessionKey=%s", c.SessionKey))
	}

	// Use a recent Chrome user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
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

// generateUUID generates a UUID v4 for conversations.
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp if crypto/rand fails (shouldn't happen)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	// Set version (4) and variant (2) bits per RFC 4122
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}

// parseWebError parses error responses from the claude.ai web API.
func (c *WebClient) parseWebError(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &APIError{
			Type:    "read_error",
			Message: fmt.Sprintf("HTTP %d: failed to read response body", resp.StatusCode),
		}
	}

	// Try to parse as JSON error
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(body, &errResp); err == nil {
		if errResp.Error.Message != "" {
			return &APIError{
				Type:    errResp.Error.Type,
				Message: errResp.Error.Message,
			}
		}
		if errResp.Message != "" {
			return &APIError{
				Type:    "web_error",
				Message: errResp.Message,
			}
		}
	}

	// Return raw body as error message
	return &APIError{
		Type:    "web_error",
		Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
	}
}
