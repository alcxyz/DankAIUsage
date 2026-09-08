package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUsageHistoryBaselineRepeatedMissingStaleAndOutOfOrder(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "state", "usage-history.json")
	resetAt := now.Add(4 * time.Hour)
	provider := historyProvider("codex", "general-weekly", "Weekly", 60, resetAt)

	if events, err := observeUsageHistory(path, now, []ProviderUsage{provider}); err != nil || len(events) != 0 {
		t.Fatalf("baseline events = %+v, err = %v", events, err)
	}
	if events, err := observeUsageHistory(path, now.Add(time.Minute), []ProviderUsage{provider}); err != nil || len(events) != 0 {
		t.Fatalf("repeated events = %+v, err = %v", events, err)
	}
	missing := provider
	missing.QuotaBuckets = nil
	if events, err := observeUsageHistory(path, now.Add(2*time.Minute), []ProviderUsage{missing}); err != nil || len(events) != 0 {
		t.Fatalf("missing events = %+v, err = %v", events, err)
	}
	missingReset := historyProvider("codex", "general-weekly", "Weekly", 10, time.Time{})
	if events, err := observeUsageHistory(path, now.Add(150*time.Second), []ProviderUsage{missingReset}); err != nil || len(events) != 0 {
		t.Fatalf("missing-reset events = %+v, err = %v", events, err)
	}
	stale := historyProvider("claude", "general-weekly", "Weekly", 70, resetAt)
	stale.Meta["usageDataStale"] = true
	if events, err := observeUsageHistory(path, now.Add(3*time.Minute), []ProviderUsage{stale}); err != nil || len(events) != 0 {
		t.Fatalf("stale events = %+v, err = %v", events, err)
	}
	outOfOrder := historyProvider("codex", "general-weekly", "Weekly", 10, resetAt.Add(time.Hour))
	if events, err := observeUsageHistory(path, now.Add(-time.Minute), []ProviderUsage{outOfOrder}); err != nil || len(events) != 0 {
		t.Fatalf("out-of-order events = %+v, err = %v", events, err)
	}
	events, err := observeUsageHistory(path, now.Add(4*time.Minute), []ProviderUsage{outOfOrder})
	if err != nil || len(events) != 1 || events[0].Kind != "allowance_increased_unknown" {
		t.Fatalf("fresh refill events = %+v, err = %v", events, err)
	}
}

func TestUsageHistoryScheduledAndEarlyRefills(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	providers := []ProviderUsage{
		historyProvider("codex", "general-weekly", "Weekly", 80, now.Add(time.Hour)),
		historyProvider("claude", "general-weekly", "Weekly", 70, now.Add(4*time.Hour)),
	}
	if _, err := observeUsageHistory(path, now, providers); err != nil {
		t.Fatal(err)
	}
	providers[0] = historyProvider("codex", "general-weekly", "Weekly", 5, now.Add(8*24*time.Hour))
	providers[1] = historyProvider("claude", "general-weekly", "Weekly", 10, now.Add(7*24*time.Hour))
	events, err := observeUsageHistory(path, now.Add(2*time.Hour), providers)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	if events[0].Kind != "scheduled_window" || events[0].Confidence != "inferred" {
		t.Fatalf("scheduled event = %+v", events[0])
	}
	if events[1].Kind != "allowance_increased_unknown" || events[1].Label != "Weekly" {
		t.Fatalf("early event = %+v", events[1])
	}
}

func TestUsageHistoryAnchorShiftAndIdleSlidingWindow(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 20, now.Add(4*time.Hour))
	provider.QuotaBuckets = append(provider.QuotaBuckets, historyBucket("spark-weekly", "Spark · weekly", 0, now.Add(time.Hour), "codex app-server"))
	if _, err := observeUsageHistory(path, now, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 21, now.Add(5*time.Hour))
	provider.QuotaBuckets = append(provider.QuotaBuckets, historyBucket("spark-weekly", "Spark · weekly", 0, now.Add(2*time.Hour), "codex app-server"))
	events, err := observeUsageHistory(path, now.Add(time.Minute), []ProviderUsage{provider})
	if err != nil || len(events) != 1 || events[0].Kind != "window_changed_unknown" || events[0].Bucket != "general-weekly" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestUsageHistoryInfersRedemptionOnlyWithUnexpiredCompleteCredits(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 90, now.Add(6*time.Hour))
	setHistoryCredits(&provider, now.Add(12*time.Hour), now.Add(24*time.Hour))
	if _, err := observeUsageHistory(path, now, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 10, now.Add(7*time.Hour))
	setHistoryCredits(&provider, now.Add(24*time.Hour))
	events, err := observeUsageHistory(path, now.Add(time.Hour), []ProviderUsage{provider})
	if err != nil || len(events) != 1 || events[0].Kind != "reset_redeemed_inferred" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	if events[0].Before.AvailableCredits == nil || *events[0].Before.AvailableCredits != 2 ||
		events[0].After.AvailableCredits == nil || *events[0].After.AvailableCredits != 1 {
		t.Fatalf("credit evidence = %+v", events[0])
	}

	ambiguousPath := filepath.Join(t.TempDir(), "usage-history.json")
	provider = historyProvider("codex", "general-weekly", "Weekly", 90, now.Add(6*time.Hour))
	setHistoryCredits(&provider, now.Add(30*time.Minute))
	if _, err := observeUsageHistory(ambiguousPath, now, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 10, now.Add(7*time.Hour))
	setHistoryCredits(&provider)
	events, err = observeUsageHistory(ambiguousPath, now.Add(time.Hour), []ProviderUsage{provider})
	if err != nil || len(events) != 2 || events[0].Kind != "allowance_increased_unknown" || events[1].Kind != "credits_changed" {
		t.Fatalf("ambiguous events = %+v, err = %v", events, err)
	}
}

func TestUsageHistoryCountChangeWithoutRefillIsObservedOnly(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 20, now.Add(6*time.Hour))
	setHistoryCredits(&provider, now.Add(24*time.Hour))
	if _, err := observeUsageHistory(path, now, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 25, now.Add(6*time.Hour))
	setHistoryCredits(&provider, now.Add(24*time.Hour), now.Add(48*time.Hour))
	events, err := observeUsageHistory(path, now.Add(time.Hour), []ProviderUsage{provider})
	if err != nil || len(events) != 1 || events[0].Kind != "credits_changed" || events[0].Confidence != "observed" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestUsageHistoryRetentionAndPermissions(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	state := defaultUsageHistoryState()
	for index := 0; index < usageHistoryMaxEvents+20; index++ {
		observedAt := now.Add(-time.Duration(usageHistoryMaxEvents+20-index) * time.Minute)
		state.Events = append(state.Events, UsageHistoryEvent{ObservedAt: observedAt.Format(time.RFC3339), Provider: "codex"})
	}
	state.Events = append(state.Events, UsageHistoryEvent{ObservedAt: now.Add(-31 * 24 * time.Hour).Format(time.RFC3339), Provider: "codex"})
	pruneUsageHistory(&state, now)
	if len(state.Events) != usageHistoryMaxEvents {
		t.Fatalf("retained events = %d", len(state.Events))
	}
	path := filepath.Join(t.TempDir(), "state", "usage-history.json")
	if _, err := observeUsageHistory(path, now, nil); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".lock"} {
		info, err := os.Stat(candidate)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %v, err = %v", candidate, info, err)
		}
	}

	readPath := filepath.Join(t.TempDir(), "usage-history.json")
	readNow := time.Now().UTC()
	readState := defaultUsageHistoryState()
	readState.Events = []UsageHistoryEvent{
		{ObservedAt: readNow.Add(-31 * 24 * time.Hour).Format(time.RFC3339), Provider: "codex"},
		{ObservedAt: readNow.Format(time.RFC3339), Provider: "codex"},
	}
	if err := withUsageHistoryLock(readPath, func() error { return saveUsageHistoryState(readPath, readState) }); err != nil {
		t.Fatal(err)
	}
	readEvents, err := readUsageHistory(readPath)
	if err != nil || len(readEvents) != 1 {
		t.Fatalf("read retention events = %+v, err = %v", readEvents, err)
	}
	persisted, err := loadUsageHistoryState(readPath)
	if err != nil || len(persisted.Events) != 1 {
		t.Fatalf("persisted retention events = %+v, err = %v", persisted.Events, err)
	}
}

func TestUsageHistoryExpiredBaselineIsNotCompared(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	state := defaultUsageHistoryState()
	state.Observations[historyObservationKey("codex", "general-weekly")] = usageHistoryObservation{
		ObservedAt:  now.Add(-31 * 24 * time.Hour).Format(time.RFC3339),
		Provider:    "codex",
		Bucket:      "general-weekly",
		Label:       "Weekly",
		Source:      "codex app-server",
		UsedPercent: 90,
		ResetAt:     now.Add(-30 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := withUsageHistoryLock(path, func() error { return saveUsageHistoryState(path, state) }); err != nil {
		t.Fatal(err)
	}
	provider := historyProvider("codex", "general-weekly", "Weekly", 10, now.Add(7*24*time.Hour))
	events, err := observeUsageHistory(path, now, []ProviderUsage{provider})
	if err != nil || len(events) != 0 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestUsageHistoryCorruptStateIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-history.json")
	want := []byte(`{"version":`)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := observeUsageHistory(path, time.Now(), nil); err == nil {
		t.Fatal("expected corrupt-state error")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("corrupt state changed: %q, err = %v", got, err)
	}
}

func TestUsageHistoryConcurrentSerialization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-history.json")
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	successes := 0
	for index := 0; index < 20; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			err := recordCodexResetHistory(path, codexResetHistoryRecord{
				ObservedAt:       now.Add(time.Duration(index) * time.Second),
				BeforeObservedAt: now,
				Outcome:          "noCredit",
			})
			if err != nil {
				if !strings.Contains(err.Error(), "timed out") {
					t.Errorf("record %d: %v", index, err)
				}
				return
			}
			resultMu.Lock()
			successes++
			resultMu.Unlock()
		}(index)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state usageHistoryState
	if err := json.Unmarshal(data, &state); err != nil || len(state.Events) != successes || successes == 0 {
		t.Fatalf("serialized state events = %d, successes = %d, err = %v", len(state.Events), successes, err)
	}
}

func TestCodexResetHistoryFailureDoesNotChangeOneShotOutcome(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	dir := t.TempDir()
	statePath := filepath.Join(dir, "codex-reset.json")
	historyPath := filepath.Join(dir, "usage-history.json")
	state := armedResetState(now, "pinned")
	if err := withCodexResetLock(statePath, func() error { return saveCodexResetState(statePath, state) }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte(`{"version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexResetClient{reads: []codexRateLimitsResult{triggeringLimits(state, now), triggeringLimits(state, now)}}
	deps := resetDeps(statePath, now, func(context.Context) (codexResetClient, error) { return client, nil })
	deps.HistoryPath = historyPath
	status, err := runCodexResetAction("check", deps)
	if err != nil || status.State != "completed" || status.Armed || status.HistoryError == "" || client.consumeCall != 1 {
		t.Fatalf("status = %+v, err = %v, consumes = %d", status, err, client.consumeCall)
	}
	saved, loadErr := loadCodexResetState(statePath)
	if loadErr != nil || saved.Armed || saved.Outcome != "reset" {
		t.Fatalf("saved reset state = %+v, err = %v", saved, loadErr)
	}
}

func TestCodexResetHistoryRecordsConfirmedAndUnknownOutcomes(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	for _, test := range []struct {
		name       string
		consumeErr error
		wantKind   string
	}{
		{name: "confirmed", wantKind: "plugin_reset_reset"},
		{name: "unknown", consumeErr: context.DeadlineExceeded, wantKind: "plugin_reset_attempt_unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			statePath := filepath.Join(dir, "codex-reset.json")
			historyPath := filepath.Join(dir, "usage-history.json")
			state := armedResetState(now, "pinned")
			if err := withCodexResetLock(statePath, func() error { return saveCodexResetState(statePath, state) }); err != nil {
				t.Fatal(err)
			}
			client := &fakeCodexResetClient{reads: []codexRateLimitsResult{triggeringLimits(state, now), triggeringLimits(state, now)}}
			client.consume = func(string, string) (string, error) { return "reset", test.consumeErr }
			deps := resetDeps(statePath, now, func(context.Context) (codexResetClient, error) { return client, nil })
			deps.HistoryPath = historyPath
			_, _ = runCodexResetAction("check", deps)
			events, err := readUsageHistory(historyPath)
			if err != nil || len(events) != 1 || events[0].Kind != test.wantKind {
				t.Fatalf("events = %+v, err = %v", events, err)
			}
		})
	}
}

func TestNonAppliedPluginOutcomeDoesNotConsumeQuotaObservation(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 80, now.Add(5*time.Hour))
	if _, err := observeUsageHistory(path, now, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	before := generalHistoryLimits(80, now.Add(5*time.Hour))
	after := generalHistoryLimits(10, now.Add(6*time.Hour))
	if err := recordCodexResetHistory(path, codexResetHistoryRecord{
		ObservedAt:       now.Add(time.Minute),
		BeforeObservedAt: now,
		Outcome:          "nothingToReset",
		Before:           before,
		After:            &after,
	}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 10, now.Add(6*time.Hour))
	events, err := observeUsageHistory(path, now.Add(2*time.Minute), []ProviderUsage{provider})
	if err != nil || len(events) != 2 || events[0].Kind != "plugin_reset_nothing_to_reset" || events[1].Kind != "allowance_increased_unknown" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestConfirmedResetWithInvalidRefreshAnchorPreservesBaseline(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 20, now.Add(5*time.Hour))
	if _, err := observeUsageHistory(path, now, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	before := generalHistoryLimits(20, now.Add(5*time.Hour))
	after := generalHistoryLimits(20, now.Add(-time.Minute))
	if err := recordCodexResetHistory(path, codexResetHistoryRecord{
		ObservedAt:       now.Add(time.Minute),
		BeforeObservedAt: now,
		Outcome:          "reset",
		Before:           before,
		After:            &after,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := observeUsageHistory(path, now.Add(2*time.Minute), []ProviderUsage{provider})
	if err != nil || len(events) != 1 || events[0].Kind != "plugin_reset_reset" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestUsageHistorySameSecondSamplesStayCorrelated(t *testing.T) {
	base := mustParseTime(t, "2026-09-08T12:00:00Z").Add(100 * time.Millisecond)
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 20, base.Add(5*time.Hour))
	setHistoryCredits(&provider, base.Add(24*time.Hour), base.Add(48*time.Hour))
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 30, base.Add(5*time.Hour))
	setHistoryCredits(&provider, base.Add(24*time.Hour))
	second := base.Add(100 * time.Millisecond)
	events, err := observeUsageHistory(path, second, []ProviderUsage{provider})
	if err != nil || len(events) != 1 || events[0].Kind != "credits_changed" {
		t.Fatalf("same-second events = %+v, err = %v", events, err)
	}
	state, err := loadUsageHistoryState(path)
	if err != nil {
		t.Fatal(err)
	}
	observation := state.Observations[historyObservationKey("codex", "general-weekly")]
	credits := state.Credits["codex"]
	if observation.ObservedAt != historyTimestamp(second) || credits.ObservedAt != observation.ObservedAt || credits.AvailableCredits != 1 {
		t.Fatalf("quota = %+v, credits = %+v", observation, credits)
	}
}

func TestSameSecondConfirmedPluginEventSuppressesRedemptionInference(t *testing.T) {
	base := mustParseTime(t, "2026-09-08T12:00:00Z").Add(100 * time.Millisecond)
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 90, base.Add(5*time.Hour))
	setHistoryCredits(&provider, base.Add(24*time.Hour))
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	if err := recordCodexResetHistory(path, codexResetHistoryRecord{
		ObservedAt:       base.Add(100 * time.Millisecond),
		BeforeObservedAt: base,
		Outcome:          "reset",
		Before:           generalHistoryLimits(90, base.Add(5*time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 10, base.Add(6*time.Hour))
	setHistoryCredits(&provider)
	events, err := observeUsageHistory(path, base.Add(200*time.Millisecond), []ProviderUsage{provider})
	if err != nil || len(events) != 3 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	if events[1].Kind != "allowance_increased_unknown" || events[2].Kind != "credits_changed" {
		t.Fatalf("plugin correlation events = %+v", events)
	}
}

func TestOlderInFlightCollectionCannotOverwriteConfirmedResetBaseline(t *testing.T) {
	base := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 90, base.Add(5*time.Hour))
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	before := generalHistoryLimits(90, base.Add(5*time.Hour))
	after := generalHistoryLimits(10, base.Add(6*time.Hour))
	if err := recordCodexResetHistory(path, codexResetHistoryRecord{
		ObservedAt:       base.Add(2 * time.Minute),
		BeforeObservedAt: base.Add(time.Minute),
		Outcome:          "reset",
		Before:           before,
		After:            &after,
	}); err != nil {
		t.Fatal(err)
	}

	stale := historyProvider("codex", "general-weekly", "Weekly", 90, base.Add(5*time.Hour))
	stale.historyObservedAt = base.Add(time.Minute)
	if events, err := observeUsageHistory(path, base.Add(3*time.Minute), []ProviderUsage{stale}); err != nil || len(events) != 1 {
		t.Fatalf("in-flight events = %+v, err = %v", events, err)
	}
	fresh := historyProvider("codex", "general-weekly", "Weekly", 10, base.Add(6*time.Hour))
	fresh.historyObservedAt = base.Add(4 * time.Minute)
	events, err := observeUsageHistory(path, base.Add(4*time.Minute), []ProviderUsage{fresh})
	if err != nil || len(events) != 1 || events[0].Kind != "plugin_reset_reset" {
		t.Fatalf("fresh events = %+v, err = %v", events, err)
	}
}

func TestCollectionBoundaryUsesEndTimeForNaturalReset(t *testing.T) {
	base := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 80, base.Add(1500*time.Millisecond))
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 10, base.Add(6*time.Hour))
	provider.historyObservedAt = base.Add(time.Second)
	events, err := observeUsageHistory(path, base.Add(2*time.Second), []ProviderUsage{provider})
	if err != nil || len(events) != 1 || events[0].Kind != "scheduled_window" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestCollectionBoundaryDoesNotInferCreditRedemptionAfterExpiry(t *testing.T) {
	base := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 90, base.Add(5*time.Hour))
	setHistoryCredits(&provider, base.Add(1500*time.Millisecond))
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 10, base.Add(6*time.Hour))
	setHistoryCredits(&provider)
	provider.historyObservedAt = base.Add(time.Second)
	events, err := observeUsageHistory(path, base.Add(2*time.Second), []ProviderUsage{provider})
	if err != nil || len(events) != 2 || events[0].Kind != "allowance_increased_unknown" || events[1].Kind != "credits_changed" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestOverlappingPluginSuccessSuppressesRedemptionWithoutRefresh(t *testing.T) {
	base := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 90, base.Add(5*time.Hour))
	setHistoryCredits(&provider, base.Add(24*time.Hour))
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	if err := recordCodexResetHistory(path, codexResetHistoryRecord{
		ObservedAt:       base.Add(1500 * time.Millisecond),
		BeforeObservedAt: base,
		Outcome:          "reset",
		Before:           generalHistoryLimits(90, base.Add(5*time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 10, base.Add(6*time.Hour))
	setHistoryCredits(&provider)
	provider.historyObservedAt = base.Add(time.Second)
	events, err := observeUsageHistory(path, base.Add(2*time.Second), []ProviderUsage{provider})
	if err != nil || len(events) != 3 || events[1].Kind != "allowance_increased_unknown" || events[2].Kind != "credits_changed" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestConfirmedResetBaselineWinsAfterOlderSummaryWritesFirst(t *testing.T) {
	base := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 90, base.Add(5*time.Hour))
	setHistoryCredits(&provider, base.Add(24*time.Hour))
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	stale := provider
	stale.historyObservedAt = base.Add(time.Second)
	if _, err := observeUsageHistory(path, base.Add(3*time.Second), []ProviderUsage{stale}); err != nil {
		t.Fatal(err)
	}
	before := generalHistoryLimits(90, base.Add(5*time.Hour))
	after := generalHistoryLimits(10, base.Add(6*time.Hour))
	after.RateLimitResetCredits.AvailableCountKnown = true
	after.RateLimitResetCredits.AvailableCount = 0
	if err := recordCodexResetHistory(path, codexResetHistoryRecord{
		ObservedAt:       base.Add(2 * time.Second),
		BeforeObservedAt: base.Add(time.Second),
		Outcome:          "reset",
		Before:           before,
		After:            &after,
	}); err != nil {
		t.Fatal(err)
	}
	state, err := loadUsageHistoryState(path)
	if err != nil || state.Observations[historyObservationKey("codex", "general-weekly")].UsedPercent != 10 || state.Credits["codex"].AvailableCredits != 0 {
		t.Fatalf("state = %+v, err = %v", state, err)
	}
	fresh := historyProvider("codex", "general-weekly", "Weekly", 10, base.Add(6*time.Hour))
	setHistoryCredits(&fresh)
	fresh.historyObservedAt = base.Add(4 * time.Second)
	events, err := observeUsageHistory(path, base.Add(4*time.Second), []ProviderUsage{fresh})
	if err != nil || len(events) != 1 || events[0].Kind != "plugin_reset_reset" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestCodexResetCreditsTracksAvailableCountPresence(t *testing.T) {
	var credits codexRateLimitResetCredits
	if err := json.Unmarshal([]byte(`{"availableCount":0,"credits":[]}`), &credits); err != nil || !credits.AvailableCountKnown {
		t.Fatalf("present count = %+v, err = %v", credits, err)
	}
	if err := json.Unmarshal([]byte(`{"credits":[]}`), &credits); err != nil || credits.AvailableCountKnown || credits.AvailableCount != 0 {
		t.Fatalf("missing count = %+v, err = %v", credits, err)
	}
}

func historyProvider(providerID, bucketID, label string, used float64, resetAt time.Time) ProviderUsage {
	source := "codex app-server"
	meta := map[string]any{"source": source}
	if providerID == "claude" {
		source = claudeOAuthUsageSource
		meta["source"] = source
	}
	return ProviderUsage{
		ID:           providerID,
		Available:    true,
		Meta:         meta,
		QuotaBuckets: []QuotaBucket{historyBucket(bucketID, label, used, resetAt, source)},
	}
}

func historyBucket(id, label string, used float64, resetAt time.Time, source string) QuotaBucket {
	return QuotaBucket{
		ID:        id,
		Label:     label,
		Kind:      "weekly",
		Allowance: makeSubscriptionAllowance("weekly", source, used, resetAt, 10080),
	}
}

func setHistoryCredits(provider *ProviderUsage, expiries ...time.Time) {
	provider.Meta["availableResetCount"] = len(expiries)
	provider.Resets = nil
	for _, expiresAt := range expiries {
		provider.Resets = append(provider.Resets, UsageReset{Title: "Earned reset", ResetType: "codexRateLimits", ExpiresAt: expiresAt.Format(time.RFC3339)})
	}
}

func generalHistoryLimits(used float64, resetAt time.Time) codexRateLimitsResult {
	minutes := int64(10080)
	resetUnix := resetAt.Unix()
	return codexRateLimitsResult{RateLimits: codexRateLimitSnapshot{
		LimitID: "codex",
		Primary: &codexRateLimitWindow{UsedPercent: used, WindowDurationMins: &minutes, ResetsAt: &resetUnix},
	}}
}
