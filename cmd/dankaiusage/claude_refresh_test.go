package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Never contacts a server or uses the user's CLI credentials.
type claudeRefreshTransport struct {
	calls      atomic.Int32
	status     int
	retryAfter string
}

func (transport *claudeRefreshTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return &http.Response{
		StatusCode: transport.status,
		Header:     http.Header{"Retry-After": []string{transport.retryAfter}},
		Body:       io.NopCloser(strings.NewReader(`{"five_hour":{"utilization":10,"resets_at":"2026-09-09T18:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2026-09-15T00:00:00Z"}}`)),
		Request:    request,
	}, nil
}

func setupClaudeRefreshTest(t *testing.T) (*claudeRefreshTransport, time.Time) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("PATH", t.TempDir())
	// Synthetic, nonfunctional fixture; no production secret is read.
	if err := os.WriteFile(filepath.Join(config, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"synthetic-test-only"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	transport := &claudeRefreshTransport{status: http.StatusOK}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	return transport, mustParseTime(t, "2026-09-09T12:00:00Z")
}

func TestClaudeRefreshMinimumAndLongInterval(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, time.Hour} {
		t.Run(interval.String(), func(t *testing.T) {
			transport, now := setupClaudeRefreshTest(t)
			session, _, _, _, _, first, err := collectClaudeOAuthLimitsWithPolicy(now, interval, false)
			if err != nil || !session.Known || first.Cached {
				t.Fatalf("initial fetch: known=%v info=%+v error=%v", session.Known, first, err)
			}
			elapsed := 2 * time.Minute
			callerInterval := time.Second
			if interval == time.Hour {
				elapsed = 40 * time.Minute
				callerInterval = usageRefreshDefaultInterval
			}
			session, _, _, _, _, cached, err := collectClaudeOAuthLimitsWithPolicy(now.Add(elapsed), callerInterval, false)
			if err != nil || !session.Known || !cached.Cached || !cached.FetchedAt.Equal(now) || transport.calls.Load() != 1 {
				t.Fatalf("cache read: known=%v info=%+v error=%v calls=%d", session.Known, cached, err, transport.calls.Load())
			}
			if _, _, _, _, _, _, err := collectClaudeOAuthLimitsWithPolicy(now.Add(elapsed), callerInterval, true); !errors.Is(err, errUsageRefreshCoolingDown) {
				t.Fatalf("fresh-only read bypassed cooldown: %v", err)
			}
			if transport.calls.Load() != 1 {
				t.Fatal("fresh-only read made an extra request")
			}
		})
	}
}

func TestClaudeUsageRetryDelay(t *testing.T) {
	now := mustParseTime(t, "2026-09-09T12:00:00Z")
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"", 15 * time.Minute},
		{"invalid", 15 * time.Minute},
		{"-1", 15 * time.Minute},
		{"60", 15 * time.Minute},
		{" 1800 ", 30 * time.Minute},
		{now.Add(time.Hour).Format(http.TimeFormat), time.Hour},
		{now.Add(-time.Hour).Format(http.TimeFormat), 15 * time.Minute},
		{"9223372036854775807", time.Duration(1<<63 - 1)},
	} {
		if got := claudeUsageRetryDelay(test.value, now); got != test.want {
			t.Errorf("Retry-After %q: got %v, want %v", test.value, got, test.want)
		}
	}
}

func TestClaudeRefreshRetryAfterSurvivesOtherCallers(t *testing.T) {
	transport, now := setupClaudeRefreshTest(t)
	transport.status = http.StatusTooManyRequests
	transport.retryAfter = "1200"
	if _, _, _, _, _, _, err := collectClaudeOAuthLimitsWithPolicy(now, 3*time.Minute, false); err == nil {
		t.Fatal("expected rate-limit failure")
	}
	if _, _, _, _, _, info, err := collectClaudeOAuthLimitsWithPolicy(now.Add(19*time.Minute), time.Second, false); !errors.Is(err, errUsageRefreshCoolingDown) || !info.NextAttemptAt.Equal(now.Add(20*time.Minute)) {
		t.Fatalf("backoff not retained: info=%+v error=%v", info, err)
	}
	if transport.calls.Load() != 1 {
		t.Fatal("another caller bypassed Retry-After")
	}
	if _, _, _, _, _, _, err := collectClaudeOAuthLimitsWithPolicy(now.Add(20*time.Minute), 3*time.Minute, false); err == nil {
		t.Fatal("fake server should still report rate limiting")
	}
	if transport.calls.Load() != 2 {
		t.Fatal("eligible retry did not contact fake transport")
	}
}

func TestClaudeRefreshConcurrentCallersShareRequest(t *testing.T) {
	transport, now := setupClaudeRefreshTest(t)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			session, _, _, _, _, _, err := collectClaudeOAuthLimitsWithPolicy(now, 5*time.Minute, false)
			if err != nil || !session.Known {
				t.Errorf("concurrent cache read failed: known=%v error=%v", session.Known, err)
			}
		}()
	}
	group.Wait()
	if transport.calls.Load() != 1 {
		t.Fatalf("requests=%d, want one", transport.calls.Load())
	}
}

func TestClaudeRefreshUsesTimeSampledAfterLock(t *testing.T) {
	transport, startedAt := setupClaudeRefreshTest(t)
	lockedAt := startedAt.Add(time.Millisecond)
	path := claudeOAuthUsageCachePath()
	if err := saveClaudeOAuthUsageCache(path, claudeOAuthUsageCache{
		FetchedAt: lockedAt.UTC().Format(time.RFC3339Nano),
		Body:      json.RawMessage(`{"five_hour":{"utilization":10,"resets_at":"2026-09-09T18:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2026-09-15T00:00:00Z"}}`),
	}); err != nil {
		t.Fatal(err)
	}

	session, _, _, _, _, info, err := collectClaudeOAuthLimitsWithClock(clockAssertedUnderUsageRefreshLock(t, path, lockedAt), 5*time.Minute, false)
	if err != nil || !session.Known || !info.Cached || transport.calls.Load() != 0 {
		t.Fatalf("collect: known=%v info=%+v error=%v calls=%d", session.Known, info, err, transport.calls.Load())
	}

	_, _, _, _, _, _, err = collectClaudeOAuthLimitsWithClock(fixedUsageRefreshClock(startedAt), 5*time.Minute, false)
	if !errors.Is(err, errUsageRefreshTimestamp) || !errors.Is(err, errUsageRefreshState) {
		t.Fatalf("genuinely future cache error = %v, want timestamp and state sentinels", err)
	}
}

func TestClaudeRefreshInvalidationPreservesLongCooldown(t *testing.T) {
	transport, now := setupClaudeRefreshTest(t)
	if _, _, _, _, _, _, err := collectClaudeOAuthLimitsWithPolicy(now, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if err := invalidateClaudeUsageCache(claudeOAuthUsageCachePath(), now.Add(time.Minute), 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, info, err := collectClaudeOAuthLimitsWithPolicy(now.Add(40*time.Minute), 3*time.Minute, false); !errors.Is(err, errUsageRefreshCoolingDown) || !info.Pending {
		t.Fatalf("invalidation shortened cooldown: info=%+v error=%v", info, err)
	}
	if transport.calls.Load() != 1 {
		t.Fatal("invalidation allowed an early fetch")
	}
}

func TestClaudeRefreshCorruptCacheFailsClosed(t *testing.T) {
	for _, fixture := range []string{
		`null`, `{}`, `{"body":`,
		`{"fetchedAt":"bad-time"}`,
		`{"fetchedAt":"2026-09-09T11:59:00Z","body":{"unsupported":true}}`,
	} {
		t.Run(fixture, func(t *testing.T) {
			transport, now := setupClaudeRefreshTest(t)
			path := claudeOAuthUsageCachePath()
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, _, _, _, _, err := collectClaudeOAuthLimitsWithPolicy(now, 5*time.Minute, false); err == nil {
				t.Fatal("invalid cache was accepted")
			}
			if transport.calls.Load() != 0 {
				t.Fatal("invalid cache bypassed cooldown")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != fixture {
				t.Fatal("invalid cache was overwritten")
			}
		})
	}
}
