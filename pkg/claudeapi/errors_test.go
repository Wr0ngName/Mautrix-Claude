// Package claudeapi provides tests for error handling.
package claudeapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseAPIError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		body           string
		expectError    bool
		expectType     string
		expectContains string
	}{
		{
			name:           "parses authentication error",
			statusCode:     http.StatusUnauthorized,
			body:           `{"type":"authentication_error","message":"invalid api key"}`,
			expectError:    true,
			expectType:     "authentication_error",
			expectContains: "invalid api key",
		},
		{
			name:           "parses rate limit error",
			statusCode:     http.StatusTooManyRequests,
			body:           `{"type":"rate_limit_error","message":"rate limit exceeded"}`,
			expectError:    true,
			expectType:     "rate_limit_error",
			expectContains: "rate limit",
		},
		{
			name:           "parses API error",
			statusCode:     http.StatusInternalServerError,
			body:           `{"type":"api_error","message":"internal server error"}`,
			expectError:    true,
			expectType:     "api_error",
			expectContains: "internal server error",
		},
		{
			name:           "parses invalid request error",
			statusCode:     http.StatusBadRequest,
			body:           `{"type":"invalid_request_error","message":"invalid model"}`,
			expectError:    true,
			expectType:     "invalid_request_error",
			expectContains: "invalid model",
		},
		{
			name:        "handles malformed JSON",
			statusCode:  http.StatusBadRequest,
			body:        `{invalid json}`,
			expectError: true,
		},
		{
			name:        "handles empty body",
			statusCode:  http.StatusInternalServerError,
			body:        "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       http.NoBody,
			}

			if tt.body != "" {
				resp.Body = &mockReadCloser{strings.NewReader(tt.body)}
			}

			err := ParseAPIError(resp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if tt.expectError && err != nil {
				errMsg := err.Error()
				if tt.expectContains != "" && !strings.Contains(errMsg, tt.expectContains) {
					t.Errorf("expected error to contain %q, got %q", tt.expectContains, errMsg)
				}

				// Check if error type is preserved
				var apiErr *APIError
				if errors.As(err, &apiErr) {
					if tt.expectType != "" && apiErr.Type != tt.expectType {
						t.Errorf("expected error type %q, got %q", tt.expectType, apiErr.Type)
					}
				}
			}
		})
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		isRateErr bool
	}{
		{
			name: "identifies rate limit error",
			err: &APIError{
				Type:    "rate_limit_error",
				Message: "rate limit exceeded",
			},
			isRateErr: true,
		},
		{
			name: "identifies 429 status",
			err: &APIError{
				Type:    "rate_limit_error",
				Message: "too many requests",
			},
			isRateErr: true,
		},
		{
			name: "non-rate-limit error",
			err: &APIError{
				Type:    "api_error",
				Message: "internal error",
			},
			isRateErr: false,
		},
		{
			name:      "nil error",
			err:       nil,
			isRateErr: false,
		},
		{
			name:      "generic error",
			err:       errors.New("generic error"),
			isRateErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isRate := IsRateLimitError(tt.err)

			if isRate != tt.isRateErr {
				t.Errorf("expected IsRateLimitError = %v, got %v", tt.isRateErr, isRate)
			}
		})
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		isAuthErr bool
	}{
		{
			name: "identifies authentication error",
			err: &APIError{
				Type:    "authentication_error",
				Message: "invalid api key",
			},
			isAuthErr: true,
		},
		{
			name: "identifies permission error",
			err: &APIError{
				Type:    "permission_error",
				Message: "forbidden",
			},
			isAuthErr: true,
		},
		{
			name: "non-auth error",
			err: &APIError{
				Type:    "api_error",
				Message: "internal error",
			},
			isAuthErr: false,
		},
		{
			name:      "nil error",
			err:       nil,
			isAuthErr: false,
		},
		{
			name:      "generic error",
			err:       errors.New("generic error"),
			isAuthErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAuth := IsAuthError(tt.err)

			if isAuth != tt.isAuthErr {
				t.Errorf("expected IsAuthError = %v, got %v", tt.isAuthErr, isAuth)
			}
		})
	}
}

func TestGetRetryAfter(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectSeconds int
	}{
		{
			name: "parses retry-after from error",
			err: &APIError{
				Type:       "rate_limit_error",
				Message:    "rate limit exceeded",
				RetryAfter: 60,
			},
			expectSeconds: 60,
		},
		{
			name: "returns zero for non-rate-limit error",
			err: &APIError{
				Type:    "api_error",
				Message: "internal error",
			},
			expectSeconds: 0,
		},
		{
			name:          "returns zero for nil error",
			err:           nil,
			expectSeconds: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration := GetRetryAfter(tt.err)

			expectDuration := time.Duration(tt.expectSeconds) * time.Second

			if duration != expectDuration {
				t.Errorf("expected retry after %v, got %v", expectDuration, duration)
			}
		})
	}
}

func TestAPIErrorImplementsError(t *testing.T) {
	t.Run("APIError implements error interface", func(t *testing.T) {
		apiErr := &APIError{
			Type:    "test_error",
			Message: "test message",
		}

		var err error = apiErr

		if err == nil {
			t.Error("APIError should implement error interface")
		}

		errMsg := err.Error()
		if errMsg == "" {
			t.Error("Error() should return non-empty string")
		}

		if !strings.Contains(errMsg, "test_error") {
			t.Error("Error() should contain error type")
		}
	})
}

func TestIsOverloadedError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		isOverloaded bool
	}{
		{
			name: "identifies overloaded error (529)",
			err: &APIError{
				Type:    "overloaded_error",
				Message: "service overloaded",
			},
			isOverloaded: true,
		},
		{
			name: "non-overloaded error",
			err: &APIError{
				Type:    "api_error",
				Message: "generic error",
			},
			isOverloaded: false,
		},
		{
			name:         "nil error",
			err:          nil,
			isOverloaded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isOverloaded := IsOverloadedError(tt.err)

			if isOverloaded != tt.isOverloaded {
				t.Errorf("expected IsOverloadedError = %v, got %v", tt.isOverloaded, isOverloaded)
			}
		})
	}
}

func TestIsInvalidRequestError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		isInvalid bool
	}{
		{
			name: "identifies invalid request error",
			err: &APIError{
				Type:    "invalid_request_error",
				Message: "invalid parameter",
			},
			isInvalid: true,
		},
		{
			name: "non-invalid-request error",
			err: &APIError{
				Type:    "api_error",
				Message: "generic error",
			},
			isInvalid: false,
		},
		{
			name:      "nil error",
			err:       nil,
			isInvalid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isInvalid := IsInvalidRequestError(tt.err)

			if isInvalid != tt.isInvalid {
				t.Errorf("expected IsInvalidRequestError = %v, got %v", tt.isInvalid, isInvalid)
			}
		})
	}
}

// Mock types for testing

type mockReadCloser struct {
	*strings.Reader
}

func (m *mockReadCloser) Close() error {
	return nil
}
