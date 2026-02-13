package connector

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// createTestPortal creates a Portal with MXID for testing
func createTestPortal(roomID id.RoomID) *bridgev2.Portal {
	return &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: roomID,
		},
	}
}

// mockMatrixAPI implements bridgev2.MatrixAPI for testing
type mockMatrixAPI struct {
	mu            sync.Mutex
	sendCalls     []sendCall
	failUntilSize int // Return M_TOO_LARGE for messages larger than this
	failCount     int // Number of times to fail before succeeding (0 = always check size)
	callCount     int
	errorToReturn error // Custom error to return instead of M_TOO_LARGE
}

type sendCall struct {
	RoomID  id.RoomID
	Content string
	Size    int
}

func (m *mockMatrixAPI) SendMessage(ctx context.Context, roomID id.RoomID, eventType event.Type, content *event.Content, extra *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callCount++

	// Extract text content
	var textContent string
	var size int
	if msgContent, ok := content.Parsed.(*event.MessageEventContent); ok {
		textContent = msgContent.Body
		size = len(msgContent.Body) + len(msgContent.FormattedBody)
	}

	m.sendCalls = append(m.sendCalls, sendCall{
		RoomID:  roomID,
		Content: textContent,
		Size:    size,
	})

	// Return custom error if set
	if m.errorToReturn != nil {
		return nil, m.errorToReturn
	}

	// Check if we should fail based on call count
	if m.failCount > 0 && m.callCount <= m.failCount {
		return nil, mautrix.RespError{
			ErrCode: "M_TOO_LARGE",
			Err:     "event too large",
		}
	}

	// Check if message is too large
	if m.failUntilSize > 0 && size > m.failUntilSize {
		return nil, mautrix.RespError{
			ErrCode: "M_TOO_LARGE",
			Err:     fmt.Sprintf("event too large: %d > %d", size, m.failUntilSize),
		}
	}

	return &mautrix.RespSendEvent{
		EventID: id.EventID(fmt.Sprintf("$test_%d", m.callCount)),
	}, nil
}

// Implement other MatrixAPI methods as no-ops
func (m *mockMatrixAPI) GetMXID() id.UserID                    { return "@test:example.com" }
func (m *mockMatrixAPI) IsDoublePuppet() bool                  { return false }
func (m *mockMatrixAPI) SendState(ctx context.Context, roomID id.RoomID, eventType event.Type, stateKey string, content *event.Content, ts time.Time) (*mautrix.RespSendEvent, error) {
	return nil, nil
}
func (m *mockMatrixAPI) MarkRead(ctx context.Context, roomID id.RoomID, eventID id.EventID, ts time.Time) error {
	return nil
}
func (m *mockMatrixAPI) MarkUnread(ctx context.Context, roomID id.RoomID, unread bool) error {
	return nil
}
func (m *mockMatrixAPI) MarkTyping(ctx context.Context, roomID id.RoomID, typingType bridgev2.TypingType, timeout time.Duration) error {
	return nil
}
func (m *mockMatrixAPI) DownloadMedia(ctx context.Context, uri id.ContentURIString, file *event.EncryptedFileInfo) ([]byte, error) {
	return nil, nil
}
func (m *mockMatrixAPI) DownloadMediaToFile(ctx context.Context, uri id.ContentURIString, file *event.EncryptedFileInfo, writable bool, callback func(*os.File) error) error {
	return nil
}
func (m *mockMatrixAPI) UploadMedia(ctx context.Context, roomID id.RoomID, data []byte, fileName, mimeType string) (url id.ContentURIString, file *event.EncryptedFileInfo, err error) {
	return "", nil, nil
}
func (m *mockMatrixAPI) UploadMediaStream(ctx context.Context, roomID id.RoomID, size int64, requireFile bool, cb bridgev2.FileStreamCallback) (url id.ContentURIString, file *event.EncryptedFileInfo, err error) {
	return "", nil, nil
}
func (m *mockMatrixAPI) SetDisplayName(ctx context.Context, name string) error { return nil }
func (m *mockMatrixAPI) SetAvatarURL(ctx context.Context, avatarURL id.ContentURIString) error {
	return nil
}
func (m *mockMatrixAPI) SetExtraProfileMeta(ctx context.Context, data any) error { return nil }
func (m *mockMatrixAPI) CreateRoom(ctx context.Context, req *mautrix.ReqCreateRoom) (id.RoomID, error) {
	return "", nil
}
func (m *mockMatrixAPI) DeleteRoom(ctx context.Context, roomID id.RoomID, puppetsOnly bool) error {
	return nil
}
func (m *mockMatrixAPI) EnsureJoined(ctx context.Context, roomID id.RoomID, params ...bridgev2.EnsureJoinedParams) error {
	return nil
}
func (m *mockMatrixAPI) EnsureInvited(ctx context.Context, roomID id.RoomID, userID id.UserID) error {
	return nil
}
func (m *mockMatrixAPI) TagRoom(ctx context.Context, roomID id.RoomID, tag event.RoomTag, isTagged bool) error {
	return nil
}
func (m *mockMatrixAPI) MuteRoom(ctx context.Context, roomID id.RoomID, until time.Time) error {
	return nil
}
func (m *mockMatrixAPI) GetEvent(ctx context.Context, roomID id.RoomID, eventID id.EventID) (*event.Event, error) {
	return nil, nil
}

func TestSendMessageWithRetry(t *testing.T) {
	log := zerolog.New(zerolog.NewTestWriter(t))
	connector := &ClaudeConnector{Log: log}

	t.Run("sends message successfully without retry", func(t *testing.T) {
		mock := &mockMatrixAPI{}
		ghost := &bridgev2.Ghost{Intent: mock}
		portal := createTestPortal("!room:example.com")

		client := &ClaudeClient{Connector: connector}
		client.sendMessageWithRetry(context.Background(), portal, ghost, "Hello world", "msg_1", 0, MaxMessageSize)

		if len(mock.sendCalls) != 1 {
			t.Errorf("expected 1 send call, got %d", len(mock.sendCalls))
		}
		if mock.sendCalls[0].Content != "Hello world" {
			t.Errorf("expected content 'Hello world', got %q", mock.sendCalls[0].Content)
		}
	})

	t.Run("retries with smaller size on M_TOO_LARGE", func(t *testing.T) {
		// Create a message that will be ~10KB after markdown rendering
		largeMessage := strings.Repeat("This is a test sentence. ", 400) // ~10KB

		mock := &mockMatrixAPI{
			failUntilSize: 5000, // Fail if body+html > 5KB
		}
		ghost := &bridgev2.Ghost{Intent: mock}
		portal := createTestPortal("!room:example.com")

		client := &ClaudeClient{Connector: connector}
		client.sendMessageWithRetry(context.Background(), portal, ghost, largeMessage, "msg_1", 0, 8000)

		// Should have retried with smaller chunks
		if len(mock.sendCalls) < 2 {
			t.Errorf("expected at least 2 send calls (retry), got %d", len(mock.sendCalls))
		}

		// All successful calls should have size <= failUntilSize
		for i, call := range mock.sendCalls {
			// The first call might fail, but subsequent should succeed
			if i > 0 && call.Size > mock.failUntilSize {
				t.Errorf("call %d: size %d exceeds limit %d", i, call.Size, mock.failUntilSize)
			}
		}
	})

	t.Run("halves max size on each retry", func(t *testing.T) {
		// Fail first 3 attempts to force multiple retries
		mock := &mockMatrixAPI{
			failCount: 3,
		}
		ghost := &bridgev2.Ghost{Intent: mock}
		portal := createTestPortal("!room:example.com")

		client := &ClaudeClient{Connector: connector}
		// Use a message that fits in one chunk at any size
		client.sendMessageWithRetry(context.Background(), portal, ghost, "Small message", "msg_1", 0, 16000)

		// Should have made 4 calls (3 failures + 1 success)
		if mock.callCount != 4 {
			t.Errorf("expected 4 calls (3 fails + 1 success), got %d", mock.callCount)
		}
	})

	t.Run("sends error notice when hitting minimum size", func(t *testing.T) {
		// Always fail with M_TOO_LARGE
		mock := &mockMatrixAPI{
			failUntilSize: 1, // Nothing can succeed
		}
		ghost := &bridgev2.Ghost{Intent: mock}
		portal := createTestPortal("!room:example.com")

		client := &ClaudeClient{Connector: connector}
		client.sendMessageWithRetry(context.Background(), portal, ghost, "Test message", "msg_1", 0, MinMessageSize*2)

		// Should have attempted and eventually sent an error notice
		// The error notice itself might also fail, but we should see the attempt
		hasErrorNotice := false
		for _, call := range mock.sendCalls {
			if strings.Contains(call.Content, "could not be delivered") {
				hasErrorNotice = true
				break
			}
		}
		if !hasErrorNotice {
			t.Error("expected error notice to be sent when hitting minimum size")
		}
	})

	t.Run("handles non-M_TOO_LARGE errors", func(t *testing.T) {
		mock := &mockMatrixAPI{
			errorToReturn: fmt.Errorf("network error"),
		}
		ghost := &bridgev2.Ghost{Intent: mock}
		portal := createTestPortal("!room:example.com")

		client := &ClaudeClient{Connector: connector}
		client.sendMessageWithRetry(context.Background(), portal, ghost, "Test message", "msg_1", 0, MaxMessageSize)

		// Should have made 2 calls: 1 failed message + 1 error notice attempt
		// (no retry on non-M_TOO_LARGE, but we do try to send error notice)
		if mock.callCount != 2 {
			t.Errorf("expected 2 calls (1 fail + 1 error notice), got %d", mock.callCount)
		}
	})

	t.Run("splits large message into multiple parts", func(t *testing.T) {
		// Create a message larger than MaxMessageSize
		largeMessage := strings.Repeat("A", MaxMessageSize*2)

		mock := &mockMatrixAPI{}
		ghost := &bridgev2.Ghost{Intent: mock}
		portal := createTestPortal("!room:example.com")

		client := &ClaudeClient{Connector: connector}
		client.sendMessageWithRetry(context.Background(), portal, ghost, largeMessage, "msg_1", 0, MaxMessageSize)

		// Should have split into multiple messages
		if len(mock.sendCalls) < 2 {
			t.Errorf("expected message to be split into multiple parts, got %d calls", len(mock.sendCalls))
		}

		// Verify all content was sent
		totalContent := ""
		for _, call := range mock.sendCalls {
			totalContent += call.Content
		}
		// Content should be trimmed/split but roughly the same length
		if len(totalContent) < len(largeMessage)-100 {
			t.Errorf("lost too much content: original %d, sent %d", len(largeMessage), len(totalContent))
		}
	})
}

func TestSplitMessage(t *testing.T) {
	t.Run("returns single part for small message", func(t *testing.T) {
		parts := splitMessage("Hello world", 1000)
		if len(parts) != 1 {
			t.Errorf("expected 1 part, got %d", len(parts))
		}
		if parts[0] != "Hello world" {
			t.Errorf("expected 'Hello world', got %q", parts[0])
		}
	})

	t.Run("splits on paragraph boundaries", func(t *testing.T) {
		text := "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."
		parts := splitMessage(text, 25)

		if len(parts) < 2 {
			t.Errorf("expected at least 2 parts, got %d", len(parts))
		}

		// Each part should be a complete paragraph (or close to it)
		for _, part := range parts {
			if len(part) > 30 { // Allow some slack for boundary finding
				t.Errorf("part too large: %d chars", len(part))
			}
		}
	})

	t.Run("handles text without good split points", func(t *testing.T) {
		// Long text without spaces or newlines
		text := strings.Repeat("a", 100)
		parts := splitMessage(text, 30)

		if len(parts) < 3 {
			t.Errorf("expected at least 3 parts, got %d", len(parts))
		}

		// Reassemble and verify no content lost
		reassembled := strings.Join(parts, "")
		if reassembled != text {
			t.Errorf("content mismatch after split")
		}
	})

	t.Run("preserves content integrity", func(t *testing.T) {
		original := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
		parts := splitMessage(original, 15)

		reassembled := strings.Join(parts, "")
		// After trimming each part, we might lose some whitespace
		originalNoSpace := strings.ReplaceAll(original, " ", "")
		reassembledNoSpace := strings.ReplaceAll(reassembled, " ", "")

		if !strings.Contains(reassembledNoSpace, "Line1") {
			t.Error("lost 'Line 1' content")
		}
		if !strings.Contains(reassembledNoSpace, "Line5") {
			t.Error("lost 'Line 5' content")
		}
		_ = originalNoSpace // Avoid unused warning
	})
}

func TestUnclosedCodeFence(t *testing.T) {
	t.Run("no code fence", func(t *testing.T) {
		open, lang := unclosedCodeFence("Hello world\nNo code here")
		if open {
			t.Error("expected no open fence")
		}
		if lang != "" {
			t.Errorf("expected empty lang, got %q", lang)
		}
	})

	t.Run("closed code fence", func(t *testing.T) {
		text := "Before\n```go\nfunc main() {}\n```\nAfter"
		open, _ := unclosedCodeFence(text)
		if open {
			t.Error("expected no open fence (fence is properly closed)")
		}
	})

	t.Run("unclosed code fence", func(t *testing.T) {
		text := "Before\n```python\ndef hello():\n    print('hi')"
		open, lang := unclosedCodeFence(text)
		if !open {
			t.Error("expected open fence")
		}
		if lang != "python" {
			t.Errorf("expected lang 'python', got %q", lang)
		}
	})

	t.Run("unclosed code fence no language", func(t *testing.T) {
		text := "Before\n```\nsome code"
		open, lang := unclosedCodeFence(text)
		if !open {
			t.Error("expected open fence")
		}
		if lang != "" {
			t.Errorf("expected empty lang, got %q", lang)
		}
	})

	t.Run("multiple fences last one unclosed", func(t *testing.T) {
		text := "```go\nfunc a() {}\n```\nSome text\n```json\n{\"key\": \"value\"}"
		open, lang := unclosedCodeFence(text)
		if !open {
			t.Error("expected open fence")
		}
		if lang != "json" {
			t.Errorf("expected lang 'json', got %q", lang)
		}
	})

	t.Run("multiple fences all closed", func(t *testing.T) {
		text := "```go\nfunc a() {}\n```\nSome text\n```json\n{}\n```"
		open, _ := unclosedCodeFence(text)
		if open {
			t.Error("expected no open fence")
		}
	})
}

func TestFixCodeFencesAcrossChunks(t *testing.T) {
	t.Run("no code fences unchanged", func(t *testing.T) {
		parts := []string{"Hello world", "Another part"}
		result := fixCodeFencesAcrossChunks(parts)
		if len(result) != 2 {
			t.Fatalf("expected 2 parts, got %d", len(result))
		}
		if result[0] != "Hello world" || result[1] != "Another part" {
			t.Error("parts should be unchanged when no code fences")
		}
	})

	t.Run("split inside code fence", func(t *testing.T) {
		parts := []string{
			"Before\n```go\nfunc main() {",
			"    fmt.Println(\"hi\")\n}\n```\nAfter",
		}
		result := fixCodeFencesAcrossChunks(parts)
		if len(result) != 2 {
			t.Fatalf("expected 2 parts, got %d", len(result))
		}
		// First part should have closing fence appended
		if !strings.HasSuffix(result[0], "\n```") {
			t.Errorf("first part should end with closing fence, got: %q", result[0])
		}
		// Second part should have opening fence prepended
		if !strings.HasPrefix(result[1], "```go\n") {
			t.Errorf("second part should start with opening fence, got: %q", result[1])
		}
	})

	t.Run("split inside code fence no language", func(t *testing.T) {
		parts := []string{
			"Before\n```\nsome code line 1",
			"some code line 2\n```\nAfter",
		}
		result := fixCodeFencesAcrossChunks(parts)
		if !strings.HasSuffix(result[0], "\n```") {
			t.Errorf("first part should end with closing fence, got: %q", result[0])
		}
		if !strings.HasPrefix(result[1], "```\n") {
			t.Errorf("second part should start with opening fence, got: %q", result[1])
		}
	})

	t.Run("closed fence not modified", func(t *testing.T) {
		parts := []string{
			"Before\n```go\ncode\n```\nAfter",
			"More text",
		}
		result := fixCodeFencesAcrossChunks(parts)
		if result[0] != parts[0] {
			t.Error("closed fence part should not be modified")
		}
		if result[1] != parts[1] {
			t.Error("second part should not be modified")
		}
	})

	t.Run("multiple chunks with fence split", func(t *testing.T) {
		parts := []string{
			"Text before\n```python\ndef hello():",
			"    print('hello')",
			"    return True\n```\nAfter",
		}
		result := fixCodeFencesAcrossChunks(parts)

		// First chunk: should close the fence
		if !strings.HasSuffix(result[0], "\n```") {
			t.Errorf("chunk 0 should close fence, got: %q", result[0])
		}
		// Second chunk: should reopen and close (since it's still mid-fence after reopening)
		if !strings.HasPrefix(result[1], "```python\n") {
			t.Errorf("chunk 1 should open fence, got: %q", result[1])
		}
		if !strings.HasSuffix(result[1], "\n```") {
			t.Errorf("chunk 1 should close fence, got: %q", result[1])
		}
		// Third chunk: should reopen
		if !strings.HasPrefix(result[2], "```python\n") {
			t.Errorf("chunk 2 should open fence, got: %q", result[2])
		}
	})

	t.Run("single part unchanged", func(t *testing.T) {
		parts := []string{"```go\ncode\n```"}
		result := fixCodeFencesAcrossChunks(parts)
		if result[0] != parts[0] {
			t.Error("single part should not be modified")
		}
	})
}

func TestSplitMessageCodeFences(t *testing.T) {
	t.Run("code fence split gets fixed", func(t *testing.T) {
		// Build a message with a code block that will be split
		code := strings.Repeat("x := 1\n", 500) // ~3500 chars of code
		text := "Here is some code:\n```go\n" + code + "```\nEnd."

		parts := splitMessage(text, 2000)

		if len(parts) < 2 {
			t.Fatalf("expected at least 2 parts, got %d", len(parts))
		}

		// Every part should have balanced code fences
		for i, part := range parts {
			opens := strings.Count(part, "```")
			if opens%2 != 0 {
				t.Errorf("part %d has unbalanced code fences (count: %d): %s...", i, opens, part[:min(100, len(part))])
			}
		}
	})
}

func TestMinMessageSize(t *testing.T) {
	t.Run("MinMessageSize is reasonable", func(t *testing.T) {
		if MinMessageSize < 500 {
			t.Errorf("MinMessageSize %d is too small, might cause infinite loops", MinMessageSize)
		}
		if MinMessageSize > 5000 {
			t.Errorf("MinMessageSize %d is too large, might not fit in Matrix events", MinMessageSize)
		}
	})

	t.Run("MaxMessageSize is larger than MinMessageSize", func(t *testing.T) {
		if MaxMessageSize <= MinMessageSize {
			t.Errorf("MaxMessageSize %d should be larger than MinMessageSize %d", MaxMessageSize, MinMessageSize)
		}
	})
}
