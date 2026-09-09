package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUsageRefreshCachedSnapshotsEnterHistoryOnlyOnce(t *testing.T) {
	for _, providerID := range []string{"codex", "claude"} {
		t.Run(providerID, func(t *testing.T) {
			now := mustParseTime(t, "2026-09-09T12:00:00Z")
			state := defaultUsageHistoryState()
			provider := historyProvider(providerID, "general-weekly", "Weekly", 60, now.Add(24*time.Hour))
			provider.historyObservedAt = now
			observeProviderHistory(&state, provider, now)
			provider.QuotaBuckets[0].Allowance.PercentUsed = 20
			provider.historyObservedAt = now.Add(5 * time.Minute)
			mergeUsageRefreshMeta(provider.Meta, usageRefreshInfo{Cached: true, FetchedAt: provider.historyObservedAt})
			observeProviderHistory(&state, provider, now.Add(5*time.Minute+time.Second))
			if len(state.Events) != 1 {
				t.Fatalf("first sight of a fresh shared snapshot produced %d events, want one", len(state.Events))
			}
			before, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			observeProviderHistory(&state, provider, now.Add(6*time.Minute))
			after, err := json.Marshal(state)
			if err != nil || string(before) != string(after) {
				t.Fatal("reusing the same snapshot advanced history")
			}
			provider.historyObservedAt = now.Add(7 * time.Minute)
			provider.QuotaBuckets[0].Allowance.PercentUsed = 5
			mergeUsageRefreshMeta(provider.Meta, usageRefreshInfo{Cached: true, Stale: true, FetchedAt: provider.historyObservedAt})
			observeProviderHistory(&state, provider, now.Add(8*time.Minute))
			if len(state.Events) != 1 {
				t.Fatal("stale cache data generated a reset event")
			}
		})
	}
}

func TestNormalizeUsageRefreshInterval(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "zero uses default", in: 0, want: usageRefreshDefaultInterval},
		{name: "negative clamps to minimum", in: -time.Second, want: usageRefreshMinInterval},
		{name: "under minimum", in: usageRefreshMinInterval - time.Second, want: usageRefreshMinInterval},
		{name: "minimum", in: usageRefreshMinInterval, want: usageRefreshMinInterval},
		{name: "selected value", in: 17 * time.Minute, want: 17 * time.Minute},
		{name: "maximum", in: usageRefreshMaxInterval, want: usageRefreshMaxInterval},
		{name: "over maximum", in: usageRefreshMaxInterval + time.Second, want: usageRefreshMaxInterval},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeUsageRefreshInterval(test.in); got != test.want {
				t.Fatalf("normalizeUsageRefreshInterval(%s) = %s, want %s", test.in, got, test.want)
			}
		})
	}
}

func TestUsageRefreshIntervalSecondsClampsBeforeDurationOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		seconds int
		want    time.Duration
	}{
		{seconds: 0, want: usageRefreshDefaultInterval},
		{seconds: -1, want: usageRefreshMinInterval},
		{seconds: 1, want: usageRefreshMinInterval},
		{seconds: int(usageRefreshMaxInterval/time.Second) + 1, want: usageRefreshMaxInterval},
		{seconds: maxInt, want: usageRefreshMaxInterval},
	}
	for _, test := range tests {
		if got := usageRefreshIntervalSeconds(test.seconds); got != test.want {
			t.Errorf("usageRefreshIntervalSeconds(%d) = %s, want %s", test.seconds, got, test.want)
		}
	}
}

func TestCollectCachedCodexRateLimitsReusesCache(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	path := filepath.Join(t.TempDir(), "state", "codex-usage.json")
	want := usageRefreshTestResult("first")
	var fetches atomic.Int32
	fetch := func() (codexRateLimitsResult, error) {
		fetches.Add(1)
		return want, nil
	}

	got, info, err := collectCachedCodexRateLimits(path, now, 10*time.Minute, false, fetch)
	if err != nil || got.RateLimits.LimitID != "first" || info.Cached || !info.FetchedAt.Equal(now) {
		t.Fatalf("first collect = %+v, info = %+v, err = %v", got, info, err)
	}
	got, info, err = collectCachedCodexRateLimits(path, now.Add(9*time.Minute), 10*time.Minute, false, fetch)
	if err != nil || got.RateLimits.LimitID != "first" || !info.Cached || !info.FetchedAt.Equal(now) {
		t.Fatalf("cached collect = %+v, info = %+v, err = %v", got, info, err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
}

func TestCollectCachedCodexRateLimitsPreservesResetCreditCountPresence(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	tests := []struct {
		name  string
		count int
		known bool
	}{
		{name: "unknown"},
		{name: "known zero", known: true},
		{name: "known positive", count: 2, known: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex-usage.json")
			fetches := 0
			fetch := func() (codexRateLimitsResult, error) {
				fetches++
				return codexRateLimitsResult{RateLimitResetCredits: codexRateLimitResetCredits{
					AvailableCount:      test.count,
					AvailableCountKnown: test.known,
				}}, nil
			}
			if _, _, err := collectCachedCodexRateLimits(path, now, 5*time.Minute, false, fetch); err != nil {
				t.Fatal(err)
			}
			got, info, err := collectCachedCodexRateLimits(path, now.Add(time.Minute), 5*time.Minute, false, fetch)
			if err != nil || !info.Cached {
				t.Fatalf("cached collect info = %+v, err = %v", info, err)
			}
			credits := got.RateLimitResetCredits
			if credits.AvailableCount != test.count || credits.AvailableCountKnown != test.known {
				t.Fatalf("cached credits = %+v, want count %d known %v", credits, test.count, test.known)
			}
			if fetches != 1 {
				t.Fatalf("fetches = %d, want 1", fetches)
			}
		})
	}
}

func TestCollectCachedCodexRateLimitsFreshOnlyHonorsCooldown(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	path := filepath.Join(t.TempDir(), "codex-usage.json")
	var fetches atomic.Int32
	fetch := func() (codexRateLimitsResult, error) {
		fetches.Add(1)
		return usageRefreshTestResult("fresh"), nil
	}
	if _, _, err := collectCachedCodexRateLimits(path, now, 5*time.Minute, false, fetch); err != nil {
		t.Fatal(err)
	}

	got, info, err := collectCachedCodexRateLimits(path, now.Add(time.Minute), 5*time.Minute, true, fetch)
	if !errors.Is(err, errUsageRefreshCoolingDown) {
		t.Fatalf("fresh-only error = %v, want cooldown", err)
	}
	if got.RateLimits.LimitID != "" || info.Cached {
		t.Fatalf("fresh-only returned cached data: %+v, info = %+v", got, info)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
}

func TestCollectCachedCodexRateLimitsConcurrentCallersFetchOnce(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	path := filepath.Join(t.TempDir(), "codex-usage.json")
	const callers = 12
	ready := make(chan struct{}, callers)
	start := make(chan struct{})
	var fetches atomic.Int32
	fetch := func() (codexRateLimitsResult, error) {
		fetches.Add(1)
		return usageRefreshTestResult("shared"), nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-start
			result, _, err := collectCachedCodexRateLimits(path, now, 5*time.Minute, false, fetch)
			if err == nil && result.RateLimits.LimitID != "shared" {
				err = errors.New("caller did not receive shared result")
			}
			errs <- err
		}()
	}
	for range callers {
		<-ready
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
}

func TestCollectCachedCodexRateLimitsPersistsReservationOnFetchError(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	path := filepath.Join(t.TempDir(), "codex-usage.json")
	wantErr := errors.New("provider unavailable")
	var fetches atomic.Int32
	var reservationSeen bool
	var reservationReadErr error
	fetch := func() (codexRateLimitsResult, error) {
		fetches.Add(1)
		cache, err := loadCodexUsageCache(path)
		if err != nil {
			reservationReadErr = err
		} else {
			nextAttempt, parseErr := time.Parse(time.RFC3339Nano, cache.NextAttemptAt)
			reservationReadErr = parseErr
			reservationSeen = parseErr == nil && nextAttempt.Equal(now.Add(15*time.Minute))
		}
		return codexRateLimitsResult{}, wantErr
	}

	if _, _, err := collectCachedCodexRateLimits(path, now, 15*time.Minute, false, fetch); !errors.Is(err, wantErr) {
		t.Fatalf("first error = %v, want provider error", err)
	}
	if reservationReadErr != nil || !reservationSeen {
		t.Fatalf("reservation was not durable before fetch: seen = %v, err = %v", reservationSeen, reservationReadErr)
	}
	cache, err := loadCodexUsageCache(path)
	if err != nil {
		t.Fatal(err)
	}
	nextAttempt, err := time.Parse(time.RFC3339Nano, cache.NextAttemptAt)
	if err != nil || !nextAttempt.Equal(now.Add(15*time.Minute)) || cache.LastError == "" || cache.LastError == wantErr.Error() {
		t.Fatalf("saved failure reservation = %+v, parsed next = %v, err = %v", cache, nextAttempt, err)
	}
	if _, _, err := collectCachedCodexRateLimits(path, now.Add(time.Minute), 15*time.Minute, false, fetch); !errors.Is(err, errUsageRefreshCoolingDown) {
		t.Fatalf("retry error = %v, want cooldown", err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
}

func TestInvalidateCodexUsageCacheHidesBodyUntilDue(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	path := filepath.Join(t.TempDir(), "codex-usage.json")
	var fetches atomic.Int32
	fetch := func() (codexRateLimitsResult, error) {
		fetches.Add(1)
		return usageRefreshTestResult("result"), nil
	}
	if _, _, err := collectCachedCodexRateLimits(path, now, 10*time.Minute, false, fetch); err != nil {
		t.Fatal(err)
	}
	if err := invalidateCodexUsageCache(path, now.Add(time.Minute), 10*time.Minute); err != nil {
		t.Fatal(err)
	}

	got, info, err := collectCachedCodexRateLimits(path, now.Add(9*time.Minute), 10*time.Minute, false, fetch)
	if !errors.Is(err, errUsageRefreshCoolingDown) || got.RateLimits.LimitID != "" || !info.Pending || info.Cached {
		t.Fatalf("invalidated collect = %+v, info = %+v, err = %v", got, info, err)
	}
	got, info, err = collectCachedCodexRateLimits(path, now.Add(10*time.Minute), 10*time.Minute, false, fetch)
	if err != nil || got.RateLimits.LimitID != "result" || info.Pending || info.Cached {
		t.Fatalf("due collect = %+v, info = %+v, err = %v", got, info, err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("fetches = %d, want 2", got)
	}
}

func TestUsageRefreshStatePermissions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested", "state")
	path := filepath.Join(dir, "codex-usage.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := collectCachedCodexRateLimits(path, time.Now(), 5*time.Minute, false, func() (codexRateLimitsResult, error) {
		return usageRefreshTestResult("protected"), nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		want os.FileMode
	}{
		{path: dir, want: 0o700},
		{path: path, want: 0o600},
		{path: path + ".lock", want: 0o600},
	} {
		info, err := os.Stat(test.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != test.want {
			t.Errorf("%s permissions = %o, want %o", test.path, got, test.want)
		}
	}
}

func TestCodexInvalidationPreservesLongCooldown(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	path := filepath.Join(t.TempDir(), "codex-usage.json")
	fetches := 0
	fetch := func() (codexRateLimitsResult, error) {
		fetches++
		return usageRefreshTestResult("result"), nil
	}
	if _, _, err := collectCachedCodexRateLimits(path, now, time.Hour, false, fetch); err != nil {
		t.Fatal(err)
	}
	if err := invalidateCodexUsageCache(path, now.Add(time.Minute), 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, info, err := collectCachedCodexRateLimits(path, now.Add(40*time.Minute), 3*time.Minute, false, fetch); !errors.Is(err, errUsageRefreshCoolingDown) || !info.Pending {
		t.Fatalf("invalidation shortened cooldown: info=%+v error=%v", info, err)
	}
	if fetches != 1 {
		t.Fatal("invalidation allowed an early fetch")
	}
}

func TestCollectCachedCodexRateLimitsFailsClosedForInvalidCache(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	tests := []struct {
		name string
		data string
	}{
		{name: "corrupt JSON", data: `{"version":1,"result":`},
		{name: "unsupported version", data: `{"version":2}`},
		{name: "invalid fetched timestamp", data: `{"version":1,"fetchedAt":"not-a-time"}`},
		{name: "future fetched timestamp", data: `{"version":1,"fetchedAt":"2026-09-09T13:00:00Z","result":{"rateLimits":{"limitId":"future"}}}`},
		{name: "invalid next-attempt timestamp", data: `{"version":1,"nextAttemptAt":"not-a-time"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex-usage.json")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			called := false
			got, info, err := collectCachedCodexRateLimits(path, now, 5*time.Minute, false, func() (codexRateLimitsResult, error) {
				called = true
				return usageRefreshTestResult("unexpected"), nil
			})
			if err == nil || called || got.RateLimits.LimitID != "" || info.Cached {
				t.Fatalf("collect = %+v, info = %+v, err = %v, fetch called = %v", got, info, err, called)
			}
		})
	}
}

func TestCollectCachedCodexRateLimitsRetainsSelectedLongInterval(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	path := filepath.Join(t.TempDir(), "codex-usage.json")
	var fetches atomic.Int32
	fetch := func() (codexRateLimitsResult, error) {
		id := "first"
		if fetches.Add(1) > 1 {
			id = "second"
		}
		return usageRefreshTestResult(id), nil
	}
	if _, _, err := collectCachedCodexRateLimits(path, now, time.Hour, false, fetch); err != nil {
		t.Fatal(err)
	}
	got, info, err := collectCachedCodexRateLimits(path, now.Add(6*time.Minute), usageRefreshDefaultInterval, false, fetch)
	if err != nil || got.RateLimits.LimitID != "first" || !info.Cached {
		t.Fatalf("shorter caller collect = %+v, info = %+v, err = %v", got, info, err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
	got, info, err = collectCachedCodexRateLimits(path, now.Add(time.Hour), usageRefreshDefaultInterval, false, fetch)
	if err != nil || got.RateLimits.LimitID != "second" || info.Cached {
		t.Fatalf("deadline collect = %+v, info = %+v, err = %v", got, info, err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("fetches at deadline = %d, want 2", got)
	}
}

func usageRefreshTestResult(id string) codexRateLimitsResult {
	return codexRateLimitsResult{RateLimits: codexRateLimitSnapshot{LimitID: id}}
}
