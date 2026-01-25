# Concurrency Review Report: mautrix-claude Bridge
**Date:** 2026-01-25
**Reviewer:** Claude Code (Concurrency Analysis)
**Focus Areas:** Race conditions, goroutine leaks, channel operations, mutex usage, context handling

---

## Executive Summary

**OVERALL STATUS:** 🔴 CRITICAL ISSUES FOUND

Found **7 concurrency issues** across 4 files:
- **2 CRITICAL** - Must fix immediately (goroutine leak, potential race condition)
- **3 HIGH** - Should fix soon (context propagation, check-then-act patterns)
- **2 MEDIUM** - Consider fixing (optimization opportunities)

---

## 1. CRITICAL ISSUES (MUST FIX)

### 🔴 CRITICAL-1: Goroutine Leak in Streaming (client.go:120-160)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/client.go:120-160`

**Severity:** CRITICAL - Goroutine leak on context cancellation

**Problem:**
```go
go func() {
    defer close(eventCh)
    // ... processing
    for stream.Next() {  // ← No context check!
        event := stream.Current()
        // Process event
        eventCh <- *streamEvent
    }
}()
```

The streaming goroutine does NOT check if `ctx` is cancelled inside the `for stream.Next()` loop. If the client context is cancelled while streaming is in progress, the goroutine will continue running until the stream naturally completes or errors.

**Impact:**
- Goroutine continues consuming CPU and memory after client disconnect
- Channel writes may block indefinitely if consumer stops reading
- Multiple leaked goroutines can accumulate over time
- Resource exhaustion under high load

**Fix Required:**
```go
go func() {
    defer close(eventCh)
    startTime := time.Now()
    var inputTokens, outputTokens int

    for stream.Next() {
        // CHECK CONTEXT BEFORE PROCESSING
        select {
        case <-ctx.Done():
            c.Log.Debug().Msg("Stream cancelled by context")
            return
        default:
        }

        event := stream.Current()
        // ... rest of processing

        // ALSO: Check context before channel send
        select {
        case <-ctx.Done():
            return
        case eventCh <- *streamEvent:
        }
    }
    // ... error handling
}()
```

---

### 🔴 CRITICAL-2: Check-Then-Act Race in GetCachedModels (models.go:166)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/models.go:166`

**Severity:** CRITICAL - Race condition in cache check

**Problem:**
```go
func GetCachedModels(ctx context.Context, apiKey string) ([]ModelInfo, error) {
    if globalModelCache.IsEmpty() || globalModelCache.IsStale() {  // ← CHECK
        return FetchModels(ctx, apiKey)                             // ← ACT
    }
    return globalModelCache.GetAll(), nil
}
```

Classic check-then-act race condition. Two goroutines can both see an empty/stale cache and BOTH call `FetchModels()`, resulting in:
1. Duplicate API calls (wasting quota and money)
2. Concurrent writes to `globalModelCache` via `FetchModels() → Update()`
3. While `Update()` uses proper locking, the duplicate fetches are wasteful

**Impact:**
- Wasted API calls during cache refresh
- Potential thundering herd if many goroutines check simultaneously
- Unnecessary load on Claude API

**Fix Required:**
Use double-checked locking or a sync.Once-style mechanism:

```go
var (
    globalModelCache = NewModelCache(15 * time.Minute)
    fetchMu          sync.Mutex  // Add mutex for fetch coordination
)

func GetCachedModels(ctx context.Context, apiKey string) ([]ModelInfo, error) {
    // Fast path: check without lock
    if !globalModelCache.IsEmpty() && !globalModelCache.IsStale() {
        return globalModelCache.GetAll(), nil
    }

    // Slow path: coordinate fetch
    fetchMu.Lock()
    defer fetchMu.Unlock()

    // Double-check after acquiring lock
    if !globalModelCache.IsEmpty() && !globalModelCache.IsStale() {
        return globalModelCache.GetAll(), nil
    }

    return FetchModels(ctx, apiKey)
}
```

---

## 2. HIGH SEVERITY ISSUES (SHOULD FIX)

### 🟡 HIGH-1: Context Not Passed to Stream Goroutine (client.go:114-162)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/client.go:114-162`

**Severity:** HIGH - Missing context propagation

**Problem:**
The `CreateMessageStream` function receives a `ctx` parameter but the spawned goroutine doesn't capture or use it for cancellation handling.

```go
func (c *Client) CreateMessageStream(ctx context.Context, req *CreateMessageRequest) (<-chan StreamEvent, error) {
    // ... setup
    stream := c.sdk.Messages.NewStreaming(ctx, sdkParams)  // ctx used here
    
    go func() {
        defer close(eventCh)
        for stream.Next() {  // ← No context check in loop
            // ...
        }
    }()
}
```

**Impact:**
- Cannot gracefully cancel streaming operations
- Goroutine continues even if caller context is cancelled
- Related to CRITICAL-1 above

**Fix Required:**
Capture context and check it in the loop (see CRITICAL-1 fix).

---

### 🟡 HIGH-2: Missing Context Check in queueAssistantResponse (client.go:469-478)

**File:** `/mnt/data/git/mautrix-claude/pkg/connector/client.go:469-478`

**Severity:** HIGH - Race between WaitGroup and early return

**Problem:**
```go
c.wg.Add(1)
go func() {
    defer c.wg.Done()
    
    // Check if already shutting down before queuing
    if c.ctx.Err() != nil {
        c.Connector.Log.Debug().Msg("Skipping assistant response queue due to shutdown")
        return  // ← Returns AFTER wg.Done() which is good
    }
    c.queueAssistantResponse(msg.Portal, responseContent, claudeMessageID, inputTokens+outputTokens)
}()
```

**Analysis:**
This code is actually CORRECT - it checks context and returns properly with defer. However, there's a window where:
1. `c.wg.Add(1)` is called
2. Context is cancelled before goroutine starts
3. Goroutine runs, sees cancelled context, returns immediately

This is safe but suboptimal. Better pattern:

```go
// Check BEFORE spawning goroutine
if c.ctx.Err() != nil {
    c.Connector.Log.Debug().Msg("Not queuing response - already shutting down")
    return &bridgev2.MatrixMessageResponse{...}, nil
}

c.wg.Add(1)
go func() {
    defer c.wg.Done()
    c.queueAssistantResponse(msg.Portal, responseContent, claudeMessageID, inputTokens+outputTokens)
}()
```

---

### 🟡 HIGH-3: Potential Double-Update Race in ModelCache.Update (models.go:111-123)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/models.go:111-123`

**Severity:** HIGH - Concurrent Update() calls not prevented

**Problem:**
```go
func (c *ModelCache) Update(models []ModelInfo) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Deep copy to avoid storing references to caller's data
    c.models = make([]ModelInfo, len(models))
    copy(c.models, models)
    c.byID = make(map[string]*ModelInfo, len(c.models))
    for i := range c.models {
        c.byID[c.models[i].ID] = &c.models[i]
    }
    c.lastFetch = time.Now()
}
```

While this function is properly locked, `FetchModels()` calls it:

```go
func FetchModels(ctx context.Context, apiKey string) ([]ModelInfo, error) {
    // ... fetch from API
    globalModelCache.Update(allModels)  // ← Update called
    return allModels, nil
}
```

Multiple concurrent `FetchModels()` calls (from CRITICAL-2) will result in multiple `Update()` calls with different data, leading to last-write-wins behavior.

**Impact:**
- Cache thrashing if multiple fetches return in different orders
- Inconsistent cache state during concurrent fetches
- Related to CRITICAL-2

**Fix:**
Fix CRITICAL-2 to prevent concurrent fetches. This issue resolves as a consequence.

---

## 3. MEDIUM SEVERITY ISSUES (CONSIDER FIXING)

### 🟢 MEDIUM-1: Check-Then-Act in getOrCreateModelMetrics (metrics.go:91-111)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/metrics.go:91-111`

**Severity:** MEDIUM - Safe but potentially inefficient pattern

**Problem:**
```go
func (m *Metrics) getOrCreateModelMetrics(model string) *ModelMetrics {
    m.modelMu.RLock()
    mm, ok := m.modelMetrics[model]
    m.modelMu.RUnlock()  // ← UNLOCK

    if ok {
        return mm
    }

    m.modelMu.Lock()     // ← LOCK AGAIN
    defer m.modelMu.Unlock()

    // Double-check after acquiring write lock
    if mm, ok = m.modelMetrics[model]; ok {
        return mm
    }

    mm = &ModelMetrics{}
    m.modelMetrics[model] = mm
    return mm
}
```

**Analysis:**
This is the **correct** double-checked locking pattern! However, it has a theoretical inefficiency:
- Multiple goroutines can simultaneously detect a missing model
- All acquire write lock sequentially
- Only first creates, others see it exists on double-check

**Impact:**
- Minor lock contention when new models are first seen
- Not a correctness issue - just efficiency
- Actual impact is likely negligible

**Recommendation:**
Keep as-is. This is a well-known, safe pattern. The inefficiency is minor and only occurs once per new model.

---

### 🟢 MEDIUM-2: No Buffered Channel Size Justification (client.go:117)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/client.go:117`

**Severity:** MEDIUM - Potential performance issue

**Problem:**
```go
eventCh := make(chan StreamEvent, 100)
```

Why buffer size 100?
- No comment explaining the choice
- Not configurable
- May be too large (wasted memory) or too small (blocking writes)

**Analysis:**
- Streaming events come in bursts (message_start, multiple content_block_delta, message_delta, message_stop)
- Buffer prevents goroutine blocking on fast event generation
- 100 seems arbitrary but probably reasonable

**Recommendation:**
1. Add a comment explaining the buffer size choice
2. Consider making it a constant with documentation
3. Monitor in production to see if it's ever filled

```go
const (
    // StreamEventBufferSize buffers streaming events to prevent goroutine blocking.
    // Size of 100 allows for ~20-30 content deltas without blocking, which is
    // sufficient for typical Claude responses.
    StreamEventBufferSize = 100
)

eventCh := make(chan StreamEvent, StreamEventBufferSize)
```

---

## 4. SAFE PATTERNS (NO ISSUES)

### ✅ GOOD: ConversationManager Mutex Usage (conversations.go)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/conversations.go`

**Analysis:**
All methods properly lock/unlock:
- `AddMessage*`: Write lock acquired correctly
- `GetMessages`: Read lock acquired, returns COPY (good!)
- `EditMessageByID`, `DeleteMessageByID`: Write lock acquired
- `Clear`, `TrimToTokenLimit`: Write lock acquired
- Read methods (`MessageCount`, `EstimatedTokens`, etc.): Read lock acquired

**Verdict:** Thread-safe, well-implemented.

---

### ✅ GOOD: ClaudeClient Conversation Map Access (client.go:282-298)

**File:** `/mnt/data/git/mautrix-claude/pkg/connector/client.go:282-298`

```go
func (c *ClaudeClient) getConversationManager(portal *bridgev2.Portal) *claudeapi.ConversationManager {
    c.convMu.Lock()
    defer c.convMu.Unlock()

    portalID := portal.PortalKey.ID

    if cm, ok := c.conversations[portalID]; ok {
        return cm
    }

    // Create new conversation manager
    maxTokens := claudeapi.GetModelMaxTokens(c.Connector.Config.GetDefaultModel())
    cm := claudeapi.NewConversationManager(maxTokens)
    c.conversations[portalID] = cm

    return cm
}
```

**Analysis:**
- Properly locked throughout
- No check-then-act race (lock held during entire operation)
- Returns pointer to manager (safe because ConversationManager itself is thread-safe)

**Verdict:** Correct implementation.

---

### ✅ GOOD: Metrics Using atomic.Int64 (metrics.go)

**File:** `/mnt/data/git/mautrix-claude/pkg/claudeapi/metrics.go`

**Analysis:**
All counter fields use `atomic.Int64`, which provides lock-free thread-safe increments:
```go
type Metrics struct {
    TotalRequests      atomic.Int64  // ✓ Atomic
    SuccessfulRequests atomic.Int64  // ✓ Atomic
    // ... etc
}

func (m *Metrics) RecordRequest(...) {
    m.TotalRequests.Add(1)  // ✓ Lock-free atomic operation
    // ...
}
```

Map access for `modelMetrics` is properly protected with RWMutex.

**Verdict:** Excellent use of atomic operations for counters.

---

### ✅ GOOD: Context Cancellation in Cleanup Loop (client.go:221-236)

**File:** `/mnt/data/git/mautrix-claude/pkg/connector/client.go:221-236`

```go
func (c *ClaudeClient) conversationCleanupLoop() {
    defer c.wg.Done()

    maxAge := time.Duration(c.Connector.Config.ConversationMaxAge) * time.Hour
    ticker := time.NewTicker(10 * time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-c.ctx.Done():
            return  // ✓ Proper context cancellation
        case <-ticker.C:
            c.cleanupExpiredConversations(maxAge)
        }
    }
}
```

**Analysis:**
- Proper select with context.Done() check
- WaitGroup correctly used (Done() deferred)
- Ticker properly stopped (deferred Stop())
- Clean exit on context cancellation

**Verdict:** Textbook goroutine lifecycle management.

---

### ✅ GOOD: RateLimiter Mutex Usage (client.go:120-183)

**File:** `/mnt/data/git/mautrix-claude/pkg/connector/client.go:120-183`

**Analysis:**
```go
func (r *RateLimiter) Allow() bool {
    if r == nil {
        return true  // ✓ Nil check prevents panic
    }

    r.mu.Lock()
    defer r.mu.Unlock()  // ✓ Proper unlock

    // ... modify r.requestTimes under lock
}
```

All RateLimiter methods:
1. Check for nil receiver
2. Lock mutex
3. Defer unlock
4. Perform operations on shared state

**Verdict:** Properly synchronized, nil-safe.

---

## 5. SUMMARY BY FILE

### `/mnt/data/git/mautrix-claude/pkg/claudeapi/client.go`
- 🔴 CRITICAL-1: Goroutine leak (line 120-160)
- 🟡 HIGH-1: Context not propagated (line 114-162)
- 🟢 MEDIUM-2: Buffer size not justified (line 117)

### `/mnt/data/git/mautrix-claude/pkg/claudeapi/models.go`
- 🔴 CRITICAL-2: Check-then-act race (line 166)
- 🟡 HIGH-3: Update() race consequence (line 111-123)
- ✅ ModelCache methods: Properly locked

### `/mnt/data/git/mautrix-claude/pkg/connector/client.go`
- 🟡 HIGH-2: Context check timing (line 469-478)
- ✅ getConversationManager: Properly locked
- ✅ conversationCleanupLoop: Excellent context handling
- ✅ RateLimiter: Properly synchronized

### `/mnt/data/git/mautrix-claude/pkg/claudeapi/conversations.go`
- ✅ All methods: Properly locked (RWMutex used correctly)
- ✅ GetMessages returns copy (prevents external mutation)

### `/mnt/data/git/mautrix-claude/pkg/claudeapi/metrics.go`
- 🟢 MEDIUM-1: Minor inefficiency in getOrCreateModelMetrics
- ✅ Atomic operations used correctly
- ✅ Map access properly protected

---

## 6. CONCURRENCY BEST PRACTICES OBSERVED

**Good Patterns Used:**
1. ✅ `sync.RWMutex` for read-heavy data structures (ModelCache, ConversationManager)
2. ✅ `atomic.Int64` for counters (Metrics)
3. ✅ `sync.WaitGroup` for goroutine lifecycle management
4. ✅ Context cancellation via `ctx.Done()` in cleanup loop
5. ✅ Deep copying in `ModelCache.GetAll()` to prevent external mutation
6. ✅ Deferred `Unlock()` to prevent lock leaks
7. ✅ Nil checks in RateLimiter methods

**Patterns to Avoid/Fix:**
1. ❌ Check-then-act without lock coordination (CRITICAL-2)
2. ❌ Goroutines without context cancellation checks (CRITICAL-1)
3. ❌ Missing context propagation to async operations (HIGH-1)

---

## 7. RECOMMENDED FIXES (PRIORITY ORDER)

### Priority 1: CRITICAL (Fix Immediately)

1. **Fix CRITICAL-1**: Add context cancellation to streaming goroutine
   - File: `pkg/claudeapi/client.go:120-160`
   - Add `select` with `ctx.Done()` in loop
   - Estimated effort: 10 minutes
   - Risk if not fixed: Goroutine leaks, resource exhaustion

2. **Fix CRITICAL-2**: Add fetch coordination to GetCachedModels
   - File: `pkg/claudeapi/models.go:166`
   - Add mutex for fetch coordination
   - Use double-checked locking pattern
   - Estimated effort: 15 minutes
   - Risk if not fixed: Wasted API calls, quota exhaustion

### Priority 2: HIGH (Fix Soon)

3. **Fix HIGH-1**: Propagate context to stream goroutine
   - File: `pkg/claudeapi/client.go:114-162`
   - Capture `ctx` and use in goroutine
   - Estimated effort: 5 minutes (part of CRITICAL-1 fix)

4. **Fix HIGH-2**: Check context before spawning goroutine
   - File: `pkg/connector/client.go:469-478`
   - Add early return if context cancelled
   - Estimated effort: 5 minutes

### Priority 3: MEDIUM (Consider)

5. **Fix MEDIUM-2**: Document channel buffer size
   - File: `pkg/claudeapi/client.go:117`
   - Add constant and comment
   - Estimated effort: 5 minutes

---

## 8. TESTING RECOMMENDATIONS

### Race Detector Testing
Run all tests with race detector enabled:
```bash
go test -race ./...
```

Expected to catch:
- CRITICAL-2: If tests have concurrent GetCachedModels calls
- Possibly CRITICAL-1 if tests cancel contexts during streaming

### Concurrency Stress Tests
Create stress tests for:

1. **Streaming cancellation:**
```go
func TestStreamCancellation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    client := claudeapi.NewClient(apiKey, log)
    
    stream, err := client.CreateMessageStream(ctx, req)
    require.NoError(t, err)
    
    // Read a few events
    <-stream
    <-stream
    
    // Cancel context
    cancel()
    
    // Should stop gracefully within 1 second
    time.Sleep(1 * time.Second)
    
    // Check goroutine count didn't increase
}
```

2. **Concurrent cache access:**
```go
func TestConcurrentCacheAccess(t *testing.T) {
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, _ = claudeapi.GetCachedModels(ctx, apiKey)
        }()
    }
    wg.Wait()
    
    // Verify only one FetchModels call was made
}
```

---

## 9. PRODUCTION MONITORING

Add monitoring for:

1. **Goroutine count:** Track over time to detect leaks
   ```go
   runtime.NumGoroutine()
   ```

2. **Channel buffer usage:** Log warnings if stream buffer fills
   ```go
   if len(eventCh) > StreamEventBufferSize * 0.9 {
       log.Warn().Msg("Stream buffer 90% full")
   }
   ```

3. **Context cancellation rate:** Track how often streams are cancelled
   ```go
   metrics.StreamCancellations.Add(1)
   ```

---

## 10. CONCLUSION

**Overall Code Quality:** Good foundation with some critical concurrency gaps.

**Strengths:**
- Proper use of sync.RWMutex, atomic operations, WaitGroup
- Good goroutine lifecycle management in most places
- Thread-safe data structures (ConversationManager, Metrics)

**Weaknesses:**
- Missing context cancellation in streaming goroutine (CRITICAL)
- Check-then-act race in cache access (CRITICAL)
- Some timing issues in context checks (HIGH)

**Recommendation:**
Fix CRITICAL-1 and CRITICAL-2 immediately before production deployment. These can cause resource leaks and wasted API quota. The HIGH and MEDIUM issues should be addressed in the next development cycle.

**Estimated Total Fix Time:** 1-2 hours including testing.

**Risk Assessment:**
- Current state: MEDIUM-HIGH risk for production
- After critical fixes: LOW risk for production

---

**Report Generated:** 2026-01-25
**Next Review:** After implementing critical fixes
