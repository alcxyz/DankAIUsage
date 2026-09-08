package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexTranscriptCumulativeSnapshotsAndWindows(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	path := filepath.Join(root, "sessions", "rollout.jsonl")
	writeCodexTranscript(t, path, now,
		codexMetaLine("session-a", "2026-09-01T08:00:00Z"),
		codexTurnContextLine("gpt-test"),
		codexTokenLine("2026-09-01T08:01:00Z", testCodexUsage(100, 10, 40, 2), testCodexUsage(100, 10, 40, 2)),
		codexTokenLine("2026-09-08T10:00:00Z", testCodexUsage(150, 30, 60, 5), testCodexUsage(50, 20, 20, 3)),
		codexTokenLine("2026-09-08T10:01:00Z", testCodexUsage(150, 30, 60, 5), testCodexUsage(50, 20, 20, 3)),
	)

	events, stats := collectCodexTranscriptEvents(root, now.AddDate(0, 0, -32))
	if stats.UsageRecords != 3 || stats.partial() || len(events) != 2 {
		t.Fatalf("stats = %+v, events = %d", stats, len(events))
	}
	provider := ProviderUsage{ID: "codex"}
	applyEvents(&provider, events, now, options{PeriodDays: 7, SessionHours: 5})
	if provider.Period.Input != 50 || provider.Period.Output != 20 || provider.Period.Cached != 20 || provider.Period.Reasoning != 3 || provider.Period.Requests != 1 {
		t.Fatalf("period totals = %+v", provider.Period)
	}
	if provider.Session.Total != 70 || provider.Today.Total != 70 || provider.Period.Sessions != 1 {
		t.Fatalf("window totals: session=%+v today=%+v period=%+v", provider.Session, provider.Today, provider.Period)
	}
	if len(provider.Models) != 1 || provider.Models[0].Model != "gpt-test" || provider.Models[0].Total != 70 {
		t.Fatalf("models = %+v", provider.Models)
	}
}

func TestCodexTranscriptForkResumeAndDuplicateFiles(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	first := []string{
		codexMetaLine("session-a", "2026-09-08T09:00:00Z"),
		codexTokenLine("2026-09-08T09:01:00Z", testCodexUsage(100, 10, 40, 2), testCodexUsage(100, 10, 40, 2)),
		codexTokenLine("2026-09-08T09:01:00Z", testCodexUsage(150, 30, 60, 5), testCodexUsage(50, 20, 20, 3)),
	}
	writeCodexTranscript(t, filepath.Join(root, "sessions", "a.jsonl"), now, first...)
	writeCodexTranscript(t, filepath.Join(root, "archived_sessions", "a.jsonl"), now, first...)
	writeCodexTranscript(t, filepath.Join(root, "sessions", "b.jsonl"), now,
		codexMetaLine("session-a", "2026-09-08T09:00:00Z"),
		codexTokenLine("2026-09-08T09:02:30Z", testCodexUsage(150, 30, 60, 5), testCodexUsage(50, 20, 20, 3)),
		codexTokenLine("2026-09-08T09:03:00Z", testCodexUsage(200, 45, 80, 7), testCodexUsage(50, 15, 20, 2)),
	)
	writeCodexTranscript(t, filepath.Join(root, "sessions", "fork.jsonl"), now,
		codexMetaLine("child", "2026-09-08T10:00:00Z"),
		codexMetaLine("parent", "2026-09-08T09:00:00Z"),
		first[1], first[2],
		codexTokenLine("2026-09-08T10:01:00Z", testCodexUsage(210, 50, 80, 8), testCodexUsage(60, 20, 20, 3)),
	)

	events, stats := collectCodexTranscriptEvents(root, now.Add(-24*time.Hour))
	if stats.partial() || len(events) != 4 {
		t.Fatalf("stats = %+v, events = %d", stats, len(events))
	}
	provider := ProviderUsage{ID: "codex"}
	applyEvents(&provider, events, now, options{PeriodDays: 7, SessionHours: 5})
	if provider.Period.Input != 260 || provider.Period.Output != 65 || provider.Period.Requests != 4 || provider.Period.Sessions != 2 {
		t.Fatalf("deduplicated totals = %+v", provider.Period)
	}
}

func TestCodexTranscriptCounterResetUsesLastUsage(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	writeCodexTranscript(t, filepath.Join(root, "sessions", "reset.jsonl"), now,
		codexMetaLine("session-a", "2026-09-08T10:00:00Z"),
		codexTokenLine("2026-09-08T10:01:00Z", testCodexUsage(100, 10, 40, 2), testCodexUsage(100, 10, 40, 2)),
		codexTokenLine("2026-09-08T10:02:00Z", testCodexUsage(150, 30, 60, 5), testCodexUsage(50, 20, 20, 3)),
		codexTokenLine("2026-09-08T10:03:00Z", testCodexUsage(30, 5, 10, 1), testCodexUsage(20, 4, 8, 1)),
	)

	events, stats := collectCodexTranscriptEvents(root, now.Add(-24*time.Hour))
	if stats.partial() || len(events) != 3 {
		t.Fatalf("stats = %+v, events = %d", stats, len(events))
	}
	provider := ProviderUsage{ID: "codex"}
	applyEvents(&provider, events, now, options{PeriodDays: 7, SessionHours: 5})
	if provider.Period.Input != 170 || provider.Period.Output != 34 || provider.Period.Cached != 68 || provider.Period.Reasoning != 6 {
		t.Fatalf("reset totals = %+v", provider.Period)
	}
}

func TestCodexTranscriptMalformedTruncatedLargeAndSpacedLines(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	spaced := strings.ReplaceAll(codexTokenLine("2026-09-08T10:01:00Z", testCodexUsage(10, 2, 4, 1), testCodexUsage(10, 2, 4, 1)), `":`, `" : `)
	largePrompt := `{"type":"response_item","payload":"` + strings.Repeat("x", codexTranscriptMaxLine+1) + `"}`
	writeCodexTranscript(t, filepath.Join(root, "sessions", "mixed.jsonl"), now,
		codexMetaLine("session-a", "2026-09-08T10:00:00Z"),
		spaced,
		`{"timestamp":"2026-09-08T10:02:00Z","type":"event_msg","payload":{"type":"token_count"`,
		largePrompt,
		codexTokenLine("2026-09-08T10:03:00Z", testCodexUsage(20, 5, 8, 2), testCodexUsage(10, 3, 4, 1)),
	)

	events, stats := collectCodexTranscriptEvents(root, now.Add(-24*time.Hour))
	if len(events) != 2 || stats.Malformed != 1 || stats.ReadFailures != 0 || !stats.partial() {
		t.Fatalf("stats = %+v, events = %d", stats, len(events))
	}
}

func TestCodexTranscriptUnsafeFirstTotalIsBaselineOnly(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	writeCodexTranscript(t, filepath.Join(root, "sessions", "baseline.jsonl"), now,
		codexMetaLine("session-a", "2026-09-08T10:00:00Z"),
		codexTokenLine("2026-09-08T10:01:00Z", testCodexUsage(1000, 100, 800, 20), nil),
		codexTokenLine("2026-09-08T10:02:00Z", testCodexUsage(1050, 120, 820, 23), nil),
	)

	events, stats := collectCodexTranscriptEvents(root, now.Add(-24*time.Hour))
	if len(events) != 1 || stats.Incomplete != 1 || events[0].Input != 50 || events[0].Output != 20 {
		t.Fatalf("stats = %+v, events = %+v", stats, events)
	}
}

func TestCodexTranscriptZeroBaselineAndMissingSessionMetadata(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	writeCodexTranscript(t, filepath.Join(root, "sessions", "zero.jsonl"), now,
		codexMetaLine("session-a", "2026-09-08T10:00:00Z"),
		codexTokenLine("2026-09-08T10:01:00Z", testCodexUsage(0, 0, 0, 0), nil),
	)
	writeCodexTranscript(t, filepath.Join(root, "sessions", "missing-meta.jsonl"), now,
		codexTokenLine("2026-09-08T10:02:00Z", testCodexUsage(10, 2, 0, 0), testCodexUsage(10, 2, 0, 0)),
	)

	events, stats := collectCodexTranscriptEvents(root, now.Add(-24*time.Hour))
	if len(events) != 1 || stats.UsableRecords != 2 || stats.Incomplete != 1 || stats.Malformed != 0 {
		t.Fatalf("stats = %+v, events = %d", stats, len(events))
	}
}

func TestCodexTranscriptInvalidUsageAndMissingSources(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	badTotal := testCodexUsage(-1, 2, 0, 0)
	badLast := testCodexUsage(1, -2, 0, 0)
	writeCodexTranscript(t, filepath.Join(root, "sessions", "invalid.jsonl"), now,
		codexMetaLine("session-a", "2026-09-08T10:00:00Z"),
		codexTokenLine("2026-09-08T10:01:00Z", badTotal, nil),
		codexTokenLine("2026-09-08T10:02:00Z", testCodexUsage(10, 2, 0, 0), badLast),
	)
	events, stats := collectCodexTranscriptEvents(root, now.Add(-24*time.Hour))
	if len(events) != 0 || stats.Malformed != 2 || stats.Incomplete != 1 || stats.UsageRecords != 1 {
		t.Fatalf("invalid stats = %+v, events = %d", stats, len(events))
	}

	missingEvents, missingStats := collectCodexTranscriptEvents(filepath.Join(root, "missing"), now.Add(-24*time.Hour))
	if len(missingEvents) != 0 || missingStats.SourcesFound != 0 || missingStats.partial() {
		t.Fatalf("missing stats = %+v, events = %d", missingStats, len(missingEvents))
	}
}

func testCodexUsage(input, output, cached, reasoning int64) map[string]int64 {
	return map[string]int64{
		"input_tokens": input, "output_tokens": output,
		"cached_input_tokens": cached, "reasoning_output_tokens": reasoning,
	}
}

func codexMetaLine(id, timestamp string) string {
	return marshalCodexTestLine(map[string]any{
		"timestamp": timestamp,
		"type":      "session_meta",
		"payload": map[string]any{
			"id": id, "timestamp": timestamp,
		},
	})
}

func codexTurnContextLine(model string) string {
	return marshalCodexTestLine(map[string]any{
		"type": "turn_context", "payload": map[string]any{"model": model},
	})
}

func codexTokenLine(timestamp string, total, last map[string]int64) string {
	return marshalCodexTestLine(map[string]any{
		"timestamp": timestamp,
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{"total_token_usage": total, "last_token_usage": last},
		},
	})
}

func marshalCodexTestLine(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func writeCodexTranscript(t *testing.T, path string, modified time.Time, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}
