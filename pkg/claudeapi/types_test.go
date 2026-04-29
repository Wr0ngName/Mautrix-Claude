package claudeapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestIsRateLimitError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if IsRateLimitError(nil) {
			t.Error("nil error should not be rate limit")
		}
	})

	t.Run("rate_limit string in error", func(t *testing.T) {
		if !IsRateLimitError(fmt.Errorf("rate_limit exceeded")) {
			t.Error("should detect rate_limit in error string")
		}
	})

	t.Run("429 string in error", func(t *testing.T) {
		if !IsRateLimitError(fmt.Errorf("HTTP 429 too many requests")) {
			t.Error("should detect 429 in error string")
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if IsRateLimitError(fmt.Errorf("connection refused")) {
			t.Error("should not detect unrelated error as rate limit")
		}
	})
}

func TestIsAuthError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if IsAuthError(nil) {
			t.Error("nil error should not be auth error")
		}
	})

	t.Run("authentication string in error", func(t *testing.T) {
		if !IsAuthError(fmt.Errorf("authentication failed")) {
			t.Error("should detect authentication in error string")
		}
	})

	t.Run("unauthorized string in error", func(t *testing.T) {
		if !IsAuthError(fmt.Errorf("unauthorized access")) {
			t.Error("should detect unauthorized in error string")
		}
	})

	t.Run("invalid_api_key string in error", func(t *testing.T) {
		if !IsAuthError(fmt.Errorf("invalid_api_key")) {
			t.Error("should detect invalid_api_key in error string")
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if IsAuthError(fmt.Errorf("timeout waiting for response")) {
			t.Error("should not detect unrelated error as auth error")
		}
	})
}

func TestIsOverloadedError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if IsOverloadedError(nil) {
			t.Error("nil error should not be overloaded")
		}
	})

	t.Run("overloaded string in error", func(t *testing.T) {
		if !IsOverloadedError(fmt.Errorf("service overloaded")) {
			t.Error("should detect overloaded in error string")
		}
	})

	t.Run("server_error string in error", func(t *testing.T) {
		if !IsOverloadedError(fmt.Errorf("server_error occurred")) {
			t.Error("should detect server_error in error string")
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if IsOverloadedError(fmt.Errorf("invalid request")) {
			t.Error("should not detect unrelated error as overloaded")
		}
	})
}

func TestIsInvalidRequestError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if IsInvalidRequestError(nil) {
			t.Error("nil error should not be invalid request")
		}
	})

	t.Run("invalid_request string in error", func(t *testing.T) {
		if !IsInvalidRequestError(fmt.Errorf("invalid_request: missing model")) {
			t.Error("should detect invalid_request in error string")
		}
	})

	t.Run("bad request string in error", func(t *testing.T) {
		if !IsInvalidRequestError(fmt.Errorf("bad request")) {
			t.Error("should detect bad request in error string")
		}
	})

	t.Run("unrelated error", func(t *testing.T) {
		if IsInvalidRequestError(fmt.Errorf("rate limit exceeded")) {
			t.Error("should not detect unrelated error as invalid request")
		}
	})
}

func TestErrorClassifiersAreMutuallyExclusive(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		rateLimit  bool
		auth       bool
		overloaded bool
		invalid    bool
	}{
		{"rate_limit", fmt.Errorf("rate_limit"), true, false, false, false},
		{"authentication", fmt.Errorf("authentication failed"), false, true, false, false},
		{"overloaded", fmt.Errorf("overloaded"), false, false, true, false},
		{"invalid_request", fmt.Errorf("invalid_request"), false, false, false, true},
		{"generic error", fmt.Errorf("something went wrong"), false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRateLimitError(tt.err); got != tt.rateLimit {
				t.Errorf("IsRateLimitError = %v, want %v", got, tt.rateLimit)
			}
			if got := IsAuthError(tt.err); got != tt.auth {
				t.Errorf("IsAuthError = %v, want %v", got, tt.auth)
			}
			if got := IsOverloadedError(tt.err); got != tt.overloaded {
				t.Errorf("IsOverloadedError = %v, want %v", got, tt.overloaded)
			}
			if got := IsInvalidRequestError(tt.err); got != tt.invalid {
				t.Errorf("IsInvalidRequestError = %v, want %v", got, tt.invalid)
			}
		})
	}
}

func TestGetRetryAfter(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if got := GetRetryAfter(nil); got != 0 {
			t.Errorf("expected 0, got %v", got)
		}
	})

	t.Run("rate limit error without header returns default 30s", func(t *testing.T) {
		err := fmt.Errorf("rate_limit exceeded")
		got := GetRetryAfter(err)
		if got != 30*time.Second {
			t.Errorf("expected 30s default for rate limit, got %v", got)
		}
	})

	t.Run("non-rate-limit error returns 0", func(t *testing.T) {
		err := fmt.Errorf("something went wrong")
		got := GetRetryAfter(err)
		if got != 0 {
			t.Errorf("expected 0 for non-rate-limit error, got %v", got)
		}
	})

	t.Run("SDK error with Retry-After header", func(t *testing.T) {
		resp := &http.Response{
			Header: http.Header{"Retry-After": []string{"60"}},
		}
		sdkErr := &anthropic.Error{
			StatusCode: 429,
			Response:   resp,
		}
		got := GetRetryAfter(sdkErr)
		if got != 60*time.Second {
			t.Errorf("expected 60s from header, got %v", got)
		}
	})

	t.Run("SDK error without Retry-After header falls back to default", func(t *testing.T) {
		resp := &http.Response{
			Header: http.Header{},
		}
		sdkErr := &anthropic.Error{
			StatusCode: 429,
			Response:   resp,
		}
		got := GetRetryAfter(sdkErr)
		if got != 30*time.Second {
			t.Errorf("expected 30s default, got %v", got)
		}
	})
}

func TestAPIError(t *testing.T) {
	t.Run("Error method", func(t *testing.T) {
		err := &APIError{
			Type:    "rate_limit_error",
			Message: "too many requests",
		}
		got := err.Error()
		expected := "rate_limit_error: too many requests"
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})

	t.Run("implements error interface", func(t *testing.T) {
		var err error = &APIError{Type: "test", Message: "test"}
		if err == nil {
			t.Error("APIError should implement error interface")
		}
	})
}
