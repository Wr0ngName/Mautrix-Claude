// Package claudeapi provides a client for the Claude API.
package claudeapi

// Model constants for Claude API.
const (
	ModelOpus4_5   = "claude-opus-4-5-20251101"
	ModelSonnet4_5 = "claude-sonnet-4-5-20250924"
	ModelSonnet3_5 = "claude-3-5-sonnet-20241022"
	ModelHaiku3_5  = "claude-3-5-haiku-20241022"
	ModelOpus3     = "claude-3-opus-20240229"
)

// DefaultModel is the default Claude model to use.
var DefaultModel = ModelSonnet3_5

// ValidModels is a list of all valid Claude models.
var ValidModels = []string{
	ModelOpus4_5,
	ModelSonnet4_5,
	ModelSonnet3_5,
	ModelHaiku3_5,
	ModelOpus3,
}

// ModelInfo contains information about a Claude model.
type ModelInfo struct {
	Name            string
	MaxInputTokens  int
	MaxOutputTokens int
	Family          string
}

// modelInfoMap maps model IDs to their info.
var modelInfoMap = map[string]*ModelInfo{
	ModelOpus4_5: {
		Name:            "Claude Opus 4.5",
		MaxInputTokens:  200000,
		MaxOutputTokens: 16384,
		Family:          "opus",
	},
	ModelSonnet4_5: {
		Name:            "Claude Sonnet 4.5",
		MaxInputTokens:  200000,
		MaxOutputTokens: 8192,
		Family:          "sonnet",
	},
	ModelSonnet3_5: {
		Name:            "Claude 3.5 Sonnet",
		MaxInputTokens:  200000,
		MaxOutputTokens: 8192,
		Family:          "sonnet",
	},
	ModelHaiku3_5: {
		Name:            "Claude 3.5 Haiku",
		MaxInputTokens:  200000,
		MaxOutputTokens: 8192,
		Family:          "haiku",
	},
	ModelOpus3: {
		Name:            "Claude 3 Opus",
		MaxInputTokens:  200000,
		MaxOutputTokens: 4096,
		Family:          "opus",
	},
}

// ValidateModel checks if a model name is valid.
func ValidateModel(model string) bool {
	for _, validModel := range ValidModels {
		if model == validModel {
			return true
		}
	}
	return false
}

// GetModelMaxTokens returns the maximum input tokens for a model.
// Returns a default value for unknown models.
func GetModelMaxTokens(model string) int {
	if info, ok := modelInfoMap[model]; ok {
		return info.MaxInputTokens
	}
	return 200000 // Default for unknown models
}

// GetModelMaxOutputTokens returns the maximum output tokens for a model.
// Returns a default value for unknown models.
func GetModelMaxOutputTokens(model string) int {
	if info, ok := modelInfoMap[model]; ok {
		return info.MaxOutputTokens
	}
	return 4096 // Default for unknown models
}

// GetModelInfo returns information about a model.
// Returns nil for unknown models.
func GetModelInfo(model string) *ModelInfo {
	return modelInfoMap[model]
}

// GetModelFamily returns the family (opus, sonnet, haiku) for a model.
// Returns empty string for unknown models.
func GetModelFamily(model string) string {
	if info, ok := modelInfoMap[model]; ok {
		return info.Family
	}
	return ""
}
