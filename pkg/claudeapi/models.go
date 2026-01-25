// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Model constants for Claude API (fallbacks if API unavailable).
const (
	// Claude 3.5 models (latest generation)
	ModelSonnet3_5 = "claude-3-5-sonnet-20241022"
	ModelHaiku3_5  = "claude-3-5-haiku-20241022"

	// Claude 3 models
	ModelOpus3   = "claude-3-opus-20240229"
	ModelSonnet3 = "claude-3-sonnet-20240229"
	ModelHaiku3  = "claude-3-haiku-20240307"
)

// DefaultModel is the default Claude model to use.
var DefaultModel = ModelSonnet3_5

// modelCache stores dynamically fetched models.
var (
	modelCache     []APIModel
	modelCacheMu   sync.RWMutex
	modelCacheTime time.Time
	modelCacheTTL  = 1 * time.Hour
)

// APIModel represents a model from the Claude API.
type APIModel struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	Type        string    `json:"type"`
}

// modelsResponse represents the response from /v1/models.
type modelsResponse struct {
	Data    []APIModel `json:"data"`
	HasMore bool       `json:"has_more"`
}

// FetchModels fetches available models from the Claude API.
func FetchModels(ctx context.Context, apiKey string) ([]APIModel, error) {
	// Check cache first
	modelCacheMu.RLock()
	if len(modelCache) > 0 && time.Since(modelCacheTime) < modelCacheTTL {
		models := make([]APIModel, len(modelCache))
		copy(models, modelCache)
		modelCacheMu.RUnlock()
		return models, nil
	}
	modelCacheMu.RUnlock()

	// Fetch from API
	req, err := http.NewRequestWithContext(ctx, "GET", DefaultBaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", DefaultVersion)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ParseAPIError(resp)
	}

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// Update cache
	modelCacheMu.Lock()
	modelCache = result.Data
	modelCacheTime = time.Now()
	modelCacheMu.Unlock()

	return result.Data, nil
}

// GetLatestModelByFamily returns the latest model for a given family (opus, sonnet, haiku).
func GetLatestModelByFamily(models []APIModel, family string) string {
	family = strings.ToLower(family)
	var candidates []APIModel

	for _, m := range models {
		modelLower := strings.ToLower(m.ID)
		if strings.Contains(modelLower, family) {
			candidates = append(candidates, m)
		}
	}

	if len(candidates) == 0 {
		// Return fallback
		switch family {
		case "opus":
			return ModelOpus3
		case "sonnet":
			return ModelSonnet3_5
		case "haiku":
			return ModelHaiku3_5
		default:
			return ModelSonnet3_5
		}
	}

	// Sort by created_at descending (newest first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
	})

	return candidates[0].ID
}

// ValidModels is a list of all valid Claude models.
var ValidModels = []string{
	ModelSonnet3_5,
	ModelHaiku3_5,
	ModelOpus3,
	ModelSonnet3,
	ModelHaiku3,
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
	ModelSonnet3: {
		Name:            "Claude 3 Sonnet",
		MaxInputTokens:  200000,
		MaxOutputTokens: 4096,
		Family:          "sonnet",
	},
	ModelHaiku3: {
		Name:            "Claude 3 Haiku",
		MaxInputTokens:  200000,
		MaxOutputTokens: 4096,
		Family:          "haiku",
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
