// Package claudeapi provides tests for conversation management.
package claudeapi

import (
	"testing"
)

func TestNewConversationManager(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens int
	}{
		{
			name:      "creates manager with default max tokens",
			maxTokens: 100000,
		},
		{
			name:      "creates manager with custom max tokens",
			maxTokens: 50000,
		},
		{
			name:      "creates manager with zero max tokens",
			maxTokens: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewConversationManager(tt.maxTokens)

			if cm == nil {
				t.Fatal("NewConversationManager returned nil")
			}

			if cm.maxTokens != tt.maxTokens {
				t.Errorf("expected maxTokens %d, got %d", tt.maxTokens, cm.maxTokens)
			}

			if cm.messages == nil {
				t.Error("messages slice should be initialized")
			}

			if len(cm.messages) != 0 {
				t.Errorf("expected empty messages, got %d messages", len(cm.messages))
			}
		})
	}
}

func TestAddMessage(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		content  string
		expected int
	}{
		{
			name:     "adds user message",
			role:     "user",
			content:  "Hello, Claude!",
			expected: 1,
		},
		{
			name:     "adds assistant message",
			role:     "assistant",
			content:  "Hello! How can I help you?",
			expected: 1,
		},
		{
			name:     "adds empty message",
			role:     "user",
			content:  "",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewConversationManager(100000)

			cm.AddMessage(tt.role, tt.content)

			messages := cm.GetMessages()
			if len(messages) != tt.expected {
				t.Errorf("expected %d messages, got %d", tt.expected, len(messages))
			}

			if len(messages) > 0 {
				msg := messages[0]
				if msg.Role != tt.role {
					t.Errorf("expected role %q, got %q", tt.role, msg.Role)
				}

				if len(msg.Content) == 0 {
					t.Fatal("message content should not be empty")
				}

				if msg.Content[0].Text != tt.content {
					t.Errorf("expected content %q, got %q", tt.content, msg.Content[0].Text)
				}
			}
		})
	}
}

func TestGetMessages(t *testing.T) {
	t.Run("returns messages in order", func(t *testing.T) {
		cm := NewConversationManager(100000)

		cm.AddMessage("user", "First message")
		cm.AddMessage("assistant", "Second message")
		cm.AddMessage("user", "Third message")

		messages := cm.GetMessages()

		if len(messages) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(messages))
		}

		if messages[0].Content[0].Text != "First message" {
			t.Error("first message order incorrect")
		}
		if messages[1].Content[0].Text != "Second message" {
			t.Error("second message order incorrect")
		}
		if messages[2].Content[0].Text != "Third message" {
			t.Error("third message order incorrect")
		}
	})

	t.Run("returns empty slice for new manager", func(t *testing.T) {
		cm := NewConversationManager(100000)

		messages := cm.GetMessages()

		if messages == nil {
			t.Error("GetMessages should return empty slice, not nil")
		}

		if len(messages) != 0 {
			t.Errorf("expected 0 messages, got %d", len(messages))
		}
	})
}

func TestClear(t *testing.T) {
	t.Run("clears all messages", func(t *testing.T) {
		cm := NewConversationManager(100000)

		cm.AddMessage("user", "Message 1")
		cm.AddMessage("assistant", "Message 2")
		cm.AddMessage("user", "Message 3")

		messages := cm.GetMessages()
		if len(messages) != 3 {
			t.Fatalf("setup failed: expected 3 messages, got %d", len(messages))
		}

		cm.Clear()

		messages = cm.GetMessages()
		if len(messages) != 0 {
			t.Errorf("expected 0 messages after clear, got %d", len(messages))
		}
	})

	t.Run("can add messages after clear", func(t *testing.T) {
		cm := NewConversationManager(100000)

		cm.AddMessage("user", "First")
		cm.Clear()
		cm.AddMessage("user", "After clear")

		messages := cm.GetMessages()
		if len(messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(messages))
		}

		if messages[0].Content[0].Text != "After clear" {
			t.Error("message content incorrect after clear")
		}
	})
}

func TestMessageAlternation(t *testing.T) {
	t.Run("enforces user-assistant alternation", func(t *testing.T) {
		cm := NewConversationManager(100000)

		// Should allow user message first
		cm.AddMessage("user", "Hello")

		// Should allow assistant after user
		cm.AddMessage("assistant", "Hi")

		// Should allow user after assistant
		cm.AddMessage("user", "How are you?")

		messages := cm.GetMessages()
		if len(messages) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(messages))
		}

		// Verify alternation
		if messages[0].Role != "user" {
			t.Error("first message should be user")
		}
		if messages[1].Role != "assistant" {
			t.Error("second message should be assistant")
		}
		if messages[2].Role != "user" {
			t.Error("third message should be user")
		}
	})

	t.Run("handles consecutive same-role messages", func(t *testing.T) {
		cm := NewConversationManager(100000)

		cm.AddMessage("user", "First")
		cm.AddMessage("user", "Second consecutive user")

		// Implementation should handle this (merge or reject)
		// This test documents expected behavior
		messages := cm.GetMessages()

		// Should either merge or keep separate based on implementation
		if len(messages) == 0 {
			t.Error("should have at least one message")
		}
	})
}

func TestConcurrentAccess(t *testing.T) {
	t.Run("handles concurrent reads and writes", func(t *testing.T) {
		cm := NewConversationManager(100000)

		done := make(chan bool)

		// Concurrent writes
		go func() {
			for i := 0; i < 10; i++ {
				cm.AddMessage("user", "Concurrent message")
			}
			done <- true
		}()

		// Concurrent reads
		go func() {
			for i := 0; i < 10; i++ {
				cm.GetMessages()
			}
			done <- true
		}()

		<-done
		<-done

		// Should not panic or race
	})
}

func TestTrimToTokenLimit(t *testing.T) {
	tests := []struct {
		name           string
		maxTokens      int
		messagesToAdd  int
		expectTrimming bool
	}{
		{
			name:           "does not trim when under limit",
			maxTokens:      100000,
			messagesToAdd:  5,
			expectTrimming: false,
		},
		{
			name:           "trims when over limit",
			maxTokens:      1000,
			messagesToAdd:  100,
			expectTrimming: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := NewConversationManager(tt.maxTokens)

			// Add messages
			for i := 0; i < tt.messagesToAdd; i++ {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				cm.AddMessage(role, "This is a test message to fill up tokens")
			}

			initialCount := len(cm.GetMessages())

			err := cm.TrimToTokenLimit()
			if err != nil {
				t.Errorf("TrimToTokenLimit failed: %v", err)
			}

			finalCount := len(cm.GetMessages())

			if tt.expectTrimming {
				if finalCount >= initialCount {
					t.Error("expected trimming to reduce message count")
				}
			} else {
				if finalCount != initialCount {
					t.Error("should not trim when under limit")
				}
			}
		})
	}
}

func TestMessageContent(t *testing.T) {
	t.Run("supports text content", func(t *testing.T) {
		cm := NewConversationManager(100000)

		cm.AddMessage("user", "Plain text message")

		messages := cm.GetMessages()
		if len(messages) == 0 {
			t.Fatal("no messages added")
		}

		content := messages[0].Content
		if len(content) == 0 {
			t.Fatal("message has no content")
		}

		if content[0].Type != "text" {
			t.Errorf("expected type 'text', got %q", content[0].Type)
		}

		if content[0].Text != "Plain text message" {
			t.Errorf("text content mismatch")
		}
	})
}
