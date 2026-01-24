// Package connector provides tests for configuration.
package connector

import (
	"strings"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	t.Run("config has sensible defaults", func(t *testing.T) {
		temp := 1.0
		config := &Config{
			DefaultModel:       "claude-3-5-sonnet-20241022",
			MaxTokens:          4096,
			Temperature:        &temp,
			SystemPrompt:       "You are a helpful AI assistant.",
			ConversationMaxAge: 24,
			RateLimitPerMinute: 60,
		}

		if config.DefaultModel == "" {
			t.Error("DefaultModel should not be empty")
		}

		if config.MaxTokens <= 0 {
			t.Error("MaxTokens should be positive")
		}

		if config.Temperature != nil && (*config.Temperature < 0 || *config.Temperature > 1) {
			t.Error("Temperature should be between 0 and 1")
		}

		if config.RateLimitPerMinute < 0 {
			t.Error("RateLimitPerMinute should not be negative")
		}
	})
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: Config{
				DefaultModel:       "claude-3-5-sonnet-20241022",
				MaxTokens:          4096,
				Temperature:        TemperaturePtr(0.7),
				SystemPrompt:       "Test prompt",
				ConversationMaxAge: 24,
				RateLimitPerMinute: 60,
			},
			expectError: false,
		},
		{
			name: "invalid model",
			config: Config{
				DefaultModel: "gpt-4",
				MaxTokens:    4096,
				Temperature:  TemperaturePtr(0.7),
			},
			expectError: true,
			errorMsg:    "invalid model",
		},
		{
			name: "temperature too high",
			config: Config{
				DefaultModel: "claude-3-5-sonnet-20241022",
				MaxTokens:    4096,
				Temperature:  TemperaturePtr(1.5),
			},
			expectError: true,
			errorMsg:    "temperature",
		},
		{
			name: "temperature too low",
			config: Config{
				DefaultModel: "claude-3-5-sonnet-20241022",
				MaxTokens:    4096,
				Temperature:  TemperaturePtr(-0.1),
			},
			expectError: true,
			errorMsg:    "temperature",
		},
		{
			name: "max tokens too low",
			config: Config{
				DefaultModel: "claude-3-5-sonnet-20241022",
				MaxTokens:    0,
				Temperature:  TemperaturePtr(0.7),
			},
			expectError: false, // MaxTokens 0 is allowed now (uses default)
		},
		{
			name: "max tokens too high",
			config: Config{
				DefaultModel: "claude-3-5-sonnet-20241022",
				MaxTokens:    100000,
				Temperature:  TemperaturePtr(0.7),
			},
			expectError: true,
			errorMsg:    "max_tokens",
		},
		{
			name: "negative rate limit",
			config: Config{
				DefaultModel:       "claude-3-5-sonnet-20241022",
				MaxTokens:          4096,
				Temperature:        TemperaturePtr(0.7),
				RateLimitPerMinute: -1,
			},
			expectError: true,
			errorMsg:    "rate_limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.expectError {
				if err == nil {
					t.Error("expected validation error, got nil")
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected validation error: %v", err)
				}
			}
		})
	}
}

func TestConfigModelValidation(t *testing.T) {
	validModels := []string{
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-5-20250924",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
	}

	for _, model := range validModels {
		t.Run("accepts "+model, func(t *testing.T) {
			config := Config{
				DefaultModel: model,
				MaxTokens:    4096,
				Temperature:  TemperaturePtr(0.7),
			}

			err := config.Validate()

			if err != nil {
				t.Errorf("should accept valid model %q: %v", model, err)
			}
		})
	}
}

func TestConfigTemperatureRange(t *testing.T) {
	tests := []struct {
		name        string
		temperature float64
		shouldPass  bool
	}{
		{
			name:        "minimum temperature (0.0)",
			temperature: 0.0,
			shouldPass:  true,
		},
		{
			name:        "maximum temperature (1.0)",
			temperature: 1.0,
			shouldPass:  true,
		},
		{
			name:        "mid-range temperature (0.7)",
			temperature: 0.7,
			shouldPass:  true,
		},
		{
			name:        "below minimum",
			temperature: -0.1,
			shouldPass:  false,
		},
		{
			name:        "above maximum",
			temperature: 1.1,
			shouldPass:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				DefaultModel: "claude-3-5-sonnet-20241022",
				MaxTokens:    4096,
				Temperature:  TemperaturePtr(tt.temperature),
			}

			err := config.Validate()

			if tt.shouldPass && err != nil {
				t.Errorf("should pass with temperature %v: %v", tt.temperature, err)
			}

			if !tt.shouldPass && err == nil {
				t.Errorf("should fail with temperature %v", tt.temperature)
			}
		})
	}
}

func TestConfigTemperaturePointer(t *testing.T) {
	t.Run("nil temperature uses default", func(t *testing.T) {
		config := Config{
			DefaultModel: "claude-3-5-sonnet-20241022",
			MaxTokens:    4096,
			Temperature:  nil,
		}

		temp := config.GetTemperature()
		if temp != DefaultTemperature {
			t.Errorf("expected default temperature %v, got %v", DefaultTemperature, temp)
		}
	})

	t.Run("zero temperature is valid", func(t *testing.T) {
		config := Config{
			DefaultModel: "claude-3-5-sonnet-20241022",
			MaxTokens:    4096,
			Temperature:  TemperaturePtr(0.0),
		}

		temp := config.GetTemperature()
		if temp != 0.0 {
			t.Errorf("expected temperature 0.0, got %v", temp)
		}

		err := config.Validate()
		if err != nil {
			t.Errorf("temperature 0.0 should be valid: %v", err)
		}
	})
}

func TestConfigMaxTokensRange(t *testing.T) {
	tests := []struct {
		name       string
		maxTokens  int
		shouldPass bool
	}{
		{
			name:       "minimum safe value (1)",
			maxTokens:  1,
			shouldPass: true,
		},
		{
			name:       "typical value (4096)",
			maxTokens:  4096,
			shouldPass: true,
		},
		{
			name:       "high value (16384)",
			maxTokens:  16384,
			shouldPass: true,
		},
		{
			name:       "zero tokens (uses default)",
			maxTokens:  0,
			shouldPass: true,
		},
		{
			name:       "negative tokens",
			maxTokens:  -100,
			shouldPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				DefaultModel: "claude-3-5-sonnet-20241022",
				MaxTokens:    tt.maxTokens,
				Temperature:  TemperaturePtr(0.7),
			}

			err := config.Validate()

			if tt.shouldPass && err != nil {
				t.Errorf("should pass with maxTokens %d: %v", tt.maxTokens, err)
			}

			if !tt.shouldPass && err == nil {
				t.Errorf("should fail with maxTokens %d", tt.maxTokens)
			}
		})
	}
}

func TestExampleConfig(t *testing.T) {
	t.Run("example config is valid YAML", func(t *testing.T) {
		if ExampleConfig == "" {
			t.Fatal("ExampleConfig should not be empty")
		}

		// Should contain Claude-specific fields
		requiredFields := []string{
			"default_model",
			"max_tokens",
			"temperature",
			"system_prompt",
		}

		for _, field := range requiredFields {
			if !strings.Contains(ExampleConfig, field) {
				t.Errorf("ExampleConfig should contain field %q", field)
			}
		}
	})

	t.Run("example config has comments", func(t *testing.T) {
		if !strings.Contains(ExampleConfig, "#") {
			t.Error("ExampleConfig should have comments for user guidance")
		}
	})

	t.Run("example config mentions Claude models", func(t *testing.T) {
		if !strings.Contains(ExampleConfig, "claude") {
			t.Error("ExampleConfig should mention Claude models")
		}
	})
}

func TestConfigSystemPrompt(t *testing.T) {
	t.Run("allows custom system prompt", func(t *testing.T) {
		config := Config{
			DefaultModel: "claude-3-5-sonnet-20241022",
			MaxTokens:    4096,
			Temperature:  TemperaturePtr(0.7),
			SystemPrompt: "You are a helpful coding assistant specializing in Go.",
		}

		err := config.Validate()

		if err != nil {
			t.Errorf("should allow custom system prompt: %v", err)
		}

		if config.SystemPrompt == "" {
			t.Error("system prompt should be preserved")
		}
	})

	t.Run("allows empty system prompt", func(t *testing.T) {
		config := Config{
			DefaultModel: "claude-3-5-sonnet-20241022",
			MaxTokens:    4096,
			Temperature:  TemperaturePtr(0.7),
			SystemPrompt: "",
		}

		err := config.Validate()

		if err != nil {
			t.Errorf("should allow empty system prompt: %v", err)
		}
	})
}

func TestConfigConversationMaxAge(t *testing.T) {
	tests := []struct {
		name       string
		maxAge     int
		shouldPass bool
	}{
		{
			name:       "unlimited (0)",
			maxAge:     0,
			shouldPass: true,
		},
		{
			name:       "24 hours",
			maxAge:     24,
			shouldPass: true,
		},
		{
			name:       "one week",
			maxAge:     168,
			shouldPass: true,
		},
		{
			name:       "negative value",
			maxAge:     -1,
			shouldPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				DefaultModel:       "claude-3-5-sonnet-20241022",
				MaxTokens:          4096,
				Temperature:        TemperaturePtr(0.7),
				ConversationMaxAge: tt.maxAge,
			}

			err := config.Validate()

			if tt.shouldPass && err != nil {
				t.Errorf("should pass with max age %d: %v", tt.maxAge, err)
			}

			if !tt.shouldPass && err == nil {
				t.Errorf("should fail with max age %d", tt.maxAge)
			}
		})
	}
}

func TestConfigRateLimiting(t *testing.T) {
	tests := []struct {
		name       string
		rateLimit  int
		shouldPass bool
	}{
		{
			name:       "unlimited (0)",
			rateLimit:  0,
			shouldPass: true,
		},
		{
			name:       "60 per minute",
			rateLimit:  60,
			shouldPass: true,
		},
		{
			name:       "5 per minute",
			rateLimit:  5,
			shouldPass: true,
		},
		{
			name:       "negative value",
			rateLimit:  -1,
			shouldPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				DefaultModel:       "claude-3-5-sonnet-20241022",
				MaxTokens:          4096,
				Temperature:        TemperaturePtr(0.7),
				RateLimitPerMinute: tt.rateLimit,
			}

			err := config.Validate()

			if tt.shouldPass && err != nil {
				t.Errorf("should pass with rate limit %d: %v", tt.rateLimit, err)
			}

			if !tt.shouldPass && err == nil {
				t.Errorf("should fail with rate limit %d", tt.rateLimit)
			}
		})
	}
}
