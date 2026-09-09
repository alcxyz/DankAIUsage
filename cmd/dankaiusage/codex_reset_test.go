package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCodexResetClient struct {
	mu          sync.Mutex
	reads       []codexRateLimitsResult
	readErrs    []error
	readCount   int
	readHook    func(int)
	consume     func(string, string) (string, error)
	consumeCall int
}

func (client *fakeCodexResetClient) ReadRateLimits() (codexRateLimitsResult, error) {
	client.mu.Lock()
	index := client.readCount
	client.readCount++
	hook := client.readHook
	if index < len(client.readErrs) && client.readErrs[index] != nil {
		err := client.readErrs[index]
		client.mu.Unlock()
		if hook != nil {
			hook(index)
		}
		return codexRateLimitsResult{}, err
	}
	if len(client.reads) == 0 {
		client.mu.Unlock()
		if hook != nil {
			hook(index)
		}
		return codexRateLimitsResult{}, nil
	}
	if index >= len(client.reads) {
		index = len(client.reads) - 1
	}
	result := client.reads[index]
	client.mu.Unlock()
	if hook != nil {
		hook(index)
	}
	return result, nil
}

func (client *fakeCodexResetClient) ConsumeReset(creditID, key string) (string, error) {
	client.mu.Lock()
	client.consumeCall++
	consume := client.consume
	client.mu.Unlock()
	if consume == nil {
		return "reset", nil
	}
	return consume(creditID, key)
}

func (*fakeCodexResetClient) Close() {}

func TestSelectCodexResetCreditUsesEarliestEligibleExpiry(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	credits := codexRateLimitResetCredits{AvailableCount: 5, Credits: []codexRateLimitResetCredit{
		{ID: "later", ResetType: "codexRateLimits", Status: "available", ExpiresAt: now.Add(2 * time.Hour).Unix()},
		{ID: "spark", ResetType: "sparkRateLimits", Status: "available", ExpiresAt: now.Add(time.Minute).Unix()},
		{ID: "expired", ResetType: "codexRateLimits", Status: "available", ExpiresAt: now.Add(-time.Minute).Unix()},
		{ID: "used", ResetType: "codexRateLimits", Status: "redeemed", ExpiresAt: now.Add(time.Minute).Unix()},
		{ID: "earlier", ResetType: "codexRateLimits", Status: "available", ExpiresAt: now.Add(time.Hour).Unix()},
	}}

	credit, ok := selectCodexResetCredit(credits, now)
	if !ok || credit.ID != "earlier" {
		t.Fatalf("selected credit = %+v, %v", credit, ok)
	}
	if _, ok := selectCodexResetCredit(codexRateLimitResetCredits{AvailableCount: 1}, now); ok {
		t.Fatal("availableCount without credit details must not arm")
	}
	if _, ok := selectCodexResetCredit(codexRateLimitResetCredits{
		AvailableCount: 0,
		Credits:        []codexRateLimitResetCredit{{ID: "stale", ResetType: "codexRateLimits", Status: "available", ExpiresAt: now.Add(time.Hour).Unix()}},
	}, now); ok {
		t.Fatal("availableCount zero must override a stale credit array")
	}
}

func TestShouldConsumeCodexResetPolicy(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	credit := codexRateLimitResetCredit{ExpiresAt: now.Add(time.Hour).Unix()}
	resetFar := now.Add(time.Hour).Unix()
	resetSoon := now.Add(5 * time.Minute).Unix()
	resetPast := now.Add(-time.Minute).Unix()
	weekly := int64(10080)

	tests := []struct {
		name   string
		limits codexRateLimitsResult
		credit codexRateLimitResetCredit
		want   bool
	}{
		{
			name: "threshold with distant natural reset",
			limits: generalLimits(codexRateLimitWindow{
				UsedPercent: 99, WindowDurationMins: &weekly, ResetsAt: &resetFar,
			}),
			credit: credit,
			want:   true,
		},
		{
			name: "wait for imminent natural reset",
			limits: generalLimits(codexRateLimitWindow{
				UsedPercent: 100, WindowDurationMins: &weekly, ResetsAt: &resetSoon,
			}),
			credit: credit,
		},
		{
			name: "unknown natural reset is not enough",
			limits: generalLimits(codexRateLimitWindow{
				UsedPercent: 100, WindowDurationMins: &weekly,
			}),
			credit: credit,
		},
		{
			name: "near-expiry credit with usage",
			limits: generalLimits(codexRateLimitWindow{
				UsedPercent: 1, WindowDurationMins: &weekly, ResetsAt: &resetSoon,
			}),
			credit: codexRateLimitResetCredit{ExpiresAt: now.Add(5 * time.Minute).Unix()},
			want:   true,
		},
		{
			name: "near-expiry credit without usage",
			limits: generalLimits(codexRateLimitWindow{
				UsedPercent: 0, WindowDurationMins: &weekly, ResetsAt: &resetSoon,
			}),
			credit: codexRateLimitResetCredit{ExpiresAt: now.Add(5 * time.Minute).Unix()},
		},
		{
			name: "near-expiry credit with stale usage window",
			limits: generalLimits(codexRateLimitWindow{
				UsedPercent: 50, WindowDurationMins: &weekly, ResetsAt: &resetPast,
			}),
			credit: codexRateLimitResetCredit{ExpiresAt: now.Add(5 * time.Minute).Unix()},
		},
		{
			name: "spark never triggers",
			limits: codexRateLimitsResult{RateLimitsByLimitID: map[string]codexRateLimitSnapshot{
				"gpt-5.3-codex-spark": {
					LimitID: "gpt-5.3-codex-spark",
					Primary: &codexRateLimitWindow{UsedPercent: 100, ResetsAt: &resetFar},
				},
			}},
			credit: credit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := shouldConsumeCodexReset(test.limits, test.credit, now)
			if got != test.want {
				t.Fatalf("shouldConsume = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCodexResetArmPinsCreditAndProtectsState(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/state/codex-reset.json"
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{{
		RateLimitResetCredits: codexRateLimitResetCredits{AvailableCount: 2, Credits: []codexRateLimitResetCredit{
			{ID: "credit-2", Title: "Later", ResetType: "codexRateLimits", Status: "available", ExpiresAt: now.Add(2 * time.Hour).Unix()},
			{ID: "credit-1", Title: "Soon", ResetType: "codexRateLimits", Status: "available", ExpiresAt: now.Add(time.Hour).Unix()},
		}},
	}}}
	status, err := runCodexResetAction("arm", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return client, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Armed || status.CreditID != "credit-1" || status.Title != "Soon" || status.CanArm {
		t.Fatalf("status = %+v", status)
	}
	state, err := loadCodexResetState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !isUUID(state.IdempotencyKey) {
		t.Fatalf("idempotency key = %q", state.IdempotencyKey)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state permissions = %o", got)
	}
	if info, err := os.Stat(path + ".lock"); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock permissions = %v, %v", info, err)
	}
}

func TestCodexResetArmRejectsMissingCreditDetails(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{{
		RateLimitResetCredits: codexRateLimitResetCredits{AvailableCount: 1},
	}}}
	status, err := runCodexResetAction("arm", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return client, nil
	}))
	if err == nil || status.Armed || status.State != "off" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func TestCodexResetArmReusesHealthyCachedLimits(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	deps := resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return nil, errors.New("cached arm must not open a provider client")
	})
	deps.RefreshPath = path + ".usage"
	want := triggeringLimits(armedResetState(now, "cached-credit"), now)
	want.RateLimitResetCredits.AvailableCountKnown = true
	if _, _, err := collectCachedCodexRateLimits(deps.RefreshPath, now, time.Hour, false, func() (codexRateLimitsResult, error) {
		return want, nil
	}); err != nil {
		t.Fatal(err)
	}
	deps.RefreshInterval = time.Hour
	status, err := runCodexResetAction("arm", deps)
	if err != nil || !status.Armed || status.CreditID != "cached-credit" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func TestCodexResetArmDoesNotReplaceExistingArmOnFailure(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	want := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, want) }); err != nil {
		t.Fatal(err)
	}
	opened := false
	status, err := runCodexResetAction("arm", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		opened = true
		return nil, errors.New("must not open")
	}))
	if err == nil || opened || !status.Armed || status.CreditID != want.CreditID || !status.StateKnown {
		t.Fatalf("status = %+v, err = %v, opened = %v", status, err, opened)
	}
	got, err := loadCodexResetState(path)
	if err != nil || got.IdempotencyKey != want.IdempotencyKey || !got.Armed {
		t.Fatalf("saved state = %+v, err = %v", got, err)
	}
}

func TestCodexResetDisarmRecoversUnreadableState(t *testing.T) {
	path := t.TempDir() + "/codex-reset.json"
	if err := os.WriteFile(path, []byte(`{"armed":`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := runCodexResetAction("disarm", resetDeps(path, time.Now(), nil))
	if err != nil || !status.StateKnown || status.Armed || status.State != "off" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if _, err := loadCodexResetState(path); err != nil {
		t.Fatalf("replacement state unreadable: %v", err)
	}
}

func TestCodexResetCheckDoesNotUseStaleOrSubstituteCredit(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	resetFar := now.Add(time.Hour).Unix()
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{{
		RateLimits: codexRateLimitSnapshot{LimitID: "codex", Primary: &codexRateLimitWindow{UsedPercent: 100, ResetsAt: &resetFar}},
		RateLimitResetCredits: codexRateLimitResetCredits{AvailableCount: 1, Credits: []codexRateLimitResetCredit{
			{ID: "different", ResetType: "codexRateLimits", Status: "available", ExpiresAt: now.Add(time.Hour).Unix()},
		}},
	}}}
	status, err := runCodexResetAction("check", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return client, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Armed || status.State != "waiting" || client.consumeCall != 0 {
		t.Fatalf("status = %+v, consume calls = %d", status, client.consumeCall)
	}
}

func TestCodexResetCheckWaitsRatherThanUsingCachedLimits(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	deps := resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return nil, errors.New("cooldown check must not open a provider client")
	})
	deps.RefreshPath = path + ".usage"
	if _, _, err := collectCachedCodexRateLimits(deps.RefreshPath, now, time.Hour, false, func() (codexRateLimitsResult, error) {
		return triggeringLimits(state, now), nil
	}); err != nil {
		t.Fatal(err)
	}
	deps.RefreshInterval = time.Hour
	status, err := runCodexResetAction("check", deps)
	if err != nil || !status.Armed || status.State != "waiting" || !strings.Contains(status.Message, "fresh") {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func TestCodexResetDoesNotConsumeWhenUsageInvalidationFails(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{triggeringLimits(state, now)}}
	deps := resetDeps(path, now, func(context.Context) (codexResetClient, error) { return client, nil })
	deps.InvalidateUsage = func(string, time.Time, time.Duration) error { return errors.New("disk failure") }
	status, err := runCodexResetAction("check", deps)
	if err == nil || status.Armed || status.State != "error" || client.consumeCall != 0 || status.HistoryError != "" {
		t.Fatalf("status = %+v, err = %v, consumes = %d", status, err, client.consumeCall)
	}
	if strings.Contains(status.Error, "disk failure") {
		t.Fatalf("private invalidation error leaked: %q", status.Error)
	}
}

func TestCodexResetDisarmsBeforeConsumeAndDefersRefresh(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	limits := triggeringLimits(state, now)
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{limits, limits}}
	client.consume = func(id, key string) (string, error) {
		saved, err := loadCodexResetState(path)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Armed || saved.State != "attempted" || saved.LastAttemptAt == "" || saved.Message != codexResetOutcomeUnknown {
			t.Fatalf("state at consume = %+v", saved)
		}
		if id != state.CreditID || key != state.IdempotencyKey {
			t.Fatalf("consume args = %q, %q", id, key)
		}
		return "reset", nil
	}
	status, err := runCodexResetAction("check", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return client, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if status.Armed || status.State != "completed" || status.Outcome != "reset" || status.Refreshed {
		t.Fatalf("status = %+v", status)
	}
	if client.readCount != 1 || client.consumeCall != 1 {
		t.Fatalf("reads = %d, consumes = %d", client.readCount, client.consumeCall)
	}
}

func TestCodexResetDoesNotConsumeWhenDisarmCannotBePersisted(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	dir := t.TempDir()
	path := dir + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{triggeringLimits(state, now)}}
	client.readHook = func(index int) {
		if index == 0 {
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Error(err)
			}
		}
	}
	defer os.Chmod(dir, 0o700) //nolint:errcheck
	status, err := runCodexResetAction("check", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return client, nil
	}))
	_ = os.Chmod(dir, 0o700)
	if err == nil {
		t.Skip("filesystem permissions did not reject the state write")
	}
	if client.consumeCall != 0 || !status.Armed || !status.StateKnown {
		t.Fatalf("status = %+v, consume calls = %d", status, client.consumeCall)
	}
	saved, loadErr := loadCodexResetState(path)
	if loadErr != nil || !saved.Armed {
		t.Fatalf("saved state = %+v, err = %v", saved, loadErr)
	}
}

func TestCodexResetConsumeErrorIsOneShot(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexResetClient{
		reads: []codexRateLimitsResult{triggeringLimits(state, now)},
		consume: func(string, string) (string, error) {
			return "", context.DeadlineExceeded
		},
	}
	deps := resetDeps(path, now, func(context.Context) (codexResetClient, error) { return client, nil })
	status, err := runCodexResetAction("check", deps)
	if err == nil || status.Armed || status.State != "error" || !strings.Contains(status.Message, "unknown") {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if status.Error != "Codex app-server reset request timed out" {
		t.Fatalf("safe error = %q", status.Error)
	}
	status, err = runCodexResetAction("check", deps)
	if err != nil || status.Armed || client.consumeCall != 1 {
		t.Fatalf("second check status = %+v, err = %v, consumes = %d", status, err, client.consumeCall)
	}
}

func TestCodexResetProtocolOutcomesAreOneShot(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	for _, test := range []struct {
		outcome string
		wantErr bool
	}{
		{outcome: "reset"},
		{outcome: "alreadyRedeemed"},
		{outcome: "nothingToReset", wantErr: true},
		{outcome: "noCredit", wantErr: true},
	} {
		t.Run(test.outcome, func(t *testing.T) {
			path := t.TempDir() + "/codex-reset.json"
			state := armedResetState(now, "pinned")
			if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
				t.Fatal(err)
			}
			client := &fakeCodexResetClient{reads: []codexRateLimitsResult{triggeringLimits(state, now), triggeringLimits(state, now)}}
			client.consume = func(string, string) (string, error) { return test.outcome, nil }
			status, err := runCodexResetAction("check", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
				return client, nil
			}))
			if (err != nil) != test.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, test.wantErr)
			}
			if status.Armed || status.State != "completed" || status.Outcome != test.outcome || status.Refreshed || client.consumeCall != 1 {
				t.Fatalf("status = %+v, consume calls = %d", status, client.consumeCall)
			}
		})
	}
}

func TestCodexResetKnownOutcomeStaysOffWithDeferredRefresh(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexResetClient{
		reads:    []codexRateLimitsResult{triggeringLimits(state, now)},
		readErrs: []error{nil, errors.New("private backend failure body")},
	}
	status, err := runCodexResetAction("check", resetDeps(path, now, func(context.Context) (codexResetClient, error) {
		return client, nil
	}))
	if err != nil || status.Armed || status.Outcome != "reset" || status.Refreshed || client.consumeCall != 1 || client.readCount != 1 {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if status.Error != "" {
		t.Fatalf("unexpected deferred refresh result = %+v", status)
	}
}

func TestSafeCodexResetErrorDoesNotExposeServerBody(t *testing.T) {
	err := codexRPCError{Code: -32601}
	if got := safeCodexResetError(err); got != codexResetProtocolFailure || strings.Contains(got, "32601") {
		t.Fatalf("safe error = %q", got)
	}
}

func TestCodexResetUsesClockAfterFreshRead(t *testing.T) {
	before := mustParseTime(t, "2026-09-08T12:00:00Z")
	after := before.Add(2 * time.Hour)
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(before, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{triggeringLimits(state, before)}}
	var clockCalls int
	deps := resetDeps(path, before, func(context.Context) (codexResetClient, error) { return client, nil })
	deps.Now = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return before
		}
		return after
	}
	status, err := runCodexResetAction("check", deps)
	if err != nil {
		t.Fatal(err)
	}
	if client.consumeCall != 0 || !status.Armed || status.State != "waiting" {
		t.Fatalf("status = %+v, consume calls = %d", status, client.consumeCall)
	}
}

func TestCodexResetConcurrentChecksConsumeExactlyOnce(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := t.TempDir() + "/codex-reset.json"
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(path, func() error { return saveCodexResetState(path, state) }); err != nil {
		t.Fatal(err)
	}
	var consumes atomic.Int32
	open := func(context.Context) (codexResetClient, error) {
		client := &fakeCodexResetClient{reads: []codexRateLimitsResult{triggeringLimits(state, now), triggeringLimits(state, now)}}
		client.consume = func(string, string) (string, error) {
			consumes.Add(1)
			time.Sleep(20 * time.Millisecond)
			return "reset", nil
		}
		return client, nil
	}
	deps := resetDeps(path, now, open)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := runCodexResetAction("check", deps)
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := consumes.Load(); got != 1 {
		t.Fatalf("consume calls = %d", got)
	}
}

func TestCodexResetUnreadableStateIsUnknown(t *testing.T) {
	path := t.TempDir() + "/codex-reset.json"
	if err := os.WriteFile(path, []byte(`{"armed":`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := runCodexResetAction("status", resetDeps(path, time.Now(), nil))
	if err == nil || status.StateKnown || status.State != "error" || status.CanArm || !strings.Contains(status.Message, "unknown") {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func TestCodexResetLockAcquisitionIsBounded(t *testing.T) {
	path := t.TempDir() + "/codex-reset.json"
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withCodexResetLock(path, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	started := time.Now()
	err := withCodexResetLockTimeout(path, 75*time.Millisecond, func() error {
		t.Fatal("contended lock callback ran")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("lock error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lock wait was not bounded: %v", elapsed)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCodexAppServerClientProtocolUsesPinnedCreditAndRefresh(t *testing.T) {
	if os.Getenv("DANKAIUSAGE_CODEX_RESET_HELPER") == "1" {
		runDummyCodexAppServer(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	t.Setenv("DANKAIUSAGE_CODEX_RESET_HELPER", "1")
	client, err := newCodexAppServerClient(ctx, os.Args[0], "-test.run=TestCodexAppServerClientProtocolUsesPinnedCreditAndRefresh")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.ReadRateLimits(); err != nil {
		t.Fatal(err)
	}
	outcome, err := client.ConsumeReset("credit-pinned", "123e4567-e89b-42d3-a456-426614174000")
	if err != nil || outcome != "reset" {
		t.Fatalf("outcome = %q, err = %v", outcome, err)
	}
	if _, err := client.ReadRateLimits(); err != nil {
		t.Fatal(err)
	}
}

func runDummyCodexAppServer(t *testing.T) {
	scanner := bufio.NewScanner(os.Stdin)
	readCount := 0
	for scanner.Scan() {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
		case "account/rateLimits/read":
			readCount++
			result = map[string]any{"rateLimits": map[string]any{"limitId": "codex"}}
		case "account/rateLimitResetCredit/consume":
			if request.Params["creditId"] != "credit-pinned" || request.Params["idempotencyKey"] != "123e4567-e89b-42d3-a456-426614174000" {
				os.Exit(3)
			}
			result = map[string]string{"outcome": "reset"}
		default:
			os.Exit(4)
		}
		response, _ := json.Marshal(map[string]any{"id": request.ID, "result": result})
		_, _ = os.Stdout.Write(append(response, '\n'))
		if readCount == 2 {
			return
		}
	}
}

func generalLimits(window codexRateLimitWindow) codexRateLimitsResult {
	return codexRateLimitsResult{RateLimits: codexRateLimitSnapshot{
		LimitID: "codex",
		Primary: &window,
	}}
}

func armedResetState(now time.Time, creditID string) codexResetState {
	return codexResetState{
		Version:        codexResetStateVersion,
		Armed:          true,
		CreditID:       creditID,
		Title:          "Earned reset",
		ResetType:      "codexRateLimits",
		ExpiresAt:      now.Add(time.Hour).UTC().Format(time.RFC3339),
		IdempotencyKey: "123e4567-e89b-42d3-a456-426614174000",
		State:          "armed",
		Message:        "Automatic Codex reset is armed",
	}
}

func triggeringLimits(state codexResetState, now time.Time) codexRateLimitsResult {
	resetAt := now.Add(time.Hour).Unix()
	expiresAt, _ := time.Parse(time.RFC3339, state.ExpiresAt)
	limits := generalLimits(codexRateLimitWindow{UsedPercent: 99, ResetsAt: &resetAt})
	limits.RateLimitResetCredits.Credits = []codexRateLimitResetCredit{{
		ID: state.CreditID, ResetType: state.ResetType, Status: "available", ExpiresAt: expiresAt.Unix(),
	}}
	limits.RateLimitResetCredits.AvailableCount = 1
	return limits
}

func resetDeps(path string, now time.Time, open func(context.Context) (codexResetClient, error)) codexResetDeps {
	if open == nil {
		open = func(context.Context) (codexResetClient, error) { return nil, errors.New("not used") }
	}
	return codexResetDeps{Now: func() time.Time { return now }, StatePath: path, OpenClient: open, Timeout: time.Second}
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
