package main

import (
	"testing"
	"time"
)

func TestParseLogFields(t *testing.T) {
	fields := parseLogFields(`event.name="codex.sse_event" input_token_count=123 output_token_count=45 cached_token_count=100 conversation.id=abc`)

	if fields["event.name"] != "codex.sse_event" {
		t.Fatalf("event.name = %q", fields["event.name"])
	}
	if got := intField(fields, "input_token_count"); got != 123 {
		t.Fatalf("input_token_count = %d", got)
	}
	if fields["conversation.id"] != "abc" {
		t.Fatalf("conversation.id = %q", fields["conversation.id"])
	}
}

func TestEventTotalProviderSemantics(t *testing.T) {
	codex := tokenEvent{Provider: "codex", Input: 100, Output: 20, Cached: 80, Reasoning: 5, Tool: 10}
	if got := eventTotal(codex); got != 120 {
		t.Fatalf("codex total = %d", got)
	}

	claude := tokenEvent{Provider: "claude", Input: 100, Output: 20, Cached: 80}
	if got := eventTotal(claude); got != 200 {
		t.Fatalf("claude total = %d", got)
	}
}

func TestMakeAllowance(t *testing.T) {
	resetAt := mustParseTime(t, "2026-05-28T12:00:00Z")
	allowance := makeAllowance("session", 75, 100, resetAt)

	if !allowance.Known {
		t.Fatal("allowance should be known")
	}
	if allowance.Remaining != 25 {
		t.Fatalf("remaining = %d", allowance.Remaining)
	}
	if allowance.PercentRemaining != 25 {
		t.Fatalf("percent remaining = %f", allowance.PercentRemaining)
	}

	unknown := makeAllowance("weekly", 75, 0, resetAt)
	if unknown.Known {
		t.Fatal("zero limit should be unknown")
	}
}

func TestCodexSnapshotAllowances(t *testing.T) {
	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	sessionReset := int64(1779994842)
	weeklyReset := int64(1780528901)
	sessionMins := int64(300)
	weeklyMins := int64(10080)
	snapshot := codexRateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "pro",
		Primary: codexRateLimitWindow{
			UsedPercent:        12.5,
			WindowDurationMins: &sessionMins,
			ResetsAt:           &sessionReset,
		},
		Secondary: codexRateLimitWindow{
			UsedPercent:        44,
			WindowDurationMins: &weeklyMins,
			ResetsAt:           &weeklyReset,
		},
	}

	session := codexSnapshotAllowances(snapshot, now)
	if !session.Known || session.Unit != "percent" || session.PercentRemaining != 87.5 {
		t.Fatalf("session allowance = %+v", session)
	}
	if session.WindowMinutes != 300 {
		t.Fatalf("session window minutes = %d", session.WindowMinutes)
	}
	weekly := codexSnapshotWeeklyAllowance(snapshot, now)
	if !weekly.Known || weekly.PercentRemaining != 56 {
		t.Fatalf("weekly allowance = %+v", weekly)
	}
}

func TestParseClaudeStatusline(t *testing.T) {
	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	data := []byte(`{
		"version": "2.1.152",
		"model": {"display_name": "Sonnet"},
		"rate_limits": {
			"five_hour": {"used_percentage": 20, "resets_at": "2026-05-28T17:00:00Z"},
			"seven_day": {"usedPercent": 75, "resetsAt": 1780528901}
		}
	}`)

	limits, err := parseClaudeStatusline(data, now)
	if err != nil {
		t.Fatal(err)
	}
	if limits.Model != "Sonnet" || limits.Version != "2.1.152" {
		t.Fatalf("metadata = %+v", limits)
	}
	if limits.Session.PercentRemaining != 80 {
		t.Fatalf("session remaining = %f", limits.Session.PercentRemaining)
	}
	if limits.Weekly.PercentRemaining != 25 {
		t.Fatalf("weekly remaining = %f", limits.Weekly.PercentRemaining)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
