// Package claudeapi provides a client for the Claude API.
package claudeapi

// Message represents a message in a conversation.
type Message struct {
	Role    string    `json:"role"`    // "user" or "assistant"
	Content []Content `json:"content"` // Text, images, etc.
}

// Content represents content within a message.
type Content struct {
	Type   string       `json:"type"`             // "text" or "image"
	Text   string       `json:"text,omitempty"`   // Text content
	Source *ImageSource `json:"source,omitempty"` // Image source
}

// ImageSource represents an image source.
type ImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/jpeg", "image/png", etc.
	Data      string `json:"data"`       // Base64-encoded image data
}

// CreateMessageRequest represents a request to create a message.
type CreateMessageRequest struct {
	Model       string                 `json:"model"`
	Messages    []Message              `json:"messages"`
	MaxTokens   int                    `json:"max_tokens"`
	Temperature float64                `json:"temperature,omitempty"`
	System      string                 `json:"system,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// CreateMessageResponse represents a response from creating a message.
type CreateMessageResponse struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Role         string    `json:"role"`
	Content      []Content `json:"content"`
	Model        string    `json:"model"`
	StopReason   string    `json:"stop_reason,omitempty"`
	StopSequence string    `json:"stop_sequence,omitempty"`
	Usage        *Usage    `json:"usage,omitempty"`
}

// Usage represents token usage information.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StreamEvent represents an event in a streaming response.
type StreamEvent struct {
	Type    string                 `json:"type"` // "message_start", "content_block_delta", "message_stop", "error", etc.
	Index   int                    `json:"index,omitempty"`
	Delta   *ContentDelta          `json:"delta,omitempty"`
	Message *CreateMessageResponse `json:"message,omitempty"`
	Usage   *Usage                 `json:"usage,omitempty"`
	Error   *StreamError           `json:"error,omitempty"` // Error details for "error" type events
}

// StreamError represents an error in a streaming response.
type StreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ContentDelta represents incremental content in a streaming response.
type ContentDelta struct {
	Type string `json:"type"` // "text_delta"
	Text string `json:"text"`
}

// APIError represents an error from the Claude API.
type APIError struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	RetryAfter int    `json:"-"` // Retry-After header value in seconds
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return e.Type + ": " + e.Message
}
