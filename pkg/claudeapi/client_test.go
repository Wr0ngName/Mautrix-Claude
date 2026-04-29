package claudeapi

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/rs/zerolog"
)

func TestConvertSDKResponse(t *testing.T) {
	t.Run("text blocks", func(t *testing.T) {
		resp := &anthropic.Message{
			ID:         "msg_123",
			Type:       "message",
			Role:       "assistant",
			Model:      "claude-sonnet-4-20250514",
			StopReason: anthropic.StopReasonEndTurn,
			Content: []anthropic.ContentBlockUnion{
				{Type: "text", Text: "Hello, world!"},
			},
			Usage: anthropic.Usage{
				InputTokens:  100,
				OutputTokens: 50,
			},
		}

		result := convertSDKResponse(resp)

		if result.ID != "msg_123" {
			t.Errorf("expected ID 'msg_123', got %q", result.ID)
		}
		if result.Role != "assistant" {
			t.Errorf("expected role 'assistant', got %q", result.Role)
		}
		if result.StopReason != "end_turn" {
			t.Errorf("expected stop reason 'end_turn', got %q", result.StopReason)
		}
		if len(result.Content) != 1 {
			t.Fatalf("expected 1 content block, got %d", len(result.Content))
		}
		if result.Content[0].Type != "text" {
			t.Errorf("expected type 'text', got %q", result.Content[0].Type)
		}
		if result.Content[0].Text != "Hello, world!" {
			t.Errorf("expected text 'Hello, world!', got %q", result.Content[0].Text)
		}
		if result.Usage == nil {
			t.Fatal("expected usage to be set")
		}
		if result.Usage.InputTokens != 100 {
			t.Errorf("expected 100 input tokens, got %d", result.Usage.InputTokens)
		}
		if result.Usage.OutputTokens != 50 {
			t.Errorf("expected 50 output tokens, got %d", result.Usage.OutputTokens)
		}
	})

	t.Run("thinking blocks included as text", func(t *testing.T) {
		resp := &anthropic.Message{
			ID:   "msg_456",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlockUnion{
				{Type: "thinking", Thinking: "Let me reason about this..."},
				{Type: "text", Text: "Here's my answer."},
			},
			Usage: anthropic.Usage{},
		}

		result := convertSDKResponse(resp)

		if len(result.Content) != 2 {
			t.Fatalf("expected 2 content blocks (thinking + text), got %d", len(result.Content))
		}
		if result.Content[0].Text != "Let me reason about this..." {
			t.Errorf("expected thinking text, got %q", result.Content[0].Text)
		}
		if result.Content[1].Text != "Here's my answer." {
			t.Errorf("expected answer text, got %q", result.Content[1].Text)
		}
	})

	t.Run("empty thinking blocks skipped", func(t *testing.T) {
		resp := &anthropic.Message{
			ID:   "msg_789",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlockUnion{
				{Type: "thinking", Thinking: ""},
				{Type: "text", Text: "Answer."},
			},
			Usage: anthropic.Usage{},
		}

		result := convertSDKResponse(resp)

		if len(result.Content) != 1 {
			t.Fatalf("expected 1 content block (empty thinking skipped), got %d", len(result.Content))
		}
		if result.Content[0].Text != "Answer." {
			t.Errorf("expected 'Answer.', got %q", result.Content[0].Text)
		}
	})

	t.Run("redacted thinking blocks skipped", func(t *testing.T) {
		resp := &anthropic.Message{
			ID:   "msg_redacted",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlockUnion{
				{Type: "redacted_thinking"},
				{Type: "text", Text: "Final answer."},
			},
			Usage: anthropic.Usage{},
		}

		result := convertSDKResponse(resp)

		if len(result.Content) != 1 {
			t.Fatalf("expected 1 content block (redacted skipped), got %d", len(result.Content))
		}
	})

	t.Run("tool_use blocks skipped", func(t *testing.T) {
		resp := &anthropic.Message{
			ID:   "msg_tool",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlockUnion{
				{Type: "tool_use"},
				{Type: "text", Text: "With tools."},
			},
			Usage: anthropic.Usage{},
		}

		result := convertSDKResponse(resp)

		if len(result.Content) != 1 {
			t.Fatalf("expected 1 content block (tool_use skipped), got %d", len(result.Content))
		}
	})

	t.Run("cache token usage", func(t *testing.T) {
		resp := &anthropic.Message{
			ID:   "msg_cache",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlockUnion{
				{Type: "text", Text: "Cached response."},
			},
			Usage: anthropic.Usage{
				InputTokens:             100,
				OutputTokens:            50,
				CacheCreationInputTokens: 200,
				CacheReadInputTokens:     300,
			},
		}

		result := convertSDKResponse(resp)

		if result.Usage.CacheCreationTokens != 200 {
			t.Errorf("expected 200 cache creation tokens, got %d", result.Usage.CacheCreationTokens)
		}
		if result.Usage.CacheReadTokens != 300 {
			t.Errorf("expected 300 cache read tokens, got %d", result.Usage.CacheReadTokens)
		}
	})

	t.Run("empty content blocks", func(t *testing.T) {
		resp := &anthropic.Message{
			ID:      "msg_empty",
			Type:    "message",
			Role:    "assistant",
			Content: []anthropic.ContentBlockUnion{},
			Usage:   anthropic.Usage{},
		}

		result := convertSDKResponse(resp)

		if len(result.Content) != 0 {
			t.Errorf("expected 0 content blocks, got %d", len(result.Content))
		}
	})
}

func TestConvertSDKStreamEvent(t *testing.T) {
	t.Run("message_start", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "message_start",
			Message: anthropic.Message{
				ID:    "msg_start_1",
				Model: "claude-sonnet-4-20250514",
				Usage: anthropic.Usage{
					InputTokens: 150,
				},
			},
		}

		result := convertSDKStreamEvent(ev)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != "message_start" {
			t.Errorf("expected type 'message_start', got %q", result.Type)
		}
		if result.Message == nil {
			t.Fatal("expected message to be set")
		}
		if result.Message.ID != "msg_start_1" {
			t.Errorf("expected ID 'msg_start_1', got %q", result.Message.ID)
		}
		if result.Message.Model != "claude-sonnet-4-20250514" {
			t.Errorf("expected model, got %q", result.Message.Model)
		}
		if result.Message.Usage == nil {
			t.Fatal("expected usage to be set")
		}
		if result.Message.Usage.InputTokens != 150 {
			t.Errorf("expected 150 input tokens, got %d", result.Message.Usage.InputTokens)
		}
	})

	t.Run("message_start with empty ID gets fallback", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type:    "message_start",
			Message: anthropic.Message{},
		}

		result := convertSDKStreamEvent(ev)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Message.ID == "" {
			t.Error("expected fallback ID to be generated")
		}
	})

	t.Run("message_start with cache tokens", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "message_start",
			Message: anthropic.Message{
				ID: "msg_cache_start",
				Usage: anthropic.Usage{
					InputTokens:             100,
					CacheCreationInputTokens: 500,
					CacheReadInputTokens:     200,
				},
			},
		}

		result := convertSDKStreamEvent(ev)

		if result.Message.Usage.InputTokens != 100 {
			t.Errorf("expected 100 input tokens, got %d", result.Message.Usage.InputTokens)
		}
		if result.Message.Usage.CacheCreationTokens != 500 {
			t.Errorf("expected 500 cache creation tokens, got %d", result.Message.Usage.CacheCreationTokens)
		}
		if result.Message.Usage.CacheReadTokens != 200 {
			t.Errorf("expected 200 cache read tokens, got %d", result.Message.Usage.CacheReadTokens)
		}
	})

	t.Run("content_block_delta text_delta", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "content_block_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				Type: "text_delta",
				Text: "Hello",
			},
		}

		result := convertSDKStreamEvent(ev)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != "content_block_delta" {
			t.Errorf("expected type 'content_block_delta', got %q", result.Type)
		}
		if result.Delta == nil {
			t.Fatal("expected delta to be set")
		}
		if result.Delta.Type != "text_delta" {
			t.Errorf("expected delta type 'text_delta', got %q", result.Delta.Type)
		}
		if result.Delta.Text != "Hello" {
			t.Errorf("expected text 'Hello', got %q", result.Delta.Text)
		}
		if result.Delta.IsThinking {
			t.Error("text_delta should not be marked as thinking")
		}
	})

	t.Run("content_block_delta thinking_delta", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "content_block_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				Type:     "thinking_delta",
				Thinking: "Let me think...",
			},
		}

		result := convertSDKStreamEvent(ev)

		if result == nil {
			t.Fatal("expected non-nil result for thinking_delta")
		}
		if result.Type != "content_block_delta" {
			t.Errorf("expected type 'content_block_delta', got %q", result.Type)
		}
		if result.Delta == nil {
			t.Fatal("expected delta to be set")
		}
		if result.Delta.Type != "thinking_delta" {
			t.Errorf("expected delta type 'thinking_delta', got %q", result.Delta.Type)
		}
		if result.Delta.Text != "Let me think..." {
			t.Errorf("expected thinking text, got %q", result.Delta.Text)
		}
		if !result.Delta.IsThinking {
			t.Error("thinking_delta should be marked as thinking")
		}
	})

	t.Run("content_block_delta empty text_delta returns nil", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "content_block_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				Type: "text_delta",
				Text: "",
			},
		}

		result := convertSDKStreamEvent(ev)
		if result != nil {
			t.Error("expected nil for empty text_delta")
		}
	})

	t.Run("content_block_delta empty thinking_delta returns nil", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "content_block_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				Type:     "thinking_delta",
				Thinking: "",
			},
		}

		result := convertSDKStreamEvent(ev)
		if result != nil {
			t.Error("expected nil for empty thinking_delta")
		}
	})

	t.Run("message_delta with stop reason", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "message_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				StopReason: anthropic.StopReasonEndTurn,
			},
			Usage: anthropic.MessageDeltaUsage{
				OutputTokens: 250,
			},
		}

		result := convertSDKStreamEvent(ev)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != "message_delta" {
			t.Errorf("expected type 'message_delta', got %q", result.Type)
		}
		if result.StopReason != "end_turn" {
			t.Errorf("expected stop reason 'end_turn', got %q", result.StopReason)
		}
		if result.Usage == nil {
			t.Fatal("expected usage to be set")
		}
		if result.Usage.OutputTokens != 250 {
			t.Errorf("expected 250 output tokens, got %d", result.Usage.OutputTokens)
		}
	})

	t.Run("message_delta with refusal", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "message_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				StopReason: anthropic.StopReason("refusal"),
			},
			Usage: anthropic.MessageDeltaUsage{},
		}

		result := convertSDKStreamEvent(ev)

		if result.StopReason != "refusal" {
			t.Errorf("expected stop reason 'refusal', got %q", result.StopReason)
		}
	})

	t.Run("message_delta with cache tokens", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "message_delta",
			Usage: anthropic.MessageDeltaUsage{
				OutputTokens:             300,
				InputTokens:              100,
				CacheCreationInputTokens: 50,
				CacheReadInputTokens:     75,
			},
		}

		result := convertSDKStreamEvent(ev)

		if result.Usage.OutputTokens != 300 {
			t.Errorf("expected 300 output tokens, got %d", result.Usage.OutputTokens)
		}
		if result.Usage.InputTokens != 100 {
			t.Errorf("expected 100 input tokens, got %d", result.Usage.InputTokens)
		}
		if result.Usage.CacheCreationTokens != 50 {
			t.Errorf("expected 50 cache creation tokens, got %d", result.Usage.CacheCreationTokens)
		}
		if result.Usage.CacheReadTokens != 75 {
			t.Errorf("expected 75 cache read tokens, got %d", result.Usage.CacheReadTokens)
		}
	})

	t.Run("message_stop", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{
			Type: "message_stop",
		}

		result := convertSDKStreamEvent(ev)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Type != "message_stop" {
			t.Errorf("expected type 'message_stop', got %q", result.Type)
		}
	})

	t.Run("content_block_start returns nil", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{Type: "content_block_start"}
		if convertSDKStreamEvent(ev) != nil {
			t.Error("content_block_start should return nil")
		}
	})

	t.Run("content_block_stop returns nil", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{Type: "content_block_stop"}
		if convertSDKStreamEvent(ev) != nil {
			t.Error("content_block_stop should return nil")
		}
	})

	t.Run("error event", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{Type: "error"}

		result := convertSDKStreamEvent(ev)

		if result == nil {
			t.Fatal("expected non-nil result for error event")
		}
		if result.Type != "error" {
			t.Errorf("expected type 'error', got %q", result.Type)
		}
		if result.Error == nil {
			t.Fatal("expected error to be set")
		}
		if result.Error.Type != "api_error" {
			t.Errorf("expected error type 'api_error', got %q", result.Error.Type)
		}
	})

	t.Run("unknown event type returns nil", func(t *testing.T) {
		ev := anthropic.MessageStreamEventUnion{Type: "some_new_event_type"}
		if convertSDKStreamEvent(ev) != nil {
			t.Error("unknown event type should return nil")
		}
	})
}

func TestConvertMessagesToSDK(t *testing.T) {
	t.Run("basic user and assistant messages", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: []Content{{Type: "text", Text: "Hello"}}},
			{Role: "assistant", Content: []Content{{Type: "text", Text: "Hi there!"}}},
		}

		result := convertMessagesToSDK(messages)

		if len(result) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(result))
		}
		if result[0].Role != "user" {
			t.Errorf("expected role 'user', got %q", result[0].Role)
		}
		if result[1].Role != "assistant" {
			t.Errorf("expected role 'assistant', got %q", result[1].Role)
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		result := convertMessagesToSDK([]Message{})
		if len(result) != 0 {
			t.Errorf("expected 0 messages, got %d", len(result))
		}
	})

	t.Run("image content", func(t *testing.T) {
		messages := []Message{
			{
				Role: "user",
				Content: []Content{
					{
						Type: "image",
						Source: &ImageSource{
							Type:      "base64",
							MediaType: "image/png",
							Data:      "iVBORw0KGgo=",
						},
					},
					{Type: "text", Text: "What's in this image?"},
				},
			},
		}

		result := convertMessagesToSDK(messages)

		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
	})

	t.Run("image without source is skipped", func(t *testing.T) {
		messages := []Message{
			{
				Role: "user",
				Content: []Content{
					{Type: "image", Source: nil},
					{Type: "text", Text: "Hello"},
				},
			},
		}

		result := convertMessagesToSDK(messages)
		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
	})
}

func TestConvertMessagesToSDKWithCache(t *testing.T) {
	t.Run("caching disabled - no cache control", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: []Content{{Type: "text", Text: "First"}}},
			{Role: "assistant", Content: []Content{{Type: "text", Text: "Response"}}},
			{Role: "user", Content: []Content{{Type: "text", Text: "Second"}}},
		}

		result := convertMessagesToSDKWithCache(messages, false)

		if len(result) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(result))
		}
		for i, msg := range result {
			for _, block := range msg.Content {
				if block.OfText != nil && block.OfText.CacheControl.Type != "" {
					t.Errorf("message %d: expected no cache control when caching disabled", i)
				}
			}
		}
	})

	t.Run("caching enabled - last message not cached", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: []Content{{Type: "text", Text: "First"}}},
			{Role: "assistant", Content: []Content{{Type: "text", Text: "Response"}}},
			{Role: "user", Content: []Content{{Type: "text", Text: "Second"}}},
		}

		result := convertMessagesToSDKWithCache(messages, true)

		if len(result) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(result))
		}

		// First two messages should have cache control
		for i := 0; i < 2; i++ {
			for _, block := range result[i].Content {
				if block.OfText != nil && block.OfText.CacheControl.Type != "ephemeral" {
					t.Errorf("message %d: expected cache control 'ephemeral', got %q", i, block.OfText.CacheControl.Type)
				}
			}
		}

		// Last message should NOT have cache control
		lastMsg := result[2]
		for _, block := range lastMsg.Content {
			if block.OfText != nil && block.OfText.CacheControl.Type != "" {
				t.Error("last message should not have cache control")
			}
		}
	})

	t.Run("single message with caching - not cached", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: []Content{{Type: "text", Text: "Only message"}}},
		}

		result := convertMessagesToSDKWithCache(messages, true)

		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
		for _, block := range result[0].Content {
			if block.OfText != nil && block.OfText.CacheControl.Type != "" {
				t.Error("single (last) message should not have cache control")
			}
		}
	})
}

func TestStreamEventThinkingFlow(t *testing.T) {
	events := []anthropic.MessageStreamEventUnion{
		{
			Type: "message_start",
			Message: anthropic.Message{
				ID:    "msg_thinking_flow",
				Model: "claude-sonnet-4-20250514",
				Usage: anthropic.Usage{InputTokens: 100},
			},
		},
		{Type: "content_block_start"},
		{
			Type: "content_block_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				Type:     "thinking_delta",
				Thinking: "Step 1: analyze the question. ",
			},
		},
		{
			Type: "content_block_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				Type:     "thinking_delta",
				Thinking: "Step 2: form a response.",
			},
		},
		{Type: "content_block_stop"},
		{Type: "content_block_start"},
		{
			Type: "content_block_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				Type: "text_delta",
				Text: "Here is my answer.",
			},
		},
		{Type: "content_block_stop"},
		{
			Type: "message_delta",
			Delta: anthropic.MessageStreamEventUnionDelta{
				StopReason: anthropic.StopReasonEndTurn,
			},
			Usage: anthropic.MessageDeltaUsage{
				OutputTokens: 200,
			},
		},
		{Type: "message_stop"},
	}

	var thinkingText, responseText string
	var stopReason string
	var outputTokens int

	for _, ev := range events {
		result := convertSDKStreamEvent(ev)
		if result == nil {
			continue
		}

		switch result.Type {
		case "content_block_delta":
			if result.Delta.IsThinking {
				thinkingText += result.Delta.Text
			} else {
				responseText += result.Delta.Text
			}
		case "message_delta":
			stopReason = result.StopReason
			if result.Usage != nil {
				outputTokens = result.Usage.OutputTokens
			}
		}
	}

	if thinkingText != "Step 1: analyze the question. Step 2: form a response." {
		t.Errorf("unexpected thinking text: %q", thinkingText)
	}
	if responseText != "Here is my answer." {
		t.Errorf("unexpected response text: %q", responseText)
	}
	if stopReason != "end_turn" {
		t.Errorf("expected stop reason 'end_turn', got %q", stopReason)
	}
	if outputTokens != 200 {
		t.Errorf("expected 200 output tokens, got %d", outputTokens)
	}
}

func TestNewClient(t *testing.T) {
	client := NewClient("test-key", zerolog.Nop())

	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.sdk == nil {
		t.Error("expected SDK client to be initialized")
	}
	if client.Metrics == nil {
		t.Error("expected metrics to be initialized")
	}
	if client.GetClientType() != ClientTypeAPI {
		t.Errorf("expected client type %q, got %q", ClientTypeAPI, client.GetClientType())
	}
}

func TestClientImplementsInterface(t *testing.T) {
	var _ MessageClient = (*Client)(nil)
}
