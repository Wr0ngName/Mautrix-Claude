// Package claudeapi provides a wrapper around the official Anthropic Go SDK.
package claudeapi

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rs/zerolog"
)

// Client wraps the official Anthropic SDK client.
type Client struct {
	sdk     *anthropic.Client
	Log     zerolog.Logger
	Metrics *Metrics
}

// Ensure Client implements MessageClient interface.
var _ MessageClient = (*Client)(nil)

// NewClient creates a new Claude API client using the official SDK.
func NewClient(apiKey string, log zerolog.Logger) *Client {
	sdk := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)

	return &Client{
		sdk:     &sdk,
		Log:     log,
		Metrics: NewMetrics(),
	}
}

// Validate checks if the API key is valid by making a minimal test request.
func (c *Client) Validate(ctx context.Context) error {
	// Use the Models API to validate the key
	_, err := c.sdk.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		c.Log.Debug().Err(err).Msg("API key validation failed")
		return err
	}
	return nil
}

// CreateMessage creates a new message (non-streaming).
func (c *Client) CreateMessage(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResponse, error) {
	startTime := time.Now()

	// Convert our request to SDK format
	sdkParams := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  convertMessagesToSDK(req.Messages),
	}

	if req.System != "" {
		sdkParams.System = []anthropic.TextBlockParam{
			{Text: req.System},
		}
	}

	if req.Temperature >= 0 {
		sdkParams.Temperature = anthropic.Float(req.Temperature)
	}

	c.Log.Debug().
		Str("model", req.Model).
		Int("max_tokens", req.MaxTokens).
		Msg("Sending message to Claude API")

	resp, err := c.sdk.Messages.New(ctx, sdkParams)
	if err != nil {
		c.Metrics.RecordError(err)
		return nil, err
	}

	// Record metrics
	duration := time.Since(startTime)
	inputTokens := int(resp.Usage.InputTokens)
	outputTokens := int(resp.Usage.OutputTokens)
	c.Metrics.RecordRequest(req.Model, duration, inputTokens, outputTokens)

	// Convert response to our format
	return convertSDKResponse(resp), nil
}

// CreateMessageStream creates a new message with streaming.
func (c *Client) CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error) {
	// Convert our request to SDK format
	sdkParams := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  convertMessagesToSDK(req.Messages),
	}

	if req.System != "" {
		sdkParams.System = []anthropic.TextBlockParam{
			{Text: req.System},
		}
	}

	if req.Temperature >= 0 {
		sdkParams.Temperature = anthropic.Float(req.Temperature)
	}

	c.Log.Debug().
		Str("model", req.Model).
		Int("max_tokens", req.MaxTokens).
		Msg("Starting streaming message to Claude API")

	stream := c.sdk.Messages.NewStreaming(ctx, sdkParams)

	// Create output channel
	eventCh := make(chan StreamEvent, 100)

	// Start goroutine to process stream
	go func() {
		defer close(eventCh)
		startTime := time.Now()
		var inputTokens, outputTokens int

		for stream.Next() {
			event := stream.Current()

			// Convert SDK event to our format
			streamEvent := convertSDKStreamEvent(event)
			if streamEvent != nil {
				// Track token usage
				if streamEvent.Message != nil && streamEvent.Message.Usage != nil {
					if streamEvent.Message.Usage.InputTokens > 0 {
						inputTokens = streamEvent.Message.Usage.InputTokens
					}
				}
				if streamEvent.Usage != nil && streamEvent.Usage.OutputTokens > 0 {
					outputTokens = streamEvent.Usage.OutputTokens
				}

				eventCh <- *streamEvent
			}
		}

		if err := stream.Err(); err != nil {
			c.Log.Error().Err(err).Msg("Stream error")
			c.Metrics.RecordError(err)
			eventCh <- StreamEvent{
				Type: "error",
				Error: &StreamError{
					Type:    "stream_error",
					Message: err.Error(),
				},
			}
		} else {
			// Record successful request
			duration := time.Since(startTime)
			c.Metrics.RecordRequest(req.Model, duration, inputTokens, outputTokens)
		}
	}()

	return eventCh, nil
}

// GetClientType returns the client type identifier.
func (c *Client) GetClientType() string {
	return ClientTypeAPI
}

// GetMetrics returns the metrics collector.
func (c *Client) GetMetrics() *Metrics {
	return c.Metrics
}

// convertMessagesToSDK converts our message format to SDK format.
func convertMessagesToSDK(messages []Message) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, 0, len(messages))

	for _, msg := range messages {
		var blocks []anthropic.ContentBlockParamUnion

		for _, content := range msg.Content {
			switch content.Type {
			case "text":
				blocks = append(blocks, anthropic.NewTextBlock(content.Text))
			case "image":
				if content.Source != nil {
					blocks = append(blocks, anthropic.NewImageBlockBase64(
						content.Source.MediaType,
						content.Source.Data,
					))
				}
			}
		}

		switch msg.Role {
		case "user":
			result = append(result, anthropic.NewUserMessage(blocks...))
		case "assistant":
			result = append(result, anthropic.NewAssistantMessage(blocks...))
		}
	}

	return result
}

// convertSDKResponse converts SDK response to our format.
func convertSDKResponse(resp *anthropic.Message) *CreateMessageResponse {
	var content []Content
	for _, block := range resp.Content {
		if block.Type == "text" {
			content = append(content, Content{
				Type: "text",
				Text: block.Text,
			})
		}
	}

	return &CreateMessageResponse{
		ID:         resp.ID,
		Type:       string(resp.Type),
		Role:       string(resp.Role),
		Content:    content,
		Model:      string(resp.Model),
		StopReason: string(resp.StopReason),
		Usage: &Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}
}

// convertSDKStreamEvent converts SDK stream event to our format.
// Includes defensive nil checks to prevent panics from malformed responses.
func convertSDKStreamEvent(event anthropic.MessageStreamEventUnion) *StreamEvent {
	switch event.Type {
	case "message_start":
		// Defensive nil checks for message_start event
		if event.Message.ID != "" {
			usage := &Usage{}
			if event.Message.Usage.InputTokens > 0 {
				usage.InputTokens = int(event.Message.Usage.InputTokens)
			}
			return &StreamEvent{
				Type: "message_start",
				Message: &CreateMessageResponse{
					ID:    event.Message.ID,
					Model: string(event.Message.Model),
					Usage: usage,
				},
			}
		}
	case "content_block_delta":
		// Defensive check for delta content
		if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
			return &StreamEvent{
				Type: "content_block_delta",
				Delta: &ContentDelta{
					Type: "text_delta",
					Text: event.Delta.Text,
				},
			}
		}
	case "message_delta":
		usage := &Usage{}
		if event.Usage.OutputTokens > 0 {
			usage.OutputTokens = int(event.Usage.OutputTokens)
		}
		return &StreamEvent{
			Type:  "message_delta",
			Usage: usage,
		}
	case "message_stop":
		return &StreamEvent{
			Type: "message_stop",
		}
	case "error":
		return &StreamEvent{
			Type: "error",
			Error: &StreamError{
				Type:    "api_error",
				Message: fmt.Sprintf("%v", event),
			},
		}
	}
	return nil
}
