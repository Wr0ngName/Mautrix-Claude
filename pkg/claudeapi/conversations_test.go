package claudeapi

import (
	"strings"
	"testing"
	"time"
)

func TestConversationManagerBasic(t *testing.T) {
	cm := NewConversationManager(200000)

	if cm.HasMessages() {
		t.Error("new conversation should have no messages")
	}
	if cm.MessageCount() != 0 {
		t.Errorf("expected 0 messages, got %d", cm.MessageCount())
	}

	cm.AddMessage("user", "Hello")
	cm.AddMessage("assistant", "Hi there!")

	if !cm.HasMessages() {
		t.Error("should have messages after adding")
	}
	if cm.MessageCount() != 2 {
		t.Errorf("expected 2 messages, got %d", cm.MessageCount())
	}

	msgs := cm.GetMessages()
	if msgs[0].Role != "user" {
		t.Errorf("expected 'user', got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("expected 'assistant', got %q", msgs[1].Role)
	}
}

func TestConversationManagerAddMessageWithID(t *testing.T) {
	cm := NewConversationManager(200000)

	cm.AddMessageWithID("user", "Hello", "msg_001")
	cm.AddMessageWithID("assistant", "Hi", "msg_002")

	if cm.MessageCount() != 2 {
		t.Errorf("expected 2 messages, got %d", cm.MessageCount())
	}
}

func TestConversationManagerAddMessageWithContent(t *testing.T) {
	cm := NewConversationManager(200000)

	content := []Content{
		{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png", Data: "abc"}},
		{Type: "text", Text: "What's in this image?"},
	}
	cm.AddMessageWithContent("user", content, "msg_img")

	msgs := cm.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Content) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(msgs[0].Content))
	}
}

func TestConversationManagerGetMessagesReturnsCopy(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Hello")
	cm.AddMessage("assistant", "Hi")

	msgs := cm.GetMessages()
	// Modifying the slice itself (appending) should not affect internal state
	msgs = append(msgs, Message{Role: "user", Content: []Content{{Type: "text", Text: "Extra"}}})

	original := cm.GetMessages()
	if len(original) != 2 {
		t.Errorf("expected 2 messages (append to copy should not affect original), got %d", len(original))
	}
}

func TestConversationManagerEditByID(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessageWithID("user", "Hello", "msg_001")
	cm.AddMessageWithID("assistant", "Hi there!", "msg_002")
	cm.AddMessageWithID("user", "Follow up", "msg_003")
	cm.AddMessageWithID("assistant", "Sure", "msg_004")

	found := cm.EditMessageByID("msg_001", "Updated hello")
	if !found {
		t.Error("expected message to be found")
	}

	if cm.MessageCount() != 1 {
		t.Errorf("expected 1 message after edit (subsequent truncated), got %d", cm.MessageCount())
	}

	msgs := cm.GetMessages()
	if msgs[0].Content[0].Text != "Updated hello" {
		t.Errorf("expected 'Updated hello', got %q", msgs[0].Content[0].Text)
	}
}

func TestConversationManagerEditByIDNotFound(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessageWithID("user", "Hello", "msg_001")

	found := cm.EditMessageByID("nonexistent", "Updated")
	if found {
		t.Error("expected not found")
	}
	if cm.MessageCount() != 1 {
		t.Error("message count should be unchanged")
	}
}

func TestConversationManagerDeleteByID(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessageWithID("user", "Hello", "msg_001")
	cm.AddMessageWithID("assistant", "Hi", "msg_002")
	cm.AddMessageWithID("user", "More", "msg_003")

	found := cm.DeleteMessageByID("msg_002")
	if !found {
		t.Error("expected message to be found")
	}

	if cm.MessageCount() != 1 {
		t.Errorf("expected 1 message remaining, got %d", cm.MessageCount())
	}
}

func TestConversationManagerDeleteByIDNotFound(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Hello")

	found := cm.DeleteMessageByID("nonexistent")
	if found {
		t.Error("expected not found")
	}
}

func TestConversationManagerEditLastUserMessage(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Original")
	cm.AddMessage("assistant", "Response")

	err := cm.EditLastUserMessage("Edited")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cm.MessageCount() != 1 {
		t.Errorf("expected 1 message (assistant removed), got %d", cm.MessageCount())
	}

	msgs := cm.GetMessages()
	if msgs[0].Content[0].Text != "Edited" {
		t.Errorf("expected 'Edited', got %q", msgs[0].Content[0].Text)
	}
}

func TestConversationManagerEditLastUserMessageNoUser(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("assistant", "Only assistant")

	err := cm.EditLastUserMessage("New")
	if err == nil {
		t.Error("expected error when no user message exists")
	}
}

func TestConversationManagerDeleteLastUserMessage(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Q1")
	cm.AddMessage("assistant", "A1")
	cm.AddMessage("user", "Q2")
	cm.AddMessage("assistant", "A2")

	err := cm.DeleteLastUserMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cm.MessageCount() != 2 {
		t.Errorf("expected 2 messages remaining, got %d", cm.MessageCount())
	}
}

func TestConversationManagerDeleteLastUserMessageNoUser(t *testing.T) {
	cm := NewConversationManager(200000)
	err := cm.DeleteLastUserMessage()
	if err == nil {
		t.Error("expected error when no user message exists")
	}
}

func TestConversationManagerClear(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Hello")
	cm.AddMessage("assistant", "Hi")

	cm.Clear()

	if cm.HasMessages() {
		t.Error("should have no messages after clear")
	}
	if cm.MessageCount() != 0 {
		t.Errorf("expected 0, got %d", cm.MessageCount())
	}
}

func TestConversationManagerLastMessageRole(t *testing.T) {
	cm := NewConversationManager(200000)

	if got := cm.LastMessageRole(); got != "" {
		t.Errorf("expected empty string for no messages, got %q", got)
	}

	cm.AddMessage("user", "Hello")
	if got := cm.LastMessageRole(); got != "user" {
		t.Errorf("expected 'user', got %q", got)
	}

	cm.AddMessage("assistant", "Hi")
	if got := cm.LastMessageRole(); got != "assistant" {
		t.Errorf("expected 'assistant', got %q", got)
	}
}

func TestConversationManagerTrimToTokenLimit(t *testing.T) {
	t.Run("under limit no trim", func(t *testing.T) {
		cm := NewConversationManager(200000)
		cm.AddMessage("user", "short")
		cm.AddMessage("assistant", "short")

		err := cm.TrimToTokenLimit()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cm.MessageCount() != 2 {
			t.Errorf("expected 2 messages (no trim needed), got %d", cm.MessageCount())
		}
	})

	t.Run("over limit trims old messages", func(t *testing.T) {
		cm := NewConversationManager(10) // Very low limit: 10 tokens = 40 chars
		// Add messages that exceed the limit
		cm.AddMessage("user", strings.Repeat("a", 100))     // 25 tokens
		cm.AddMessage("assistant", strings.Repeat("b", 100)) // 25 tokens
		cm.AddMessage("user", strings.Repeat("c", 20))       // 5 tokens
		cm.AddMessage("assistant", strings.Repeat("d", 20))  // 5 tokens

		err := cm.TrimToTokenLimit()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should have trimmed old messages but kept at least MinMessagesToKeep
		if cm.MessageCount() < MinMessagesToKeep {
			t.Errorf("should keep at least %d messages, got %d", MinMessagesToKeep, cm.MessageCount())
		}
	})

	t.Run("zero max tokens no limit", func(t *testing.T) {
		cm := NewConversationManager(0)
		cm.AddMessage("user", strings.Repeat("a", 10000))

		err := cm.TrimToTokenLimit()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cm.MessageCount() != 1 {
			t.Error("zero max tokens should mean no trimming")
		}
	})
}

func TestConversationManagerEstimatedTokens(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", strings.Repeat("a", 400)) // 400 chars / 4 = 100 tokens

	got := cm.EstimatedTokens()
	if got != 100 {
		t.Errorf("expected 100 estimated tokens, got %d", got)
	}
}

func TestConversationManagerTimestamps(t *testing.T) {
	cm := NewConversationManager(200000)

	if cm.CreatedAt().IsZero() {
		t.Error("created at should be set")
	}
	if cm.Age() < 0 {
		t.Error("age should be non-negative")
	}

	initialLastUsed := cm.LastUsedAt()
	time.Sleep(1 * time.Millisecond)
	cm.AddMessage("user", "Hello")

	if !cm.LastUsedAt().After(initialLastUsed) {
		t.Error("last used should update after adding message")
	}
}

func TestConversationManagerIsExpired(t *testing.T) {
	cm := NewConversationManager(200000)

	if cm.IsExpired(0) {
		t.Error("zero max age should never expire")
	}

	if cm.IsExpired(1 * time.Hour) {
		t.Error("should not be expired immediately")
	}

	// Use a very short idle time check
	cm = NewConversationManager(200000)
	time.Sleep(5 * time.Millisecond)
	if !cm.IsExpired(1 * time.Millisecond) {
		t.Error("should be expired after exceeding idle time")
	}
}

func TestConversationManagerMaxTokens(t *testing.T) {
	cm := NewConversationManager(200000)

	if got := cm.GetMaxTokens(); got != 200000 {
		t.Errorf("expected 200000, got %d", got)
	}

	cm.SetMaxTokens(100000)
	if got := cm.GetMaxTokens(); got != 100000 {
		t.Errorf("expected 100000 after set, got %d", got)
	}
}

func TestConversationManagerNeedsCompaction(t *testing.T) {
	t.Run("below threshold", func(t *testing.T) {
		cm := NewConversationManager(1000) // 1000 tokens = 4000 chars
		cm.AddMessage("user", strings.Repeat("a", 100)) // 25 tokens = 2.5%

		if cm.NeedsCompaction() {
			t.Error("should not need compaction below threshold")
		}
	})

	t.Run("above threshold", func(t *testing.T) {
		cm := NewConversationManager(100) // 100 tokens = 400 chars, threshold at 75 tokens = 300 chars
		cm.AddMessage("user", strings.Repeat("a", 400)) // 100 tokens = 100%

		if !cm.NeedsCompaction() {
			t.Error("should need compaction above threshold")
		}
	})

	t.Run("zero max tokens never needs compaction", func(t *testing.T) {
		cm := NewConversationManager(0)
		cm.AddMessage("user", strings.Repeat("a", 100000))

		if cm.NeedsCompaction() {
			t.Error("zero max tokens should never need compaction")
		}
	})
}

func TestConversationManagerApplyCompaction(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessageWithID("user", "Q1", "msg_1")
	cm.AddMessageWithID("assistant", "A1", "msg_2")
	cm.AddMessageWithID("user", "Q2", "msg_3")
	cm.AddMessageWithID("assistant", "A2", "msg_4")

	cm.ApplyCompaction("This is a summary of the conversation.", 2)

	// Should have: summary user + summary assistant + 2 recent messages
	if cm.MessageCount() != 4 {
		t.Errorf("expected 4 messages after compaction, got %d", cm.MessageCount())
	}

	if !cm.IsCompacted() {
		t.Error("should be marked as compacted")
	}

	if cm.CompactionCount() != 1 {
		t.Errorf("expected compaction count 1, got %d", cm.CompactionCount())
	}

	msgs := cm.GetMessages()
	if !strings.Contains(msgs[0].Content[0].Text, "CONVERSATION SUMMARY") {
		t.Error("first message should contain summary marker")
	}
	if msgs[0].Role != "user" {
		t.Errorf("summary should be user role, got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("ack should be assistant role, got %q", msgs[1].Role)
	}
}

func TestConversationManagerApplyCompactionKeepAll(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Only message")

	cm.ApplyCompaction("Summary", 10) // keepRecentCount > total messages

	// Should keep: summary pair + the 1 original message
	if cm.MessageCount() != 3 {
		t.Errorf("expected 3 messages, got %d", cm.MessageCount())
	}
}

func TestConversationManagerApplyCompactionKeepZero(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Q1")
	cm.AddMessage("assistant", "A1")

	cm.ApplyCompaction("Summary", 0)

	// Should only have the summary pair
	if cm.MessageCount() != 2 {
		t.Errorf("expected 2 messages (summary pair only), got %d", cm.MessageCount())
	}
}

func TestConversationManagerGetMessagesForCompaction(t *testing.T) {
	cm := NewConversationManager(200000)
	cm.AddMessage("user", "Hello Claude")
	cm.AddMessage("assistant", "Hello! How can I help?")

	text := cm.GetMessagesForCompaction()

	if !strings.Contains(text, "[User]: Hello Claude") {
		t.Error("should contain user message with User label")
	}
	if !strings.Contains(text, "[Assistant]: Hello! How can I help?") {
		t.Error("should contain assistant message with Assistant label")
	}
}

func TestConversationManagerShouldEnableCaching(t *testing.T) {
	t.Run("too few messages", func(t *testing.T) {
		cm := NewConversationManager(200000)
		cm.RecordFirstMessage()
		cm.AddMessage("user", strings.Repeat("a", 5000))

		if cm.ShouldEnableCaching() {
			t.Error("should not cache with < 2 messages")
		}
	})

	t.Run("enough messages within window and tokens", func(t *testing.T) {
		cm := NewConversationManager(200000)
		cm.RecordFirstMessage()
		cm.AddMessage("user", strings.Repeat("a", 5000))  // 1250 tokens
		cm.AddMessage("assistant", strings.Repeat("b", 5000)) // 1250 tokens

		if !cm.ShouldEnableCaching() {
			t.Error("should enable caching with enough messages and tokens")
		}
	})

	t.Run("below minimum token threshold", func(t *testing.T) {
		cm := NewConversationManager(200000)
		cm.RecordFirstMessage()
		cm.AddMessage("user", "short")
		cm.AddMessage("assistant", "short")

		if cm.ShouldEnableCaching() {
			t.Error("should not cache below minimum token threshold")
		}
	})

	t.Run("no first message recorded", func(t *testing.T) {
		cm := NewConversationManager(200000)
		cm.AddMessage("user", strings.Repeat("a", 5000))
		cm.AddMessage("assistant", strings.Repeat("b", 5000))

		if cm.ShouldEnableCaching() {
			t.Error("should not cache without first message timestamp")
		}
	})
}

func TestConversationManagerRecordFirstMessage(t *testing.T) {
	cm := NewConversationManager(200000)

	cm.RecordFirstMessage()
	first := cm.firstMessageAt

	time.Sleep(1 * time.Millisecond)
	cm.RecordFirstMessage() // Should not overwrite

	if cm.firstMessageAt != first {
		t.Error("RecordFirstMessage should not overwrite existing timestamp")
	}
}

func TestConversationManagerResetCachingWindow(t *testing.T) {
	cm := NewConversationManager(200000)

	cm.RecordFirstMessage()
	original := cm.firstMessageAt

	time.Sleep(1 * time.Millisecond)
	cm.ResetCachingWindow()

	if !cm.firstMessageAt.After(original) {
		t.Error("ResetCachingWindow should update the timestamp")
	}
}
