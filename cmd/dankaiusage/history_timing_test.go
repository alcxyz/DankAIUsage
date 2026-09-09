package main

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestUsageHistoryResetTimingTolerance(t *testing.T) {
	base := mustParseTime(t, "2026-09-09T12:00:00Z")
	for _, providerID := range []string{"codex", "claude"} {
		for _, shift := range []time.Duration{-5 * time.Second, -time.Second, time.Second, 5 * time.Second} {
			name := providerID + "/" + shift.String()
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "usage-history.json")
				resetAt := base.Add(8 * time.Hour)
				provider := historyProvider(providerID, "general-weekly", "Weekly", 20, resetAt)
				if events, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil || len(events) != 0 {
					t.Fatalf("baseline events = %+v, err = %v", events, err)
				}

				provider = historyProvider(providerID, "general-weekly", "Weekly", 25, resetAt.Add(shift))
				events, err := observeUsageHistory(path, base.Add(time.Minute), []ProviderUsage{provider})
				if err != nil || len(events) != 0 {
					t.Fatalf("jitter events = %+v, err = %v", events, err)
				}
			})
		}

		for _, shift := range []time.Duration{-6 * time.Second, 6 * time.Second} {
			t.Run(providerID+"/"+shift.String()+"_is_meaningful", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "usage-history.json")
				resetAt := base.Add(8 * time.Hour)
				provider := historyProvider(providerID, "general-weekly", "Weekly", 20, resetAt)
				if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
					t.Fatal(err)
				}

				provider = historyProvider(providerID, "general-weekly", "Weekly", 25, resetAt.Add(shift))
				events, err := observeUsageHistory(path, base.Add(time.Minute), []ProviderUsage{provider})
				if err != nil || len(events) != 1 || events[0].Kind != "window_changed_unknown" {
					t.Fatalf("six-second events = %+v, err = %v", events, err)
				}
			})
		}
	}
}

func TestUsageHistoryRefillWinsOverResetTimingJitter(t *testing.T) {
	base := mustParseTime(t, "2026-09-09T12:00:00Z")
	for _, test := range []struct {
		providerID string
		shift      time.Duration
	}{
		{providerID: "codex", shift: 5 * time.Second},
		{providerID: "claude", shift: -5 * time.Second},
	} {
		t.Run(test.providerID, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage-history.json")
			resetAt := base.Add(8 * time.Hour)
			provider := historyProvider(test.providerID, "general-weekly", "Weekly", 80, resetAt)
			if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
				t.Fatal(err)
			}

			provider = historyProvider(test.providerID, "general-weekly", "Weekly", 10, resetAt.Add(test.shift))
			events, err := observeUsageHistory(path, base.Add(time.Minute), []ProviderUsage{provider})
			if err != nil || len(events) != 1 || events[0].Kind != "allowance_increased_unknown" {
				t.Fatalf("refill events = %+v, err = %v", events, err)
			}
			if events[0].Before == nil || events[0].After == nil ||
				events[0].Before.UsedPercent == nil || *events[0].Before.UsedPercent != 80 ||
				events[0].After.UsedPercent == nil || *events[0].After.UsedPercent != 10 {
				t.Fatalf("refill evidence = %+v", events[0])
			}
		})
	}
}

func TestUsageHistoryLegacyTimingNoiseDecorationPreservesExplanation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	observedAt := historyTimestamp(now)
	beforeUsed, afterUsed := 20.0, 25.0
	beforeReset := historyTimestamp(now.Add(24 * time.Hour))
	afterReset := historyTimestamp(now.Add(24*time.Hour + 5*time.Second))
	groupID := usageHistoryGroupID("codex", observedAt)
	explanation := &UsageHistoryExplanation{
		Reason:    "account_change",
		Note:      "workspace changed",
		UpdatedAt: historyTimestamp(now.Add(time.Minute)),
		Source:    "user",
	}
	event := UsageHistoryEvent{
		ObservedAt: observedAt, Provider: "codex", Bucket: "general-weekly", Label: "Weekly",
		Kind: "window_changed_unknown", Source: "quota_observation", Confidence: "observed",
		Before:      &UsageHistoryValue{UsedPercent: &beforeUsed, ResetAt: beforeReset},
		After:       &UsageHistoryValue{UsedPercent: &afterUsed, ResetAt: afterReset},
		Message:     "Quota window timing changed; the cause is unknown.",
		GroupID:     groupID,
		Explainable: true,
		ExplanationChoices: []string{
			"subscription_change", "external_reset", "account_change", "provider_bonus", "unknown", "dismissed",
		},
		Explanation: explanation,
	}
	originalChoices := append([]string(nil), event.ExplanationChoices...)
	events := []UsageHistoryEvent{event}
	if err := decorateUsageHistoryEvents(events); err != nil {
		t.Fatal(err)
	}
	assertLegacyTimingNoiseEvent(t, events[0], groupID, originalChoices, *explanation)

	path := filepath.Join(t.TempDir(), "usage-history.json")
	state := defaultUsageHistoryState()
	state.Version = usageHistoryLegacyVersion
	state.Events = events
	state.Events[0].TimingNoise = false // Legacy retained data did not persist this derived field.
	if err := withUsageHistoryLock(path, func() error { return saveUsageHistoryState(path, state) }); err != nil {
		t.Fatal(err)
	}
	reloaded, err := readUsageHistory(path)
	if err != nil || len(reloaded) != 1 {
		t.Fatalf("reloaded events = %+v, err = %v", reloaded, err)
	}
	assertLegacyTimingNoiseEvent(t, reloaded[0], groupID, originalChoices, *explanation)
}

func TestUsageHistoryTimingNoiseRequiresCompleteValidLegacyEvidence(t *testing.T) {
	beforeUsed, afterUsed := 20.0, 25.0
	valid := UsageHistoryEvent{
		ObservedAt: "2026-09-09T12:00:00Z", Provider: "claude", Kind: "window_changed_unknown",
		Before: &UsageHistoryValue{UsedPercent: &beforeUsed, ResetAt: "2026-09-10T12:00:00Z"},
		After:  &UsageHistoryValue{UsedPercent: &afterUsed, ResetAt: "2026-09-10T12:00:05Z"},
	}
	for _, test := range []struct {
		name   string
		mutate func(*UsageHistoryEvent)
	}{
		{name: "wrong kind", mutate: func(event *UsageHistoryEvent) { event.Kind = "allowance_increased_unknown" }},
		{name: "missing before", mutate: func(event *UsageHistoryEvent) { event.Before = nil }},
		{name: "missing after", mutate: func(event *UsageHistoryEvent) { event.After = nil }},
		{name: "missing used percent", mutate: func(event *UsageHistoryEvent) { event.After.UsedPercent = nil }},
		{name: "malformed reset", mutate: func(event *UsageHistoryEvent) { event.Before.ResetAt = "not-a-time" }},
		{name: "missing reset", mutate: func(event *UsageHistoryEvent) { event.After.ResetAt = "" }},
		{name: "unchanged reset", mutate: func(event *UsageHistoryEvent) { event.After.ResetAt = event.Before.ResetAt }},
		{name: "six second shift", mutate: func(event *UsageHistoryEvent) { event.After.ResetAt = "2026-09-10T12:00:06Z" }},
		{name: "usage decrease", mutate: func(event *UsageHistoryEvent) { value := 19.998; event.After.UsedPercent = &value }},
		{name: "nan usage", mutate: func(event *UsageHistoryEvent) { value := math.NaN(); event.After.UsedPercent = &value }},
		{name: "infinite usage", mutate: func(event *UsageHistoryEvent) { value := math.Inf(1); event.After.UsedPercent = &value }},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := valid
			beforeCopy, afterCopy := *valid.Before, *valid.After
			event.Before, event.After = &beforeCopy, &afterCopy
			test.mutate(&event)
			events := []UsageHistoryEvent{event}
			if err := decorateUsageHistoryEvents(events); err != nil {
				t.Fatal(err)
			}
			if events[0].TimingNoise {
				t.Fatalf("event was tagged as timing noise: %+v", events[0])
			}
		})
	}
}

func TestUsageHistoryTimingNoiseAllowsUsedPercentRoundingThreshold(t *testing.T) {
	beforeUsed, afterUsed := 20.0, 19.999
	events := []UsageHistoryEvent{{
		ObservedAt: "2026-09-09T12:00:00Z", Provider: "claude", Kind: "window_changed_unknown",
		Before: &UsageHistoryValue{UsedPercent: &beforeUsed, ResetAt: "2026-09-10T12:00:00Z"},
		After:  &UsageHistoryValue{UsedPercent: &afterUsed, ResetAt: "2026-09-10T11:59:55Z"},
	}}
	if err := decorateUsageHistoryEvents(events); err != nil {
		t.Fatal(err)
	}
	if !events[0].TimingNoise {
		t.Fatalf("rounding-threshold event was not tagged: %+v", events[0])
	}
}

func assertLegacyTimingNoiseEvent(t *testing.T, event UsageHistoryEvent, groupID string, choices []string, explanation UsageHistoryExplanation) {
	t.Helper()
	if !event.TimingNoise || !event.Explainable || event.GroupID != groupID {
		t.Fatalf("decoration = %+v", event)
	}
	if event.Kind != "window_changed_unknown" || event.Confidence != "observed" ||
		event.Message != "Quota window timing changed; the cause is unknown." {
		t.Fatalf("legacy facts changed: %+v", event)
	}
	if !reflect.DeepEqual(event.ExplanationChoices, choices) {
		t.Fatalf("choices = %v, want %v", event.ExplanationChoices, choices)
	}
	if event.Explanation == nil || *event.Explanation != explanation {
		t.Fatalf("explanation = %+v, want %+v", event.Explanation, explanation)
	}
}
