// Package claudeapi provides tests for SSE streaming.
package claudeapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestCreateMessageStream(t *testing.T) {
	tests := []struct {
		name         string
		sseData      string
		expectEvents int
	}{
		{
			name: "receives streaming events",
			sseData: `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`,
			expectEvents: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")

				flusher, ok := w.(http.Flusher)
				if !ok {
					t.Fatal("streaming not supported")
				}

				// Write SSE data
				for _, line := range strings.Split(tt.sseData, "\n") {
					w.Write([]byte(line + "\n"))
					flusher.Flush()
				}
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
							{Type: "text", Text: "Hello"},
						},
					},
				},
				MaxTokens: 1024,
				Stream:    true,
			}

			eventChan, err := client.CreateMessageStream(context.Background(), req)
			if err != nil {
				t.Fatalf("CreateMessageStream failed: %v", err)
			}

			eventCount := 0
			timeout := time.After(2 * time.Second)

			for {
				select {
				case event, ok := <-eventChan:
					if !ok {
						// Channel closed, test complete
						if eventCount != tt.expectEvents {
							t.Errorf("expected %d events, got %d", tt.expectEvents, eventCount)
						}
						return
					}
					eventCount++

					if event.Type == "" {
						t.Error("event type should not be empty")
					}

				case <-timeout:
					t.Fatal("test timeout waiting for events")
				}
			}
		})
	}
}

func TestParseSSELine(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		expectEvent bool
		expectType  string
	}{
		{
			name:        "parses message_start event",
			line:        `data: {"type":"message_start","message":{"id":"msg_123"}}`,
			expectEvent: true,
			expectType:  "message_start",
		},
		{
			name:        "parses content_block_delta event",
			line:        `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			expectEvent: true,
			expectType:  "content_block_delta",
		},
		{
			name:        "parses message_stop event",
			line:        `data: {"type":"message_stop"}`,
			expectEvent: true,
			expectType:  "message_stop",
		},
		{
			name:        "ignores empty lines",
			line:        "",
			expectEvent: false,
		},
		{
			name:        "ignores comment lines",
			line:        ": this is a comment",
			expectEvent: false,
		},
		{
			name:        "ignores event: lines without data",
			line:        "event: message_start",
			expectEvent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := parseSSELine(tt.line)

			if tt.expectEvent {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Type != tt.expectType {
					t.Errorf("expected type %q, got %q", tt.expectType, event.Type)
				}
			} else {
				if event != nil {
					t.Errorf("expected nil event, got %+v", event)
				}
			}
		})
	}
}

func TestStreamHandlesErrors(t *testing.T) {
	t.Run("handles connection errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return error status
			w.WriteHeader(http.StatusInternalServerError)
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
			Stream:    true,
		}

		_, err := client.CreateMessageStream(context.Background(), req)

		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)

			// Stream indefinitely
			for {
				select {
				case <-r.Context().Done():
					return
				default:
					w.Write([]byte("data: test\n\n"))
					flusher.Flush()
					time.Sleep(100 * time.Millisecond)
				}
			}
		}))
		defer server.Close()

		log := zerolog.Nop()
		client := NewClient("sk-ant-test-key", log)
		client.BaseURL = server.URL

		ctx, cancel := context.WithCancel(context.Background())

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
			Stream:    true,
		}

		eventChan, err := client.CreateMessageStream(ctx, req)
		if err != nil {
			t.Fatalf("CreateMessageStream failed: %v", err)
		}

		// Cancel after a short delay
		go func() {
			time.Sleep(200 * time.Millisecond)
			cancel()
		}()

		// Channel should close after cancellation
		timeout := time.After(1 * time.Second)
		for {
			select {
			case _, ok := <-eventChan:
				if !ok {
					// Success: channel closed
					return
				}
			case <-timeout:
				t.Fatal("channel did not close after context cancellation")
			}
		}
	})
}

func TestStreamEventTypes(t *testing.T) {
	eventTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
		"error",
	}

	for _, eventType := range eventTypes {
		t.Run("handles "+eventType+" event", func(t *testing.T) {
			sseData := "data: {\"type\":\"" + eventType + "\"}\n\n"

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(sseData))
			}))
			defer server.Close()

			log := zerolog.Nop()
			client := NewClient("sk-ant-test-key", log)
			client.BaseURL = server.URL

			req := &CreateMessageRequest{
				Model:     "claude-3-5-sonnet-20241022",
				Messages:  []Message{{Role: "user", Content: []Content{{Type: "text", Text: "test"}}}},
				MaxTokens: 1024,
				Stream:    true,
			}

			eventChan, err := client.CreateMessageStream(context.Background(), req)
			if err != nil {
				t.Fatalf("CreateMessageStream failed: %v", err)
			}

			timeout := time.After(1 * time.Second)
			select {
			case event := <-eventChan:
				if event.Type != eventType {
					t.Errorf("expected event type %q, got %q", eventType, event.Type)
				}
			case <-timeout:
				t.Fatal("timeout waiting for event")
			}
		})
	}
}
