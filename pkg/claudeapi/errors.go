// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// apiErrorResponse represents the wrapper structure for Claude API errors.
type apiErrorResponse struct {
	Type  string   `json:"type"`
	Error APIError `json:"error"`
}

// ParseAPIError parses an API error from an HTTP response.
func ParseAPIError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &APIError{
			Type:    "unknown_error",
			Message: "failed to read error response: " + err.Error(),
		}
	}

	// First try to parse the nested error format: {"type": "error", "error": {...}}
	var errResp apiErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Type != "" {
		apiErr := errResp.Error
		// Parse Retry-After header for rate limit errors
		if resp.StatusCode == http.StatusTooManyRequests {
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil {
					apiErr.RetryAfter = seconds
				}
			}
		}
		return &apiErr
	}

	// Fallback: try to parse flat error format
	var apiErr APIError
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Type != "" {
		// Parse Retry-After header for rate limit errors
		if resp.StatusCode == http.StatusTooManyRequests {
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil {
					apiErr.RetryAfter = seconds
				}
			}
		}
		return &apiErr
	}

	// If we can't parse the error, create a generic one with the raw body
	return &APIError{
		Type:    "unknown_error",
		Message: "HTTP " + strconv.Itoa(resp.StatusCode) + ": " + string(body),
	}
}

// IsRateLimitError checks if an error is a rate limit error.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Type == "rate_limit_error"
	}

	return false
}

// IsAuthError checks if an error is an authentication error.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Type == "authentication_error" || apiErr.Type == "permission_error"
	}

	return false
}

// IsOverloadedError checks if an error is an overloaded error (529).
func IsOverloadedError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Type == "overloaded_error"
	}

	return false
}

// IsInvalidRequestError checks if an error is an invalid request error.
func IsInvalidRequestError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Type == "invalid_request_error"
	}

	return false
}

// GetRetryAfter returns the retry-after duration from a rate limit error.
// Returns 0 if the error is not a rate limit error or no retry-after is set.
func GetRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Type == "rate_limit_error" && apiErr.RetryAfter > 0 {
			return time.Duration(apiErr.RetryAfter) * time.Second
		}
	}

	return 0
}
