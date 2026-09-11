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

func TestUsageHistoryExplanationGroupingChoicesAndPersistence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "usage-history.json")
	beforeUsed, afterUsed := 80.0, 10.0
	beforeCredits, afterCredits := 2, 1
	state := defaultUsageHistoryState()
	state.Events = []UsageHistoryEvent{
		{
			ObservedAt: now.Format(time.RFC3339), Provider: "codex", Bucket: "general-weekly", Label: "Weekly",
			Kind: "allowance_increased_unknown", Source: "quota_observation", Confidence: "inferred",
			Before: &UsageHistoryValue{UsedPercent: &beforeUsed}, After: &UsageHistoryValue{UsedPercent: &afterUsed}, Message: "original refill",
		},
		{
			ObservedAt: now.Format(time.RFC3339), Provider: "codex", Bucket: "earned-resets", Label: "Earned resets",
			Kind: "credits_changed", Source: "quota_observation", Confidence: "observed",
			Before: &UsageHistoryValue{AvailableCredits: &beforeCredits}, After: &UsageHistoryValue{AvailableCredits: &afterCredits}, Message: "original credits",
		},
		{
			ObservedAt: now.Format(time.RFC3339), Provider: "claude", Bucket: "general-weekly", Label: "Weekly",
			Kind: "window_changed_unknown", Source: "quota_observation", Confidence: "observed", Message: "claude window",
		},
		{
			ObservedAt: now.Add(time.Second).Format(time.RFC3339), Provider: "codex", Kind: "scheduled_window",
			Source: "quota_observation", Confidence: "inferred", Message: "scheduled",
		},
		{
			ObservedAt: now.Add(2 * time.Second).Format(time.RFC3339), Provider: "codex", Kind: "plugin_reset_reset",
			Source: "plugin_reset", Confidence: "confirmed", Message: "plugin",
		},
	}
	if err := withUsageHistoryLock(path, func() error { return saveUsageHistoryState(path, state) }); err != nil {
		t.Fatal(err)
	}

	events, err := readUsageHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].GroupID == "" || events[0].GroupID != events[1].GroupID {
		t.Fatalf("correlated group ids = %q, %q", events[0].GroupID, events[1].GroupID)
	}
	if events[2].GroupID == events[0].GroupID {
		t.Fatal("providers shared an explanation group")
	}
	for _, index := range []int{3, 4} {
		if events[index].Explainable || events[index].GroupID != "" || len(events[index].ExplanationChoices) != 0 {
			t.Fatalf("ineligible event %d decorated as %+v", index, events[index])
		}
	}
	if !slicesContain(events[0].ExplanationChoices, "provider_bonus") || slicesContain(events[1].ExplanationChoices, "provider_bonus") {
		t.Fatalf("individual choices = refill %v, drop %v", events[0].ExplanationChoices, events[1].ExplanationChoices)
	}

	note := "  changed during support call  "
	request, err := normalizeUsageHistoryExplainRequest(usageHistoryExplainRequest{GroupID: events[0].GroupID, Reason: "provider_bonus", Note: &note})
	if err != nil {
		t.Fatal(err)
	}
	events, err = explainUsageHistory(path, now.Add(time.Minute), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 1} {
		if events[index].Explanation == nil || events[index].Explanation.Reason != "provider_bonus" ||
			events[index].Explanation.Note != "changed during support call" || events[index].Explanation.Source != "user" {
			t.Fatalf("group explanation %d = %+v", index, events[index].Explanation)
		}
	}
	if events[0].Kind != "allowance_increased_unknown" || events[0].Confidence != "inferred" || events[0].Message != "original refill" ||
		*events[0].Before.UsedPercent != 80 || *events[0].After.UsedPercent != 10 {
		t.Fatalf("original event facts changed: %+v", events[0])
	}

	events, err = explainUsageHistory(path, now.Add(2*time.Minute), usageHistoryExplainRequest{GroupID: events[0].GroupID, Reason: "dismissed"})
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Explanation.Reason != "dismissed" || events[0].Explanation.Note != "" || events[0].Explanation.Source != "user" {
		t.Fatalf("edited explanation = %+v", events[0].Explanation)
	}
	reloaded, err := readUsageHistory(path)
	if err != nil || reloaded[0].GroupID != events[0].GroupID || reloaded[0].Explanation.Reason != "dismissed" {
		t.Fatalf("reloaded events = %+v, err = %v", reloaded, err)
	}
}

func TestUsageHistoryExplanationChoiceFilters(t *testing.T) {
	beforeOne, afterTwo := 1, 2
	beforeTwo, afterOne := 2, 1
	creditIncrease := UsageHistoryEvent{Provider: "codex", Kind: "credits_changed", Source: "quota_observation",
		Before: &UsageHistoryValue{AvailableCredits: &beforeOne}, After: &UsageHistoryValue{AvailableCredits: &afterTwo}}
	creditDrop := UsageHistoryEvent{Provider: "codex", Kind: "credits_changed", Source: "quota_observation",
		Before: &UsageHistoryValue{AvailableCredits: &beforeTwo}, After: &UsageHistoryValue{AvailableCredits: &afterOne}}
	claudeRefill := UsageHistoryEvent{Provider: "claude", Kind: "allowance_increased_unknown", Source: "quota_observation"}

	if slicesContain(usageHistoryChoices(creditIncrease), "external_reset") || !slicesContain(usageHistoryChoices(creditIncrease), "provider_bonus") {
		t.Fatalf("credit increase choices = %v", usageHistoryChoices(creditIncrease))
	}
	if !slicesContain(usageHistoryChoices(creditDrop), "external_reset") || slicesContain(usageHistoryChoices(creditDrop), "provider_bonus") {
		t.Fatalf("credit drop choices = %v", usageHistoryChoices(creditDrop))
	}
	if slicesContain(usageHistoryChoices(claudeRefill), "external_reset") || !slicesContain(usageHistoryChoices(claudeRefill), "provider_bonus") {
		t.Fatalf("Claude refill choices = %v", usageHistoryChoices(claudeRefill))
	}
	for _, event := range []UsageHistoryEvent{
		{Kind: "scheduled_window", Source: "quota_observation"},
		{Kind: "plugin_reset_attempt_unknown", Source: "plugin_reset"},
		{Provider: "codex", Kind: "credits_changed", Source: "quota_observation", ExpiryExplained: true},
	} {
		if choices := usageHistoryChoices(event); len(choices) != 0 {
			t.Fatalf("ineligible choices = %v for %+v", choices, event)
		}
	}
}

func TestUsageHistoryExplanationRejectsStaleUnsupportedAndCorruptWithoutSaving(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	for _, test := range []struct {
		name    string
		state   usageHistoryState
		groupID string
		reason  string
	}{
		{
			name: "pruned",
			state: usageHistoryState{Version: usageHistoryStateVersion, Events: []UsageHistoryEvent{{
				ObservedAt: now.Add(-31 * 24 * time.Hour).Format(time.RFC3339), Provider: "codex", Kind: "allowance_increased_unknown", Source: "quota_observation",
			}}},
			groupID: usageHistoryGroupID("codex", now.Add(-31*24*time.Hour).Format(time.RFC3339)), reason: "unknown",
		},
		{
			name: "ineligible",
			state: usageHistoryState{Version: usageHistoryStateVersion, Events: []UsageHistoryEvent{{
				ObservedAt: now.Format(time.RFC3339), Provider: "codex", Kind: "scheduled_window", Source: "quota_observation",
			}}},
			groupID: usageHistoryGroupID("codex", now.Format(time.RFC3339)), reason: "unknown",
		},
		{
			name: "unsupported reason",
			state: usageHistoryState{Version: usageHistoryStateVersion, Events: []UsageHistoryEvent{{
				ObservedAt: now.Format(time.RFC3339), Provider: "codex", Kind: "credits_changed", Source: "quota_observation",
				Before: &UsageHistoryValue{AvailableCredits: intPointer(2)}, After: &UsageHistoryValue{AvailableCredits: intPointer(1)},
			}}},
			groupID: usageHistoryGroupID("codex", now.Format(time.RFC3339)), reason: "provider_bonus",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage-history.json")
			if test.state.Observations == nil {
				test.state.Observations = map[string]usageHistoryObservation{}
			}
			if test.state.Credits == nil {
				test.state.Credits = map[string]usageHistoryCreditObservation{}
			}
			if err := withUsageHistoryLock(path, func() error { return saveUsageHistoryState(path, test.state) }); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := explainUsageHistory(path, now, usageHistoryExplainRequest{GroupID: test.groupID, Reason: test.reason}); err == nil {
				t.Fatal("expected explanation rejection")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("state changed on rejection: before %q after %q, err = %v", before, after, err)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "usage-history.json")
	corrupt := []byte(`{}`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := explainUsageHistory(path, now, usageHistoryExplainRequest{GroupID: "missing", Reason: "unknown"}); err == nil {
		t.Fatal("expected corrupt-state rejection")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(corrupt) {
		t.Fatalf("corrupt state changed: %q, err = %v", got, err)
	}
}

func TestUsageHistoryExplanationRequestValidation(t *testing.T) {
	valid := `{"groupId":"group","reason":"unknown","note":"  hello  "}`
	request, err := readUsageHistoryExplainRequest(strings.NewReader(valid + "\n"))
	if err != nil || request.Note == nil || *request.Note != "hello" {
		t.Fatalf("request = %+v, err = %v", request, err)
	}
	withoutNewline, err := readUsageHistoryExplainRequest(strings.NewReader(valid))
	if err != nil || withoutNewline.GroupID != "group" {
		t.Fatalf("EOF-framed request = %+v, err = %v", withoutNewline, err)
	}
	longNote := strings.Repeat("ø", usageHistoryNoteMaxRunes+1)
	for name, payload := range map[string]string{
		"unknown field": `{"groupId":"group","reason":"unknown","note":"private","extra":true}`,
		"trailing JSON": `{"groupId":"group","reason":"unknown","note":"private"}{"note":"private"}`,
		"oversize body": strings.Repeat("x", usageHistoryExplainMax+1),
		"long note":     `{"groupId":"group","reason":"unknown","note":"` + longNote + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := readUsageHistoryExplainRequest(strings.NewReader(payload + "\n"))
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe validation error = %v", err)
			}
		})
	}
}

func TestUsageHistoryKnownCreditExpiryIsNotExplainable(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 20, now.Add(12*time.Hour))
	remainingExpiry := now.Add(24 * time.Hour)
	setHistoryCredits(&provider, now.Add(time.Hour), remainingExpiry)
	if _, err := observeUsageHistory(path, now, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	provider = historyProvider("codex", "general-weekly", "Weekly", 25, now.Add(12*time.Hour))
	setHistoryCredits(&provider, remainingExpiry)
	events, err := observeUsageHistory(path, now.Add(2*time.Hour), []ProviderUsage{provider})
	if err != nil || len(events) != 1 || events[0].Kind != "credits_changed" {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	if !events[0].ExpiryExplained || events[0].Explainable || events[0].GroupID != "" {
		t.Fatalf("expiry-explained event = %+v", events[0])
	}
	reloaded, err := readUsageHistory(path)
	if err != nil || !reloaded[0].ExpiryExplained || reloaded[0].Explainable {
		t.Fatalf("reloaded events = %+v, err = %v", reloaded, err)
	}
}

func TestUsageHistoryExplanationSurvivesPartialGroupPruning(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "usage-history.json")
	groupTime := now.Add(-time.Hour).Format(time.RFC3339)
	explanation := &UsageHistoryExplanation{Reason: "provider_bonus", UpdatedAt: now.Format(time.RFC3339), Source: "user"}
	state := defaultUsageHistoryState()
	state.Events = append(state.Events,
		UsageHistoryEvent{ObservedAt: groupTime, Provider: "codex", Kind: "allowance_increased_unknown", Source: "quota_observation", Explanation: explanation},
		UsageHistoryEvent{ObservedAt: groupTime, Provider: "codex", Kind: "credits_changed", Source: "quota_observation",
			Before: &UsageHistoryValue{AvailableCredits: intPointer(2)}, After: &UsageHistoryValue{AvailableCredits: intPointer(1)}, Explanation: explanation},
	)
	for index := 0; index < usageHistoryMaxEvents-1; index++ {
		state.Events = append(state.Events, UsageHistoryEvent{
			ObservedAt: now.Add(time.Duration(index+1) * time.Second).Format(time.RFC3339),
			Provider:   "codex", Kind: "scheduled_window", Source: "quota_observation",
		})
	}
	if err := decorateUsageHistoryEvents(state.Events); err != nil {
		t.Fatal(err)
	}
	if err := withUsageHistoryLock(path, func() error { return saveUsageHistoryState(path, state) }); err != nil {
		t.Fatal(err)
	}
	events, err := readUsageHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != usageHistoryMaxEvents {
		t.Fatalf("pruned history len = %d", len(events))
	}
	if events[0].Kind != "credits_changed" || events[0].Explanation == nil || events[0].Explanation.Reason != "provider_bonus" {
		t.Fatalf("pruned history first = %+v", events[0])
	}
	if _, err := readUsageHistory(path); err != nil {
		t.Fatalf("persisted partial group became unreadable: %v", err)
	}
}

func TestUsageHistoryVersionOneMigratesOnWriteAndFutureVersionIsPreserved(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "usage-history.json")
	legacy := []byte(`{"version":1,"observations":{},"credits":{},"events":[{"observedAt":"` + now.Format(time.RFC3339) + `","provider":"codex","kind":"allowance_increased_unknown","source":"quota_observation","confidence":"inferred","message":"legacy"}]}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := readUsageHistory(path)
	if err != nil || len(events) != 1 || !events[0].Explainable || events[0].GroupID == "" {
		t.Fatalf("legacy events = %+v, err = %v", events, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(legacy) {
		t.Fatalf("read-only migration rewrote state: %q, err = %v", unchanged, err)
	}
	if _, err := explainUsageHistory(path, now.Add(time.Minute), usageHistoryExplainRequest{GroupID: events[0].GroupID, Reason: "unknown"}); err != nil {
		t.Fatal(err)
	}
	var migrated usageHistoryState
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &migrated) != nil || migrated.Version != usageHistoryStateVersion || migrated.Events[0].Explanation == nil {
		t.Fatalf("migrated state = %+v, err = %v", migrated, err)
	}

	futurePath := filepath.Join(t.TempDir(), "usage-history.json")
	future := []byte(`{"version":999,"observations":{},"credits":{},"events":[]}`)
	if err := os.WriteFile(futurePath, future, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := explainUsageHistory(futurePath, now, usageHistoryExplainRequest{GroupID: "missing", Reason: "unknown"}); err == nil {
		t.Fatal("expected future-version rejection")
	}
	got, err := os.ReadFile(futurePath)
	if err != nil || string(got) != string(future) {
		t.Fatalf("future state changed: %q, err = %v", got, err)
	}
}

func TestUsageHistoryConcurrentExplanationEditsRemainAtomic(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "usage-history.json")
	state := defaultUsageHistoryState()
	state.Events = []UsageHistoryEvent{{
		ObservedAt: now.Format(time.RFC3339), Provider: "codex", Kind: "allowance_increased_unknown", Source: "quota_observation",
	}}
	if err := withUsageHistoryLock(path, func() error { return saveUsageHistoryState(path, state) }); err != nil {
		t.Fatal(err)
	}
	events, err := readUsageHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	reasons := []string{"subscription_change", "external_reset", "account_change", "provider_bonus", "unknown", "dismissed"}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, len(reasons))
	for index, reason := range reasons {
		wg.Add(1)
		go func(index int, reason string) {
			defer wg.Done()
			_, err := explainUsageHistory(path, now.Add(time.Duration(index+1)*time.Millisecond), usageHistoryExplainRequest{
				GroupID: events[0].GroupID,
				Reason:  reason,
			})
			errorsSeen <- err
		}(index, reason)
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent edit failed: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved usageHistoryState
	if err := json.Unmarshal(data, &saved); err != nil || len(saved.Events) != 1 || saved.Events[0].Explanation == nil ||
		!slicesContain(reasons, saved.Events[0].Explanation.Reason) {
		t.Fatalf("saved state = %+v, err = %v", saved, err)
	}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func intPointer(value int) *int {
	return &value
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
	now := time.Now().UTC().Truncate(time.Second)
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
