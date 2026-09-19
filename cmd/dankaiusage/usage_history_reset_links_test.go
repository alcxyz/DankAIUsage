package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLinkPluginResetObservationsScreenshotChronology(t *testing.T) {
	events := pluginResetLinkFixture(t, true)
	original := cloneResetLinkEvents(t, events)

	linkPluginResetObservations(events)

	want := "2026-09-19T14:24:00Z"
	if events[0].PluginResetAt != "" || events[1].PluginResetAt != want || events[2].PluginResetAt != want {
		t.Fatalf("plugin reset links = [%q, %q, %q]", events[0].PluginResetAt, events[1].PluginResetAt, events[2].PluginResetAt)
	}
	for i := range events {
		events[i].PluginResetAt = ""
	}
	if !reflect.DeepEqual(events, original) {
		t.Fatalf("linking changed raw history:\n got: %#v\nwant: %#v", events, original)
	}
}

func TestLinkPluginResetObservationsSupportsLegacyEventsWithoutSampledAt(t *testing.T) {
	events := pluginResetLinkFixture(t, false)
	linkPluginResetObservations(events)
	if events[1].PluginResetAt == "" || events[2].PluginResetAt == "" {
		t.Fatalf("legacy events were not linked: %+v", events)
	}
}

func TestLinkPluginResetObservationsAllowsExpiryFlagWithCorroboratingFacts(t *testing.T) {
	events := pluginResetLinkFixture(t, true)
	events[2].ExpiryExplained = true
	linkPluginResetObservations(events)
	if events[1].PluginResetAt == "" || events[2].PluginResetAt == "" {
		t.Fatalf("expiry-flagged credit spend was not linked: %+v", events)
	}
}

func TestLinkPluginResetObservationsClearsStaleDerivedLinks(t *testing.T) {
	events := pluginResetLinkFixture(t, true)[1:]
	for index := range events {
		events[index].PluginResetAt = "2026-09-19T14:24:00Z"
	}
	linkPluginResetObservations(events)
	for _, event := range events {
		if event.PluginResetAt != "" {
			t.Fatalf("stale link was retained: %+v", event)
		}
	}
}

func TestLinkPluginResetObservationsRejectsUncorroboratedOrAmbiguousEvents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]UsageHistoryEvent) []UsageHistoryEvent
	}{
		{"missing credit observation", func(events []UsageHistoryEvent) []UsageHistoryEvent { return events[:2] }},
		{"two credits spent", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			*events[2].Before.AvailableCredits = 2
			return events
		}},
		{"credit observed at a different time", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[2].ObservedAt = "2026-09-19T14:30:00Z"
			return events
		}},
		{"credit came from a different sample", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[2].SampledAt = "2026-09-19T14:27:00Z"
			return events
		}},
		{"natural reset was due", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[1].Before.ResetAt = events[1].ObservedAt
			events[0].Before.ResetAt = events[1].ObservedAt
			return events
		}},
		{"unrelated provider", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[1].Provider = "claude"
			return events
		}},
		{"spark bucket", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[0].Bucket = "spark-weekly"
			events[1].Bucket = "spark-weekly"
			return events
		}},
		{"different general bucket", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[0].Bucket = "general-5-hour"
			return events
		}},
		{"different quota window", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[0].Before.ResetAt = "2026-09-24T14:14:00Z"
			return events
		}},
		{"action before observation interval", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[0].ObservedAt = events[1].PreviousObservedAt
			return events
		}},
		{"observation gap over one hour", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[1].PreviousObservedAt = "2026-09-19T13:28:59Z"
			events[2].PreviousObservedAt = events[1].PreviousObservedAt
			return events
		}},
		{"multiple successful actions", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			duplicate := events[0]
			duplicate.ObservedAt = "2026-09-19T14:25:00Z"
			return append(events, duplicate)
		}},
		{"already redeemed action makes success ambiguous", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			duplicate := events[0]
			duplicate.ObservedAt = "2026-09-19T14:25:00Z"
			duplicate.Kind = "plugin_reset_already_redeemed"
			return append(events, duplicate)
		}},
		{"duplicate credit observations", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			return append(events, events[2])
		}},
		{"one action matches multiple refill groups", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			refill := events[1]
			credit := events[2]
			refill.ObservedAt = "2026-09-19T14:30:00Z"
			refill.SampledAt = "2026-09-19T14:29:00Z"
			credit.ObservedAt = refill.ObservedAt
			credit.SampledAt = refill.SampledAt
			return append(events, refill, credit)
		}},
		{"already redeemed action", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[0].Kind = "plugin_reset_already_redeemed"
			return events
		}},
		{"failed action", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[0].Kind = "plugin_reset_no_credit"
			return events
		}},
		{"unknown action", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[0].Kind = "plugin_reset_attempt_unknown"
			events[0].Confidence = "observed"
			return events
		}},
		{"snapshot sampled before action", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			events[1].SampledAt = "2026-09-19T14:23:00Z"
			events[2].SampledAt = events[1].SampledAt
			return events
		}},
		{"action usage below prior observation", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			*events[0].Before.UsedPercent = 97
			return events
		}},
		{"allowance did not refill", func(events []UsageHistoryEvent) []UsageHistoryEvent {
			*events[1].After.UsedPercent = 99
			return events
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := test.mutate(pluginResetLinkFixture(t, true))
			linkPluginResetObservations(events)
			for _, event := range events {
				if event.PluginResetAt != "" {
					t.Fatalf("unexpected link on %s: %+v", event.Kind, event)
				}
			}
		})
	}
}

func TestReadUsageHistoryDecoratesLegacyPluginResetLinksWithoutChangingFacts(t *testing.T) {
	events := pluginResetLinkFixture(t, false)
	shiftResetLinkFixture(t, events, time.Now().UTC().Truncate(time.Second).Add(-time.Hour))
	explanation := &UsageHistoryExplanation{
		Reason:    "external_reset",
		Note:      "auto-use near expiry",
		UpdatedAt: historyTimestamp(time.Now()),
		Source:    "user",
	}
	groupID := usageHistoryGroupID("codex", events[1].ObservedAt)
	for _, index := range []int{1, 2} {
		events[index].GroupID = groupID
		copy := *explanation
		events[index].Explanation = &copy
	}
	original := cloneResetLinkEvents(t, events)
	state := usageHistoryState{
		Version:      usageHistoryLegacyVersion,
		Observations: map[string]usageHistoryObservation{},
		Credits:      map[string]usageHistoryCreditObservation{},
		Events:       events,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "usage-history.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readUsageHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got[1].PluginResetAt != events[0].ObservedAt || got[2].PluginResetAt != events[0].ObservedAt {
		t.Fatalf("read links = %+v", got)
	}
	if got[1].Explanation == nil || got[2].Explanation == nil || *got[1].Explanation != *explanation || *got[2].Explanation != *explanation {
		t.Fatalf("explanations changed: %+v", got)
	}
	for i := range got {
		got[i].PluginResetAt = ""
		got[i].Explainable = original[i].Explainable
		got[i].ExplanationChoices = original[i].ExplanationChoices
		got[i].TimingNoise = original[i].TimingNoise
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("read changed retained facts:\n got: %#v\nwant: %#v", got, original)
	}
}

func TestObserveUsageHistoryRecordsSampleTimeAndLinksNextFreshSnapshot(t *testing.T) {
	base := mustParseTime(t, "2026-09-19T14:14:00Z")
	resetAt := mustParseTime(t, "2026-09-23T14:14:00Z")
	path := filepath.Join(t.TempDir(), "usage-history.json")
	provider := historyProvider("codex", "general-weekly", "Weekly", 98, resetAt)
	setHistoryCredits(&provider, base.Add(24*time.Hour))
	provider.historyObservedAt = base
	if _, err := observeUsageHistory(path, base, []ProviderUsage{provider}); err != nil {
		t.Fatal(err)
	}
	if err := recordCodexResetHistory(path, codexResetHistoryRecord{
		ObservedAt:       base.Add(10 * time.Minute),
		BeforeObservedAt: base,
		Outcome:          "reset",
		Before:           generalHistoryLimits(98, resetAt),
	}); err != nil {
		t.Fatal(err)
	}

	provider = historyProvider("codex", "general-weekly", "Weekly", 0, resetAt.Add(7*24*time.Hour))
	setHistoryCredits(&provider)
	provider.historyObservedAt = base.Add(11 * time.Minute)
	events, err := observeUsageHistory(path, base.Add(15*time.Minute), []ProviderUsage{provider})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	wantSampledAt := historyTimestamp(provider.historyObservedAt)
	wantActionAt := historyTimestamp(base.Add(10 * time.Minute))
	for _, index := range []int{1, 2} {
		if events[index].SampledAt != wantSampledAt || events[index].PluginResetAt != wantActionAt {
			t.Fatalf("event %d = %+v", index, events[index])
		}
	}
}

func pluginResetLinkFixture(t *testing.T, sampled bool) []UsageHistoryEvent {
	t.Helper()
	actionUsed, used98, used0 := 98.0, 98.0, 0.0
	credits1, credits0 := 1, 0
	previous := "2026-09-19T14:14:00Z"
	observed := "2026-09-19T14:29:00Z"
	sampledAt := ""
	if sampled {
		sampledAt = "2026-09-19T14:28:00Z"
	}
	return []UsageHistoryEvent{
		{
			ObservedAt: "2026-09-19T14:24:00Z", PreviousObservedAt: previous,
			Provider: "codex", Bucket: "general-weekly", Label: "Weekly",
			Kind: "plugin_reset_reset", Source: "plugin_reset", Confidence: "confirmed",
			Before:  &UsageHistoryValue{UsedPercent: &actionUsed, ResetAt: "2026-09-23T14:14:00Z"},
			Message: "confirmed plugin action", ExplanationChoices: []string{},
		},
		{
			ObservedAt: observed, SampledAt: sampledAt, PreviousObservedAt: previous,
			Provider: "codex", Bucket: "general-weekly", Label: "Weekly",
			Kind: "allowance_increased_unknown", Source: "quota_observation", Confidence: "inferred",
			Before:  &UsageHistoryValue{UsedPercent: &used98, ResetAt: "2026-09-23T14:14:00Z"},
			After:   &UsageHistoryValue{UsedPercent: &used0, ResetAt: "2026-09-30T14:14:00Z"},
			Message: "possible provider reset", ExplanationChoices: []string{},
		},
		{
			ObservedAt: observed, SampledAt: sampledAt, PreviousObservedAt: previous,
			Provider: "codex", Bucket: "earned-resets", Label: "Earned resets",
			Kind: "credits_changed", Source: "quota_observation", Confidence: "observed",
			Before:  &UsageHistoryValue{AvailableCredits: &credits1},
			After:   &UsageHistoryValue{AvailableCredits: &credits0},
			Message: "available reset count changed", ExplanationChoices: []string{},
		},
	}
}

func cloneResetLinkEvents(t *testing.T, events []UsageHistoryEvent) []UsageHistoryEvent {
	t.Helper()
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	var clone []UsageHistoryEvent
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func shiftResetLinkFixture(t *testing.T, events []UsageHistoryEvent, previous time.Time) {
	t.Helper()
	originalPrevious := mustParseTime(t, events[1].PreviousObservedAt)
	delta := previous.Sub(originalPrevious)
	shift := func(value string) string {
		if value == "" {
			return ""
		}
		return historyTimestamp(mustParseTime(t, value).Add(delta))
	}
	for index := range events {
		events[index].ObservedAt = shift(events[index].ObservedAt)
		events[index].SampledAt = shift(events[index].SampledAt)
		events[index].PreviousObservedAt = shift(events[index].PreviousObservedAt)
		if events[index].Before != nil {
			events[index].Before.ResetAt = shift(events[index].Before.ResetAt)
		}
		if events[index].After != nil {
			events[index].After.ResetAt = shift(events[index].After.ResetAt)
		}
	}
}
