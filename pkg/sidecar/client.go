// Package sidecar provides a client for the Claude Agent SDK sidecar.
// This allows the bridge to use Pro/Max subscriptions via the Agent SDK.
package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Client is an HTTP client for the Claude Agent SDK sidecar.
type Client struct {
	baseURL    string
	httpClient *http.Client
	log        zerolog.Logger
}

// ChatRequest is the request body for the chat endpoint.
type ChatRequest struct {
	PortalID     string  `json:"portal_id"`
	Message      string  `json:"message"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
	Model        *string `json:"model,omitempty"`
	Stream       bool    `json:"stream"`
}

// ChatResponse is the response body from the chat endpoint.
type ChatResponse struct {
	PortalID   string `json:"portal_id"`
	SessionID  string `json:"session_id"`
	Response   string `json:"response"`
	TokensUsed *int   `json:"tokens_used,omitempty"`
}

// SessionStats contains statistics about a session.
type SessionStats struct {
	SessionID    string  `json:"session_id"`
	PortalID     string  `json:"portal_id"`
	CreatedAt    float64 `json:"created_at"`
	LastUsed     float64 `json:"last_used"`
	MessageCount int     `json:"message_count"`
	AgeSeconds   float64 `json:"age_seconds"`
}

// HealthResponse is the response from the health endpoint.
type HealthResponse struct {
	Status   string `json:"status"`
	Sessions int    `json:"sessions"`
}

// NewClient creates a new sidecar client.
func NewClient(baseURL string, log zerolog.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // Long timeout for Claude responses
		},
		log: log.With().Str("component", "sidecar-client").Logger(),
	}
}

// Health checks if the sidecar is healthy.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("health check failed: %s - %s", resp.Status, string(body))
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &health, nil
}

// Chat sends a message to Claude and returns the response.
func (c *Client) Chat(ctx context.Context, portalID, message string, systemPrompt, model *string) (*ChatResponse, error) {
	reqBody := ChatRequest{
		PortalID:     portalID,
		Message:      message,
		SystemPrompt: systemPrompt,
		Model:        model,
		Stream:       false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	c.log.Debug().
		Str("portal_id", portalID).
		Str("message_preview", truncate(message, 50)).
		Msg("Sending chat request to sidecar")

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("chat request failed: %s - %s", resp.Status, string(body))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.log.Debug().
		Str("portal_id", portalID).
		Str("session_id", chatResp.SessionID).
		Dur("duration", time.Since(startTime)).
		Str("response_preview", truncate(chatResp.Response, 50)).
		Msg("Received chat response from sidecar")

	return &chatResp, nil
}

// DeleteSession clears the conversation history for a portal.
func (c *Client) DeleteSession(ctx context.Context, portalID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/v1/sessions/"+portalID, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session failed: %s - %s", resp.Status, string(body))
	}

	c.log.Debug().
		Str("portal_id", portalID).
		Msg("Deleted sidecar session")

	return nil
}

// GetSession gets statistics about a session.
func (c *Client) GetSession(ctx context.Context, portalID string) (*SessionStats, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/sessions/"+portalID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Session doesn't exist
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get session failed: %s - %s", resp.Status, string(body))
	}

	var stats SessionStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &stats, nil
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
