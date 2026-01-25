package connector

import (
	"fmt"

	"go.mau.fi/mautrix-claude/pkg/claudeapi"
)

// DefaultTemperature is the default temperature when not specified.
const DefaultTemperature = 1.0

// Config contains the configuration for the Claude connector.
type Config struct {
	// DefaultModel is the default Claude model to use
	DefaultModel string `yaml:"default_model"`

	// MaxTokens is the maximum tokens for responses
	MaxTokens int `yaml:"max_tokens"`

	// Temperature controls randomness (0.0-1.0)
	// Using a pointer allows distinguishing between "not set" and "set to 0"
	Temperature *float64 `yaml:"temperature,omitempty"`

	// SystemPrompt is the default system prompt
	SystemPrompt string `yaml:"system_prompt"`

	// ConversationMaxAge is the maximum conversation age in hours (0 = unlimited)
	ConversationMaxAge int `yaml:"conversation_max_age_hours"`

	// RateLimitPerMinute is the rate limit (0 = unlimited)
	RateLimitPerMinute int `yaml:"rate_limit_per_minute"`
}

// ExampleConfig is the example configuration for the connector.
const ExampleConfig = `
    # Claude API connector configuration

    # Default Claude model to use
    # Valid models: claude-opus-4-5-20251101, claude-sonnet-4-5-20250924,
    #               claude-3-5-haiku-20241022, claude-3-opus-20240229
    default_model: claude-sonnet-4-5-20250924

    # Maximum tokens for responses (1-16384 depending on model)
    max_tokens: 4096

    # Temperature controls randomness (0.0-1.0, default 1.0)
    # Lower = more focused and deterministic
    # Higher = more creative and varied
    # Set to 0 for most deterministic responses
    temperature: 1.0

    # Default system prompt (can be overridden per room)
    system_prompt: "You are a helpful AI assistant."

    # Maximum conversation age in hours (0 = unlimited)
    # Older conversations will be cleared from context
    conversation_max_age_hours: 24

    # Rate limiting (requests per minute, 0 = unlimited)
    # Helps prevent API rate limit errors
    rate_limit_per_minute: 60
`

// Validate validates the configuration.
func (c *Config) Validate() error {
	// Validate model if set
	if c.DefaultModel != "" && !claudeapi.ValidateModel(c.DefaultModel) {
		return fmt.Errorf("invalid model: %s", c.DefaultModel)
	}

	// Validate temperature if set
	if c.Temperature != nil {
		if *c.Temperature < 0 || *c.Temperature > 1 {
			return fmt.Errorf("temperature must be between 0 and 1, got %f", *c.Temperature)
		}
	}

	// Validate max tokens
	if c.MaxTokens < 0 {
		return fmt.Errorf("max_tokens must be non-negative, got %d", c.MaxTokens)
	}

	// Check against the maximum output tokens across all models (opus 4.5 has 16384)
	if c.MaxTokens > 16384 {
		return fmt.Errorf("max_tokens (%d) exceeds maximum supported (16384)", c.MaxTokens)
	}

	// Validate conversation max age
	if c.ConversationMaxAge < 0 {
		return fmt.Errorf("conversation_max_age_hours must be non-negative, got %d", c.ConversationMaxAge)
	}

	// Validate rate limit
	if c.RateLimitPerMinute < 0 {
		return fmt.Errorf("rate_limit_per_minute must be non-negative, got %d", c.RateLimitPerMinute)
	}

	return nil
}

// GetDefaultModel returns the default model, using a fallback if not set.
func (c *Config) GetDefaultModel() string {
	if c.DefaultModel == "" {
		return claudeapi.DefaultModel
	}
	return c.DefaultModel
}

// GetMaxTokens returns the max tokens, using a default if not set.
func (c *Config) GetMaxTokens() int {
	if c.MaxTokens == 0 {
		return 4096
	}
	return c.MaxTokens
}

// GetTemperature returns the temperature, using a default if not set.
// This correctly handles the case where temperature is explicitly set to 0.
func (c *Config) GetTemperature() float64 {
	if c.Temperature == nil {
		return DefaultTemperature
	}
	return *c.Temperature
}

// GetSystemPrompt returns the system prompt, using a default if not set.
func (c *Config) GetSystemPrompt() string {
	if c.SystemPrompt == "" {
		return "You are a helpful AI assistant."
	}
	return c.SystemPrompt
}

// TemperaturePtr is a helper to create a pointer to a float64.
// Useful for setting temperature in config.
func TemperaturePtr(t float64) *float64 {
	return &t
}
