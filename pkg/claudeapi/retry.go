// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"context"
	"time"
)

// RetryConfig configures retry behavior.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts.
	MaxRetries int

	// InitialDelay is the initial delay before the first retry.
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries.
	MaxDelay time.Duration

	// BackoffMultiplier is the multiplier for exponential backoff.
	BackoffMultiplier float64
}

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:        MaxRetries,
		InitialDelay:      InitialRetryDelay,
		MaxDelay:          MaxRetryDelay,
		BackoffMultiplier: RetryBackoffMultiplier,
	}
}

// IsRetryableError checks if an error is retryable.
// Retryable errors include rate limits, server overload, and server errors.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Rate limit errors are retryable
	if IsRateLimitError(err) {
		return true
	}

	// Overloaded errors (529) are retryable
	if IsOverloadedError(err) {
		return true
	}

	// Server errors (5xx) might be retryable
	var apiErr *APIError
	if AsAPIError(err, &apiErr) {
		// Generic server errors
		if apiErr.Type == "api_error" || apiErr.Type == "server_error" {
			return true
		}
	}

	return false
}

// AsAPIError is a helper to extract an APIError from an error.
func AsAPIError(err error, target **APIError) bool {
	if err == nil {
		return false
	}

	// Try to unwrap as APIError
	if apiErr, ok := err.(*APIError); ok {
		*target = apiErr
		return true
	}

	return false
}

// CalculateRetryDelay calculates the delay for a retry attempt.
func (c *RetryConfig) CalculateRetryDelay(attempt int, err error) time.Duration {
	// Check for Retry-After header in rate limit errors
	if retryAfter := GetRetryAfter(err); retryAfter > 0 {
		// Respect the server's retry-after, but cap it
		if retryAfter > c.MaxDelay {
			return c.MaxDelay
		}
		return retryAfter
	}

	// Calculate exponential backoff
	delay := c.InitialDelay
	for i := 0; i < attempt; i++ {
		delay = time.Duration(float64(delay) * c.BackoffMultiplier)
		if delay > c.MaxDelay {
			delay = c.MaxDelay
			break
		}
	}

	return delay
}

// ShouldRetry determines if a request should be retried.
func (c *RetryConfig) ShouldRetry(attempt int, err error) bool {
	if attempt >= c.MaxRetries {
		return false
	}

	return IsRetryableError(err)
}

// WaitForRetry waits for the appropriate delay before retrying.
// Returns an error if the context is cancelled during the wait.
func (c *RetryConfig) WaitForRetry(ctx context.Context, attempt int, err error) error {
	delay := c.CalculateRetryDelay(attempt, err)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

// RetryFunc is a function that can be retried.
type RetryFunc func(ctx context.Context) error

// DoWithRetry executes a function with retry logic.
func DoWithRetry(ctx context.Context, config RetryConfig, fn RetryFunc) error {
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if we should retry
		if !config.ShouldRetry(attempt, err) {
			return err
		}

		// Wait before retrying
		if err := config.WaitForRetry(ctx, attempt, lastErr); err != nil {
			return lastErr // Return the original error, not context error
		}
	}

	return lastErr
}
