// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"context"
)

// MessageClient is the interface for sending messages to Claude.
// This interface is implemented by both the official API client and the web client.
type MessageClient interface {
	// CreateMessageStream sends a message and returns a channel of streaming events.
	CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error)

	// CreateMessage sends a message and returns the complete response (non-streaming).
	CreateMessage(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResponse, error)

	// Validate checks if the client credentials are valid.
	Validate(ctx context.Context) error

	// GetMetrics returns the metrics collector for this client.
	GetMetrics() *Metrics

	// GetClientType returns the type of client ("api" or "web").
	GetClientType() string
}

// ClientType constants for identifying client implementations.
const (
	ClientTypeAPI = "api"
	ClientTypeWeb = "web"
)
