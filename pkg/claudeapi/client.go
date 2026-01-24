// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Client is a client for the Claude API.
type Client struct {
	HTTPClient  *http.Client
	APIKey      string
	BaseURL     string
	Version     string
	Log         zerolog.Logger
	Metrics     *Metrics
	RetryConfig RetryConfig
}

// ClientOption is a function that configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.HTTPClient = httpClient
	}
}

// WithBaseURL sets the base URL.
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) {
		c.BaseURL = baseURL
	}
}

// WithVersion sets the API version.
func WithVersion(version string) ClientOption {
	return func(c *Client) {
		c.Version = version
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.HTTPClient.Timeout = timeout
	}
}

// WithMetrics sets the metrics collector.
func WithMetrics(metrics *Metrics) ClientOption {
	return func(c *Client) {
		c.Metrics = metrics
	}
}

// WithRetryConfig sets the retry configuration.
func WithRetryConfig(config RetryConfig) ClientOption {
	return func(c *Client) {
		c.RetryConfig = config
	}
}

// NewClient creates a new Claude API client.
func NewClient(apiKey string, log zerolog.Logger, opts ...ClientOption) *Client {
	client := &Client{
		HTTPClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		APIKey:      apiKey,
		BaseURL:     DefaultBaseURL,
		Version:     DefaultVersion,
		Log:         log,
		Metrics:     NewMetrics(),
		RetryConfig: DefaultRetryConfig(),
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// CreateMessage creates a new message (non-streaming).
func (c *Client) CreateMessage(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResponse, error) {
	// Ensure stream is false
	req.Stream = false

	var response *CreateMessageResponse
	var lastErr error
	startTime := time.Now()

	// Retry loop
	for attempt := 0; attempt <= c.RetryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			c.Metrics.RecordRetry()
			c.Log.Debug().
				Int("attempt", attempt).
				Str("model", req.Model).
				Msg("Retrying API request")
		}

		resp, err := c.doRequest(ctx, req)
		if err != nil {
			lastErr = err

			// Check if we should retry
			if !c.RetryConfig.ShouldRetry(attempt, err) {
				c.Metrics.RecordError(err)
				return nil, err
			}

			// Wait before retrying
			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, err); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}

			continue
		}

		response = resp
		break
	}

	if response == nil && lastErr != nil {
		c.Metrics.RecordError(lastErr)
		return nil, lastErr
	}

	// Record successful request metrics
	duration := time.Since(startTime)
	inputTokens := 0
	outputTokens := 0
	if response != nil && response.Usage != nil {
		inputTokens = response.Usage.InputTokens
		outputTokens = response.Usage.OutputTokens
	}
	c.Metrics.RecordRequest(req.Model, duration, inputTokens, outputTokens)

	return response, nil
}

// doRequest performs a single API request without retry logic.
func (c *Client) doRequest(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/messages", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", c.Version)
	httpReq.Header.Set("content-type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ParseAPIError(resp)
	}

	var response CreateMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response, nil
}

// CreateMessageStream creates a new message with streaming.
func (c *Client) CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error) {
	// Ensure stream is true
	req.Stream = true

	var lastErr error

	// Retry loop for initial connection
	for attempt := 0; attempt <= c.RetryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			c.Metrics.RecordRetry()
			c.Log.Debug().
				Int("attempt", attempt).
				Str("model", req.Model).
				Msg("Retrying streaming API request")
		}

		reqBody, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/messages", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}

		httpReq.Header.Set("x-api-key", c.APIKey)
		httpReq.Header.Set("anthropic-version", c.Version)
		httpReq.Header.Set("content-type", "application/json")

		resp, err := c.HTTPClient.Do(httpReq)
		if err != nil {
			lastErr = err

			if !c.RetryConfig.ShouldRetry(attempt, err) {
				c.Metrics.RecordError(err)
				return nil, err
			}

			if waitErr := c.RetryConfig.WaitForRetry(ctx, attempt, err); waitErr != nil {
				c.Metrics.RecordError(lastErr)
				return nil, lastErr
			}

			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = ParseAPIError(resp)
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

		// Success - start streaming
		return c.streamMessages(ctx, resp, req.Model)
	}

	if lastErr != nil {
		c.Metrics.RecordError(lastErr)
	}
	return nil, lastErr
}

// ValidateAPIKey validates the API key by making a test request.
func (c *Client) ValidateAPIKey(ctx context.Context) error {
	// Make a minimal request to validate the API key
	req := &CreateMessageRequest{
		Model: DefaultModel,
		Messages: []Message{
			{
				Role: "user",
				Content: []Content{
					{Type: "text", Text: "test"},
				},
			},
		},
		MaxTokens: 1,
	}

	_, err := c.CreateMessage(ctx, req)
	return err
}

// GetMetrics returns the metrics collector.
func (c *Client) GetMetrics() *Metrics {
	return c.Metrics
}
