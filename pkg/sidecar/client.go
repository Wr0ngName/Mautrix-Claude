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
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Retry and circuit breaker configuration
const (
	maxRetries       = 3
	initialBackoff   = 100 * time.Millisecond
	maxBackoff       = 5 * time.Second
	circuitThreshold = 5                // failures before opening circuit
	circuitTimeout   = 30 * time.Second // time before trying again
)

// CircuitState represents the state of the circuit breaker
type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// Client is an HTTP client for the Claude Agent SDK sidecar.
type Client struct {
	baseURL    string
	httpClient *http.Client
	log        zerolog.Logger

	// Circuit breaker state
	mu               sync.Mutex
	circuitState     CircuitState
	consecutiveFails int
	lastFailTime     time.Time
}

// ChatRequest is the request body for the chat endpoint.
type ChatRequest struct {
	PortalID        string  `json:"portal_id"`
	UserID          string  `json:"user_id,omitempty"`          // Matrix user ID for per-user sessions
	CredentialsJSON string  `json:"credentials_json,omitempty"` // User's Claude credentials for Pro/Max
	Message         string  `json:"message"`
	SystemPrompt    *string `json:"system_prompt,omitempty"`
	Model           *string `json:"model,omitempty"`
	Stream          bool    `json:"stream"`
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
	Status        string  `json:"status"`        // "healthy" or "degraded"
	Sessions      int     `json:"sessions"`      // Active session count
	Authenticated bool    `json:"authenticated"` // Whether Claude Code auth is valid
	Message       *string `json:"message"`       // Error message if not authenticated
}

// TestAuthRequest is the request body for testing user credentials.
type TestAuthRequest struct {
	UserID          string `json:"user_id"`
	CredentialsJSON string `json:"credentials_json"`
}

// TestAuthResponse is the response from the auth test endpoint.
type TestAuthResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// NewClient creates a new sidecar client with the specified timeout.
func NewClient(baseURL string, timeout time.Duration, log zerolog.Logger) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Minute // Default timeout for Claude responses
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		log:          log.With().Str("component", "sidecar-client").Logger(),
		circuitState: CircuitClosed,
	}
}

// checkCircuit checks if a request should be allowed based on circuit state.
// Returns true if request should proceed, false if circuit is open.
func (c *Client) checkCircuit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.circuitState {
	case CircuitOpen:
		// Check if timeout has passed
		if time.Since(c.lastFailTime) >= circuitTimeout {
			c.circuitState = CircuitHalfOpen
			c.log.Info().Msg("Circuit breaker: half-open, allowing test request")
			return true
		}
		return false
	case CircuitHalfOpen, CircuitClosed:
		return true
	}
	return true
}

// recordSuccess records a successful request.
func (c *Client) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.consecutiveFails = 0
	if c.circuitState == CircuitHalfOpen {
		c.circuitState = CircuitClosed
		c.log.Info().Msg("Circuit breaker: closed (recovered)")
	}
}

// recordFailure records a failed request.
func (c *Client) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.consecutiveFails++
	c.lastFailTime = time.Now()

	if c.consecutiveFails >= circuitThreshold && c.circuitState != CircuitOpen {
		c.circuitState = CircuitOpen
		c.log.Warn().Int("failures", c.consecutiveFails).Msg("Circuit breaker: opened due to consecutive failures")
	}
}

// isRetryable checks if an error or status code is retryable.
func isRetryable(err error, statusCode int) bool {
	if err != nil {
		return true // Network errors are retryable
	}
	// Retry on 5xx server errors and 429 rate limit
	return statusCode >= 500 || statusCode == 429
}

// backoff calculates exponential backoff duration.
func backoff(attempt int) time.Duration {
	delay := initialBackoff * time.Duration(1<<uint(attempt))
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
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

// TestAuth tests user credentials by making a minimal Claude API call.
func (c *Client) TestAuth(ctx context.Context, userID, credentialsJSON string) (*TestAuthResponse, error) {
	reqBody := TestAuthRequest{
		UserID:          userID,
		CredentialsJSON: credentialsJSON,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/auth/test", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth test failed: %s - %s", resp.Status, string(body))
	}

	var authResp TestAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &authResp, nil
}

// Chat sends a message to Claude and returns the response.
// Includes retry logic with exponential backoff and circuit breaker protection.
func (c *Client) Chat(ctx context.Context, portalID, userID, credentialsJSON, message string, systemPrompt, model *string) (*ChatResponse, error) {
	// Check circuit breaker
	if !c.checkCircuit() {
		return nil, fmt.Errorf("circuit breaker open: sidecar temporarily unavailable")
	}

	reqBody := ChatRequest{
		PortalID:        portalID,
		UserID:          userID,
		CredentialsJSON: credentialsJSON,
		Message:         message,
		SystemPrompt:    systemPrompt,
		Model:           model,
		Stream:          false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	c.log.Debug().
		Str("portal_id", portalID).
		Str("message_preview", truncate(message, 50)).
		Msg("Sending chat request to sidecar")

	var lastErr error
	startTime := time.Now()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoff(attempt - 1)
			c.log.Debug().Int("attempt", attempt+1).Dur("backoff", delay).Msg("Retrying chat request")
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("failed to make request: %w", err)
			if isRetryable(err, 0) && attempt < maxRetries {
				continue
			}
			c.recordFailure()
			return nil, lastErr
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("chat request failed: %s - %s", resp.Status, string(body))
			if isRetryable(nil, resp.StatusCode) && attempt < maxRetries {
				continue
			}
			c.recordFailure()
			return nil, lastErr
		}

		var chatResp ChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		resp.Body.Close()

		// Success - record and return
		c.recordSuccess()

		c.log.Debug().
			Str("portal_id", portalID).
			Str("session_id", chatResp.SessionID).
			Dur("duration", time.Since(startTime)).
			Int("attempts", attempt+1).
			Str("response_preview", truncate(chatResp.Response, 50)).
			Msg("Received chat response from sidecar")

		return &chatResp, nil
	}

	c.recordFailure()
	return nil, lastErr
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
