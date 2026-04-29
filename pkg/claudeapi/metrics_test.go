package claudeapi

import (
	"fmt"
	"testing"
	"time"
)

func TestMetricsRecordRequest(t *testing.T) {
	m := NewMetrics()

	m.RecordRequest("claude-sonnet-4", 100*time.Millisecond, 500, 200)

	if got := m.TotalRequests.Load(); got != 1 {
		t.Errorf("expected 1 total request, got %d", got)
	}
	if got := m.SuccessfulRequests.Load(); got != 1 {
		t.Errorf("expected 1 successful request, got %d", got)
	}
	if got := m.TotalInputTokens.Load(); got != 500 {
		t.Errorf("expected 500 input tokens, got %d", got)
	}
	if got := m.TotalOutputTokens.Load(); got != 200 {
		t.Errorf("expected 200 output tokens, got %d", got)
	}
}

func TestMetricsRecordRequestWithCache(t *testing.T) {
	m := NewMetrics()

	m.RecordRequestWithCache("claude-sonnet-4", 100*time.Millisecond, 500, 200, 1000, 800)

	if got := m.TotalCacheCreationTokens.Load(); got != 1000 {
		t.Errorf("expected 1000 cache creation tokens, got %d", got)
	}
	if got := m.TotalCacheReadTokens.Load(); got != 800 {
		t.Errorf("expected 800 cache read tokens, got %d", got)
	}
}

func TestMetricsRecordError(t *testing.T) {
	m := NewMetrics()

	m.RecordError(fmt.Errorf("rate_limit exceeded"))
	if got := m.RateLimitErrors.Load(); got != 1 {
		t.Errorf("expected 1 rate limit error, got %d", got)
	}

	m.RecordError(fmt.Errorf("authentication failed"))
	if got := m.AuthErrors.Load(); got != 1 {
		t.Errorf("expected 1 auth error, got %d", got)
	}

	m.RecordError(fmt.Errorf("overloaded"))
	if got := m.ServerErrors.Load(); got != 1 {
		t.Errorf("expected 1 server error, got %d", got)
	}

	m.RecordError(fmt.Errorf("something else"))
	if got := m.OtherErrors.Load(); got != 1 {
		t.Errorf("expected 1 other error, got %d", got)
	}

	if got := m.FailedRequests.Load(); got != 4 {
		t.Errorf("expected 4 failed requests, got %d", got)
	}
	if got := m.TotalRequests.Load(); got != 4 {
		t.Errorf("expected 4 total requests, got %d", got)
	}
}

func TestMetricsGetTotalTokens(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest("model", 0, 100, 50)
	m.RecordRequest("model", 0, 200, 100)

	if got := m.GetTotalTokens(); got != 450 {
		t.Errorf("expected 450 total tokens, got %d", got)
	}
}

func TestMetricsGetErrorRate(t *testing.T) {
	m := NewMetrics()

	if got := m.GetErrorRate(); got != 0 {
		t.Errorf("expected 0 error rate with no requests, got %f", got)
	}

	m.RecordRequest("model", 0, 100, 50)
	m.RecordError(fmt.Errorf("test error"))

	got := m.GetErrorRate()
	if got != 50 {
		t.Errorf("expected 50%% error rate, got %f", got)
	}
}

func TestMetricsGetAverageRequestDuration(t *testing.T) {
	m := NewMetrics()

	if got := m.GetAverageRequestDuration(); got != 0 {
		t.Errorf("expected 0 duration with no requests, got %v", got)
	}

	m.RecordRequest("model", 100*time.Millisecond, 0, 0)
	m.RecordRequest("model", 200*time.Millisecond, 0, 0)

	got := m.GetAverageRequestDuration()
	if got != 150*time.Millisecond {
		t.Errorf("expected 150ms average, got %v", got)
	}
}

func TestMetricsPerModel(t *testing.T) {
	m := NewMetrics()

	m.RecordRequest("model-a", 100*time.Millisecond, 100, 50)
	m.RecordRequest("model-a", 200*time.Millisecond, 200, 100)
	m.RecordRequest("model-b", 150*time.Millisecond, 300, 150)

	reqs, input, output, avgDur := m.GetModelStats("model-a")
	if reqs != 2 {
		t.Errorf("expected 2 requests for model-a, got %d", reqs)
	}
	if input != 300 {
		t.Errorf("expected 300 input tokens for model-a, got %d", input)
	}
	if output != 150 {
		t.Errorf("expected 150 output tokens for model-a, got %d", output)
	}
	if avgDur != 150*time.Millisecond {
		t.Errorf("expected 150ms avg duration for model-a, got %v", avgDur)
	}

	reqs, _, _, _ = m.GetModelStats("model-b")
	if reqs != 1 {
		t.Errorf("expected 1 request for model-b, got %d", reqs)
	}

	reqs, _, _, _ = m.GetModelStats("nonexistent")
	if reqs != 0 {
		t.Errorf("expected 0 requests for nonexistent model, got %d", reqs)
	}
}

func TestMetricsGetAllModelNames(t *testing.T) {
	m := NewMetrics()

	m.RecordRequest("model-a", 0, 0, 0)
	m.RecordRequest("model-b", 0, 0, 0)

	names := m.GetAllModelNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 model names, got %d", len(names))
	}

	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["model-a"] || !nameSet["model-b"] {
		t.Errorf("expected model-a and model-b, got %v", names)
	}
}

func TestMetricsSnapshot(t *testing.T) {
	m := NewMetrics()

	m.RecordRequest("model", 100*time.Millisecond, 500, 200)
	m.RecordError(fmt.Errorf("rate_limit"))
	m.RecordLocalRateLimitReject()
	m.RecordCircuitBreakerReject()
	m.RecordCircuitBreakerOpen()

	snap := m.Snapshot()

	if snap["total_requests"].(int64) != 2 {
		t.Errorf("expected 2 total requests, got %v", snap["total_requests"])
	}
	if snap["successful_requests"].(int64) != 1 {
		t.Errorf("expected 1 successful request, got %v", snap["successful_requests"])
	}
	if snap["failed_requests"].(int64) != 1 {
		t.Errorf("expected 1 failed request, got %v", snap["failed_requests"])
	}
	if snap["total_tokens"].(int64) != 700 {
		t.Errorf("expected 700 total tokens, got %v", snap["total_tokens"])
	}
	if snap["local_rate_limit_rejects"].(int64) != 1 {
		t.Errorf("expected 1 local rate limit reject, got %v", snap["local_rate_limit_rejects"])
	}
	if snap["circuit_breaker_rejects"].(int64) != 1 {
		t.Errorf("expected 1 circuit breaker reject, got %v", snap["circuit_breaker_rejects"])
	}
	if snap["circuit_breaker_opens"].(int64) != 1 {
		t.Errorf("expected 1 circuit breaker open, got %v", snap["circuit_breaker_opens"])
	}
}

func TestMetricsReset(t *testing.T) {
	m := NewMetrics()

	m.RecordRequest("model", 100*time.Millisecond, 500, 200)
	m.RecordError(fmt.Errorf("test"))
	m.RecordLocalRateLimitReject()

	m.Reset()

	if got := m.TotalRequests.Load(); got != 0 {
		t.Errorf("expected 0 after reset, got %d", got)
	}
	if got := m.TotalInputTokens.Load(); got != 0 {
		t.Errorf("expected 0 input tokens after reset, got %d", got)
	}
	if got := m.FailedRequests.Load(); got != 0 {
		t.Errorf("expected 0 failed requests after reset, got %d", got)
	}
	if got := m.LocalRateLimitRejects.Load(); got != 0 {
		t.Errorf("expected 0 local rate limit rejects after reset, got %d", got)
	}

	names := m.GetAllModelNames()
	if len(names) != 0 {
		t.Errorf("expected 0 models after reset, got %d", len(names))
	}
}
