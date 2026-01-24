// Package claudeapi provides a client for the Claude API.
package claudeapi

import "time"

// API client constants
const (
	// DefaultBaseURL is the default base URL for the Claude API.
	DefaultBaseURL = "https://api.anthropic.com/v1"

	// DefaultVersion is the default API version.
	DefaultVersion = "2023-06-01"

	// DefaultTimeout is the default HTTP client timeout.
	DefaultTimeout = 60 * time.Second
)

// Token estimation constants
const (
	// ApproxCharsPerToken is a rough estimate for token calculation.
	// Actual tokens may vary by ±25% depending on text content.
	// Claude tokenization averages about 4 characters per token for English text.
	ApproxCharsPerToken = 4

	// ContextTrimTargetPercent is the percentage of max tokens to trim to
	// when the conversation exceeds the token limit. Trimming to 80% provides
	// headroom for the next response.
	ContextTrimTargetPercent = 80

	// MinMessagesToKeep is the minimum number of messages to keep when trimming.
	// This ensures at least one user-assistant pair is preserved.
	MinMessagesToKeep = 2
)

// Streaming constants
const (
	// StreamEventBufferSize is the buffer size for the SSE event channel.
	// A buffer of 10 prevents blocking on fast event streams while
	// maintaining reasonable memory usage.
	StreamEventBufferSize = 10
)

// Retry constants
const (
	// MaxRetries is the maximum number of retry attempts for transient errors.
	MaxRetries = 3

	// InitialRetryDelay is the initial delay before the first retry.
	InitialRetryDelay = 1 * time.Second

	// MaxRetryDelay is the maximum delay between retries.
	MaxRetryDelay = 30 * time.Second

	// RetryBackoffMultiplier is the multiplier for exponential backoff.
	RetryBackoffMultiplier = 2.0
)

// Metrics event types
const (
	MetricEventRequest  = "request"
	MetricEventResponse = "response"
	MetricEventError    = "error"
	MetricEventRetry    = "retry"
)
