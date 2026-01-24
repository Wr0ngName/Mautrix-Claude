// Package claudeapi provides tests for model definitions.
package claudeapi

import (
	"testing"
)

func TestValidateModel(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		isValid bool
	}{
		{
			name:    "validates claude-opus-4.5",
			model:   "claude-opus-4-5-20251101",
			isValid: true,
		},
		{
			name:    "validates claude-sonnet-4.5",
			model:   "claude-sonnet-4-5-20250924",
			isValid: true,
		},
		{
			name:    "validates claude-3-5-sonnet",
			model:   "claude-3-5-sonnet-20241022",
			isValid: true,
		},
		{
			name:    "validates claude-3-5-haiku",
			model:   "claude-3-5-haiku-20241022",
			isValid: true,
		},
		{
			name:    "validates claude-3-opus",
			model:   "claude-3-opus-20240229",
			isValid: true,
		},
		{
			name:    "rejects invalid model name",
			model:   "gpt-4",
			isValid: false,
		},
		{
			name:    "rejects empty model name",
			model:   "",
			isValid: false,
		},
		{
			name:    "rejects unknown claude model",
			model:   "claude-unknown-model",
			isValid: false,
		},
		{
			name:    "case sensitive validation",
			model:   "CLAUDE-3-5-SONNET-20241022",
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := ValidateModel(tt.model)

			if isValid != tt.isValid {
				t.Errorf("expected ValidateModel(%q) = %v, got %v", tt.model, tt.isValid, isValid)
			}
		})
	}
}

func TestGetModelMaxTokens(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		expectMaxInput int
		expectMaxOut   int
	}{
		{
			name:           "opus 4.5 has correct limits",
			model:          "claude-opus-4-5-20251101",
			expectMaxInput: 200000,
			expectMaxOut:   16384,
		},
		{
			name:           "sonnet 4.5 has correct limits",
			model:          "claude-sonnet-4-5-20250924",
			expectMaxInput: 200000,
			expectMaxOut:   8192,
		},
		{
			name:           "sonnet 3.5 has correct limits",
			model:          "claude-3-5-sonnet-20241022",
			expectMaxInput: 200000,
			expectMaxOut:   8192,
		},
		{
			name:           "haiku 3.5 has correct limits",
			model:          "claude-3-5-haiku-20241022",
			expectMaxInput: 200000,
			expectMaxOut:   8192,
		},
		{
			name:           "opus 3 has correct limits",
			model:          "claude-3-opus-20240229",
			expectMaxInput: 200000,
			expectMaxOut:   4096,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxInput := GetModelMaxTokens(tt.model)

			if maxInput != tt.expectMaxInput {
				t.Errorf("expected max input %d, got %d", tt.expectMaxInput, maxInput)
			}

			maxOutput := GetModelMaxOutputTokens(tt.model)

			if maxOutput != tt.expectMaxOut {
				t.Errorf("expected max output %d, got %d", tt.expectMaxOut, maxOutput)
			}
		})
	}
}

func TestGetModelMaxTokensUnknown(t *testing.T) {
	t.Run("returns default for unknown model", func(t *testing.T) {
		maxTokens := GetModelMaxTokens("unknown-model")

		if maxTokens <= 0 {
			t.Error("should return positive default value for unknown model")
		}
	})
}

func TestDefaultModel(t *testing.T) {
	t.Run("default model is valid", func(t *testing.T) {
		if DefaultModel == "" {
			t.Error("DefaultModel should not be empty")
		}

		if !ValidateModel(DefaultModel) {
			t.Errorf("DefaultModel %q should be valid", DefaultModel)
		}
	})
}

func TestValidModels(t *testing.T) {
	t.Run("ValidModels is not empty", func(t *testing.T) {
		if len(ValidModels) == 0 {
			t.Error("ValidModels should contain at least one model")
		}
	})

	t.Run("all models in ValidModels are valid", func(t *testing.T) {
		for _, model := range ValidModels {
			if !ValidateModel(model) {
				t.Errorf("model %q in ValidModels should be valid", model)
			}
		}
	})

	t.Run("no duplicate models", func(t *testing.T) {
		seen := make(map[string]bool)
		for _, model := range ValidModels {
			if seen[model] {
				t.Errorf("duplicate model %q in ValidModels", model)
			}
			seen[model] = true
		}
	})
}

func TestModelConstants(t *testing.T) {
	t.Run("model constants are defined", func(t *testing.T) {
		constants := []string{
			ModelOpus4_5,
			ModelSonnet4_5,
			ModelSonnet3_5,
			ModelHaiku3_5,
			ModelOpus3,
		}

		for _, constant := range constants {
			if constant == "" {
				t.Error("model constant should not be empty")
			}

			if !ValidateModel(constant) {
				t.Errorf("model constant %q should be valid", constant)
			}
		}
	})
}

func TestGetModelInfo(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		expectName  string
		expectValid bool
	}{
		{
			name:        "gets info for opus 4.5",
			model:       "claude-opus-4-5-20251101",
			expectName:  "Claude Opus 4.5",
			expectValid: true,
		},
		{
			name:        "gets info for sonnet 3.5",
			model:       "claude-3-5-sonnet-20241022",
			expectName:  "Claude 3.5 Sonnet",
			expectValid: true,
		},
		{
			name:        "returns error for invalid model",
			model:       "invalid-model",
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := GetModelInfo(tt.model)

			if tt.expectValid {
				if info == nil {
					t.Fatal("expected model info, got nil")
				}

				if info.Name == "" {
					t.Error("model name should not be empty")
				}

				if info.MaxInputTokens <= 0 {
					t.Error("max input tokens should be positive")
				}

				if info.MaxOutputTokens <= 0 {
					t.Error("max output tokens should be positive")
				}
			} else {
				if info != nil {
					t.Error("expected nil for invalid model")
				}
			}
		})
	}
}

func TestGetModelFamily(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		expectFamily string
	}{
		{
			name:         "opus 4.5 is opus family",
			model:        "claude-opus-4-5-20251101",
			expectFamily: "opus",
		},
		{
			name:         "sonnet 3.5 is sonnet family",
			model:        "claude-3-5-sonnet-20241022",
			expectFamily: "sonnet",
		},
		{
			name:         "haiku 3.5 is haiku family",
			model:        "claude-3-5-haiku-20241022",
			expectFamily: "haiku",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			family := GetModelFamily(tt.model)

			if family != tt.expectFamily {
				t.Errorf("expected family %q, got %q", tt.expectFamily, family)
			}
		})
	}
}
