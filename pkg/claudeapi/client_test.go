// Package claudeapi provides tests for the Claude API client.
package claudeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
	}{
		{
			name:   "creates client with valid API key",
			apiKey: "sk-ant-test-key-123",
		},
		{
			name:   "creates client with empty API key",
			apiKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := zerolog.Nop()
			client := NewClient(tt.apiKey, log)

			if client == nil {
				t.Fatal("NewClient returned nil")
			}

			if client.APIKey != tt.apiKey {
				t.Errorf("expected API key %q, got %q", tt.apiKey, client.APIKey)
			}

			if client.BaseURL == "" {
				t.Error("BaseURL should not be empty")
			}

			if client.Version == "" {
				t.Error("Version should not be empty")
			}

			if client.HTTPClient == nil {
				t.Error("HTTPClient should not be nil")
			}
		})
	}
}

func TestCreateMessage(t *testing.T) {
	tests := []struct {
		name           string
		request        *CreateMessageRequest
		mockResponse   *CreateMessageResponse
		mockStatusCode int
		expectError    bool
	}{
		{
			name: "successful message creation",
			request: &CreateMessageRequest{
				Model: "claude-3-5-sonnet-20241022",
				Messages: []Message{
					{
						Role: "user",
						Content: []Content{
							{Type: "text", Text: "Hello, Claude!"},
						},
					},
				},
				MaxTokens: 1024,
			},
			mockResponse: &CreateMessageResponse{
				ID:   "msg_123",
				Type: "message",
				Role: "assistant",
				Content: []Content{
					{Type: "text", Text: "Hello! How can I help you?"},
				},
				Model:      "claude-3-5-sonnet-20241022",
				StopReason: "end_turn",
				Usage: &Usage{
					InputTokens:  10,
					OutputTokens: 15,
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    false,
		},
		{
			name: "handles empty messages",
			request: &CreateMessageRequest{
				Model:     "claude-3-5-sonnet-20241022",
				Messages:  []Message{},
				MaxTokens: 1024,
			},
			mockStatusCode: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify headers
				if r.Header.Get("x-api-key") == "" {
					t.Error("x-api-key header not set")
				}
				if r.Header.Get("anthropic-version") == "" {
					t.Error("anthropic-version header not set")
				}
				if r.Header.Get("content-type") != "application/json" {
					t.Error("content-type header should be application/json")
				}

				w.WriteHeader(tt.mockStatusCode)
				if tt.mockResponse != nil {
					json.NewEncoder(w).Encode(tt.mockResponse)
				}
			}))
			defer server.Close()

			log := zerolog.Nop()
			client := NewClient("sk-ant-test-key", log)
			client.BaseURL = server.URL

			resp, err := client.CreateMessage(context.Background(), tt.request)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if !tt.expectError && resp == nil {
				t.Error("expected response, got nil")
			}

			if !tt.expectError && resp != nil {
				if resp.ID != tt.mockResponse.ID {
					t.Errorf("expected ID %q, got %q", tt.mockResponse.ID, resp.ID)
				}
			}
		})
	}
}

func TestCreateMessageAPIErrors(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		responseBody   interface{}
		expectErrorMsg string
	}{
		{
			name:       "401 unauthorized",
			statusCode: http.StatusUnauthorized,
			responseBody: APIError{
				Type:    "authentication_error",
				Message: "invalid api key",
			},
			expectErrorMsg: "authentication_error",
		},
		{
			name:       "429 rate limit",
			statusCode: http.StatusTooManyRequests,
			responseBody: APIError{
				Type:    "rate_limit_error",
				Message: "rate limit exceeded",
			},
			expectErrorMsg: "rate_limit_error",
		},
		{
			name:       "500 server error",
			statusCode: http.StatusInternalServerError,
			responseBody: APIError{
				Type:    "api_error",
				Message: "internal server error",
			},
			expectErrorMsg: "api_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(tt.responseBody)
			}))
			defer server.Close()

			log := zerolog.Nop()
			client := NewClient("sk-ant-test-key", log)
			client.BaseURL = server.URL

			req := &CreateMessageRequest{
				Model: "claude-3-5-sonnet-20241022",
				Messages: []Message{
					{
						Role: "user",
						Content: []Content{
							{Type: "text", Text: "test"},
						},
					},
				},
				MaxTokens: 1024,
			}

			_, err := client.CreateMessage(context.Background(), req)

			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestValidateAPIKey(t *testing.T) {
	tests := []struct {
		name           string
		apiKey         string
		mockStatusCode int
		expectError    bool
	}{
		{
			name:           "valid API key",
			apiKey:         "sk-ant-valid-key",
			mockStatusCode: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "invalid API key",
			apiKey:         "sk-ant-invalid-key",
			mockStatusCode: http.StatusUnauthorized,
			expectError:    true,
		},
		{
			name:           "empty API key",
			apiKey:         "",
			mockStatusCode: http.StatusUnauthorized,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.mockStatusCode)
				if tt.mockStatusCode == http.StatusOK {
					json.NewEncoder(w).Encode(&CreateMessageResponse{
						ID:   "msg_test",
						Type: "message",
						Role: "assistant",
						Content: []Content{
							{Type: "text", Text: "test"},
						},
					})
				}
			}))
			defer server.Close()

			log := zerolog.Nop()
			client := NewClient(tt.apiKey, log)
			client.BaseURL = server.URL

			err := client.ValidateAPIKey(context.Background())

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCreateMessageWithContext(t *testing.T) {
	t.Run("cancels request when context is cancelled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate slow response
			<-r.Context().Done()
		}))
		defer server.Close()

		log := zerolog.Nop()
		client := NewClient("sk-ant-test-key", log)
		client.BaseURL = server.URL

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		req := &CreateMessageRequest{
			Model: "claude-3-5-sonnet-20241022",
			Messages: []Message{
				{
					Role: "user",
					Content: []Content{
						{Type: "text", Text: "test"},
					},
				},
			},
			MaxTokens: 1024,
		}

		_, err := client.CreateMessage(ctx, req)

		if err == nil {
			t.Error("expected context cancellation error, got nil")
		}
	})
}
