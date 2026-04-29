package claudeapi

import (
	"testing"
	"time"
)

func TestInferModelFamily(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"claude-opus-4-20250514", "opus"},
		{"claude-sonnet-4-20250514", "sonnet"},
		{"claude-haiku-4-20250514", "haiku"},
		{"claude-3-5-sonnet-20241022", "sonnet"},
		{"claude-3-opus-20240229", "opus"},
		{"claude-3-haiku-20240307", "haiku"},
		{"CLAUDE-OPUS-4", "opus"},
		{"Claude-Sonnet-4", "sonnet"},
		{"unknown-model-2025", ""},
		{"gpt-4o", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := inferModelFamily(tt.modelID)
			if got != tt.expected {
				t.Errorf("inferModelFamily(%q) = %q, want %q", tt.modelID, got, tt.expected)
			}
		})
	}
}

func TestModelCacheUpdate(t *testing.T) {
	cache := NewModelCache(1 * time.Minute)

	models := []ModelInfo{
		{ID: "claude-opus-4-20250514", DisplayName: "Claude Opus 4", Family: "opus", CreatedAt: time.Date(2025, 5, 14, 0, 0, 0, 0, time.UTC)},
		{ID: "claude-sonnet-4-20250514", DisplayName: "Claude Sonnet 4", Family: "sonnet", CreatedAt: time.Date(2025, 5, 14, 0, 0, 0, 0, time.UTC)},
	}

	cache.Update(models)

	if cache.IsEmpty() {
		t.Error("cache should not be empty after Update")
	}
	if cache.IsStale() {
		t.Error("cache should not be stale immediately after Update")
	}

	all := cache.GetAll()
	if len(all) != 2 {
		t.Errorf("expected 2 models, got %d", len(all))
	}
}

func TestModelCacheGet(t *testing.T) {
	cache := NewModelCache(1 * time.Minute)

	models := []ModelInfo{
		{ID: "claude-opus-4-20250514", DisplayName: "Claude Opus 4", Family: "opus"},
		{ID: "claude-sonnet-4-20250514", DisplayName: "Claude Sonnet 4", Family: "sonnet"},
	}
	cache.Update(models)

	t.Run("existing model", func(t *testing.T) {
		info := cache.Get("claude-opus-4-20250514")
		if info == nil {
			t.Fatal("expected model info, got nil")
		}
		if info.DisplayName != "Claude Opus 4" {
			t.Errorf("expected display name 'Claude Opus 4', got %q", info.DisplayName)
		}
	})

	t.Run("non-existent model", func(t *testing.T) {
		info := cache.Get("nonexistent-model")
		if info != nil {
			t.Error("expected nil for non-existent model")
		}
	})

	t.Run("returns copy not reference", func(t *testing.T) {
		info := cache.Get("claude-opus-4-20250514")
		info.DisplayName = "MUTATED"

		original := cache.Get("claude-opus-4-20250514")
		if original.DisplayName == "MUTATED" {
			t.Error("Get should return a copy, not a reference to cached data")
		}
	})
}

func TestModelCacheGetAll(t *testing.T) {
	cache := NewModelCache(1 * time.Minute)

	models := []ModelInfo{
		{ID: "model-1", DisplayName: "Model 1"},
	}
	cache.Update(models)

	all := cache.GetAll()
	all[0].DisplayName = "MUTATED"

	original := cache.GetAll()
	if original[0].DisplayName == "MUTATED" {
		t.Error("GetAll should return a copy, not a reference to cached data")
	}
}

func TestModelCacheIsStale(t *testing.T) {
	cache := NewModelCache(1 * time.Millisecond)

	cache.Update([]ModelInfo{{ID: "test"}})
	time.Sleep(5 * time.Millisecond)

	if !cache.IsStale() {
		t.Error("cache should be stale after TTL expires")
	}
}

func TestModelCacheIsEmpty(t *testing.T) {
	cache := NewModelCache(1 * time.Minute)

	if !cache.IsEmpty() {
		t.Error("new cache should be empty")
	}

	cache.Update([]ModelInfo{{ID: "test"}})
	if cache.IsEmpty() {
		t.Error("cache should not be empty after Update")
	}
}

func TestModelCacheDeepCopy(t *testing.T) {
	cache := NewModelCache(1 * time.Minute)

	original := []ModelInfo{
		{ID: "model-1", DisplayName: "Original"},
	}
	cache.Update(original)

	original[0].DisplayName = "MUTATED"

	cached := cache.Get("model-1")
	if cached.DisplayName == "MUTATED" {
		t.Error("Update should deep copy input, not store references")
	}
}

func TestEstimateMaxTokens(t *testing.T) {
	// Reset global cache for this test
	oldCache := globalModelCache
	globalModelCache = NewModelCache(15 * time.Minute)
	defer func() { globalModelCache = oldCache }()

	t.Run("cached values from API", func(t *testing.T) {
		globalModelCache.Update([]ModelInfo{
			{ID: "claude-sonnet-4-20250514", Family: "sonnet", MaxInputTokens: 250000, MaxOutputTokens: 80000},
		})

		input, output := EstimateMaxTokens("claude-sonnet-4-20250514")
		if input != 250000 {
			t.Errorf("expected input tokens 250000, got %d", input)
		}
		if output != 80000 {
			t.Errorf("expected output tokens 80000, got %d", output)
		}
	})

	t.Run("fallback for opus", func(t *testing.T) {
		globalModelCache = NewModelCache(15 * time.Minute)
		input, output := EstimateMaxTokens("claude-opus-4-20250514")
		if input != 200000 {
			t.Errorf("expected fallback input tokens 200000, got %d", input)
		}
		if output != 32000 {
			t.Errorf("expected fallback output tokens 32000, got %d", output)
		}
	})

	t.Run("fallback for sonnet", func(t *testing.T) {
		input, output := EstimateMaxTokens("claude-sonnet-4-20250514")
		if input != 200000 || output != 64000 {
			t.Errorf("expected 200000/64000, got %d/%d", input, output)
		}
	})

	t.Run("fallback for haiku", func(t *testing.T) {
		input, output := EstimateMaxTokens("claude-haiku-4-20250514")
		if input != 200000 || output != 64000 {
			t.Errorf("expected 200000/64000, got %d/%d", input, output)
		}
	})

	t.Run("fallback for unknown model", func(t *testing.T) {
		input, output := EstimateMaxTokens("unknown-model")
		if input != 200000 || output != 64000 {
			t.Errorf("expected 200000/64000 for unknown, got %d/%d", input, output)
		}
	})
}

func TestGetLatestModelByFamily(t *testing.T) {
	oldCache := globalModelCache
	globalModelCache = NewModelCache(15 * time.Minute)
	defer func() { globalModelCache = oldCache }()

	globalModelCache.Update([]ModelInfo{
		{ID: "claude-sonnet-3-5-20241022", Family: "sonnet", CreatedAt: time.Date(2024, 10, 22, 0, 0, 0, 0, time.UTC)},
		{ID: "claude-sonnet-4-20250514", Family: "sonnet", CreatedAt: time.Date(2025, 5, 14, 0, 0, 0, 0, time.UTC)},
		{ID: "claude-opus-4-20250514", Family: "opus", CreatedAt: time.Date(2025, 5, 14, 0, 0, 0, 0, time.UTC)},
	})

	t.Run("returns latest sonnet", func(t *testing.T) {
		got := GetLatestModelByFamily("sonnet")
		if got != "claude-sonnet-4-20250514" {
			t.Errorf("expected claude-sonnet-4-20250514, got %q", got)
		}
	})

	t.Run("returns latest opus", func(t *testing.T) {
		got := GetLatestModelByFamily("opus")
		if got != "claude-opus-4-20250514" {
			t.Errorf("expected claude-opus-4-20250514, got %q", got)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		got := GetLatestModelByFamily("SONNET")
		if got != "claude-sonnet-4-20250514" {
			t.Errorf("expected claude-sonnet-4-20250514, got %q", got)
		}
	})

	t.Run("returns empty for unknown family", func(t *testing.T) {
		got := GetLatestModelByFamily("unknown")
		if got != "" {
			t.Errorf("expected empty string for unknown family, got %q", got)
		}
	})

	t.Run("returns empty for empty cache", func(t *testing.T) {
		globalModelCache = NewModelCache(15 * time.Minute)
		got := GetLatestModelByFamily("sonnet")
		if got != "" {
			t.Errorf("expected empty string for empty cache, got %q", got)
		}
	})
}

func TestGetModelFamily(t *testing.T) {
	oldCache := globalModelCache
	globalModelCache = NewModelCache(15 * time.Minute)
	defer func() { globalModelCache = oldCache }()

	globalModelCache.Update([]ModelInfo{
		{ID: "custom-model-123", Family: "opus"},
	})

	t.Run("from cache", func(t *testing.T) {
		got := GetModelFamily("custom-model-123")
		if got != "opus" {
			t.Errorf("expected 'opus' from cache, got %q", got)
		}
	})

	t.Run("fallback to inference", func(t *testing.T) {
		got := GetModelFamily("claude-sonnet-4-20250514")
		if got != "sonnet" {
			t.Errorf("expected 'sonnet' from inference, got %q", got)
		}
	})
}

func TestGetModelDisplayName(t *testing.T) {
	oldCache := globalModelCache
	globalModelCache = NewModelCache(15 * time.Minute)
	defer func() { globalModelCache = oldCache }()

	globalModelCache.Update([]ModelInfo{
		{ID: "claude-opus-4-20250514", DisplayName: "Claude Opus 4"},
	})

	t.Run("from cache", func(t *testing.T) {
		got := GetModelDisplayName("claude-opus-4-20250514")
		if got != "Claude Opus 4" {
			t.Errorf("expected 'Claude Opus 4', got %q", got)
		}
	})

	t.Run("fallback to model ID", func(t *testing.T) {
		got := GetModelDisplayName("unknown-model")
		if got != "unknown-model" {
			t.Errorf("expected model ID as fallback, got %q", got)
		}
	})
}

func TestGetModelInfo(t *testing.T) {
	oldCache := globalModelCache
	globalModelCache = NewModelCache(15 * time.Minute)
	defer func() { globalModelCache = oldCache }()

	globalModelCache.Update([]ModelInfo{
		{ID: "claude-opus-4-20250514", DisplayName: "Claude Opus 4", Family: "opus"},
	})

	t.Run("returns cached info", func(t *testing.T) {
		info := GetModelInfo("claude-opus-4-20250514")
		if info.DisplayName != "Claude Opus 4" {
			t.Errorf("expected 'Claude Opus 4', got %q", info.DisplayName)
		}
	})

	t.Run("returns inferred info for uncached model", func(t *testing.T) {
		info := GetModelInfo("claude-sonnet-4-20250514")
		if info.ID != "claude-sonnet-4-20250514" {
			t.Errorf("expected ID to match, got %q", info.ID)
		}
		if info.Family != "sonnet" {
			t.Errorf("expected family 'sonnet', got %q", info.Family)
		}
	})
}

func TestGetModelMaxTokens(t *testing.T) {
	oldCache := globalModelCache
	globalModelCache = NewModelCache(15 * time.Minute)
	defer func() { globalModelCache = oldCache }()

	got := GetModelMaxTokens("claude-sonnet-4-20250514")
	if got != 200000 {
		t.Errorf("expected 200000, got %d", got)
	}
}
