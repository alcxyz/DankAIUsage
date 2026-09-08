package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestApplyEventsExplicitRollingRanges(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	provider := ProviderUsage{ID: "codex"}
	events := []tokenEvent{
		{Provider: "codex", Timestamp: now.Add(-4 * time.Hour), Session: "a", Input: 1},
		{Provider: "codex", Timestamp: now.AddDate(0, 0, -6), Session: "b", Input: 2},
		{Provider: "codex", Timestamp: now.AddDate(0, 0, -29), Session: "c", Input: 4},
		{Provider: "codex", Timestamp: now.AddDate(0, 0, -89), Session: "d", Input: 8},
		{Provider: "codex", Timestamp: now.AddDate(0, 0, -91), Session: "e", Input: 16},
	}
	applyEvents(&provider, events, now, options{PeriodDays: 13, SessionHours: 9})
	if provider.Rolling.FiveHours.Total != 1 || provider.Rolling.FiveHours.Sessions != 1 {
		t.Fatalf("five hours = %+v", provider.Rolling.FiveHours)
	}
	if provider.Rolling.SevenDays.Total != 3 || provider.Rolling.SevenDays.Sessions != 2 {
		t.Fatalf("seven days = %+v", provider.Rolling.SevenDays)
	}
	if provider.Rolling.ThirtyDays.Total != 7 || provider.Rolling.ThirtyDays.Sessions != 3 {
		t.Fatalf("thirty days = %+v", provider.Rolling.ThirtyDays)
	}
	if provider.Rolling.NinetyDays.Total != 15 || provider.Rolling.NinetyDays.Sessions != 4 {
		t.Fatalf("ninety days = %+v", provider.Rolling.NinetyDays)
	}
	if provider.Session.Total != 1 || provider.Period.Total != 3 {
		t.Fatalf("configured compatibility ranges changed: session=%+v period=%+v", provider.Session, provider.Period)
	}
}

func TestTokenTrackingDefaultStatusDoesNotCreateFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state", "token-tracking.json")
	summary := readTokenTrackingSummary(path)
	if !summary.Known || summary.Enabled || summary.StartedAt != "" {
		t.Fatalf("default summary = %+v", summary)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("default status created state directory: %v", err)
	}
}

func TestTokenTrackingSeedDedupDeletionAndNoRawIDs(t *testing.T) {
	env := setupTokenTrackingTest(t)
	started := mustParseTime(t, "2026-09-08T12:00:00Z")
	codexLines := []string{
		codexMetaLine("private-codex-session", "2026-04-01T08:00:00Z"),
		codexTokenLine("2026-04-01T08:01:00Z", testCodexUsage(100, 10, 40, 2), testCodexUsage(100, 10, 40, 2)),
	}
	writeCodexTranscript(t, filepath.Join(env.codex, "sessions", "one.jsonl"), started, codexLines...)
	writeCodexTranscript(t, filepath.Join(env.codex, "archived_sessions", "copy.jsonl"), started, codexLines...)
	writeClaudeTrackingTranscript(t, filepath.Join(env.claude, "projects", "secret-project", "one.jsonl"),
		claudeTrackingLine("private-claude-message", "private-claude-session", "2026-04-02T08:00:00Z", 20, 5, 30))

	summary := enableTokenTracking(env.state, started)
	if !summary.Known || !summary.Enabled || summary.StartedAt == "" {
		t.Fatalf("enabled summary = %+v", summary)
	}
	if got := summary.Providers["codex"]; got.Input != 100 || got.Output != 10 || got.Total != 110 || got.Requests != 1 || got.Sessions != 1 {
		t.Fatalf("codex seed = %+v", got)
	}
	if got := summary.Providers["claude"]; got.Input != 20 || got.Output != 5 || got.Cached != 30 || got.Total != 55 || got.Requests != 1 || got.Sessions != 1 {
		t.Fatalf("claude seed = %+v", got)
	}

	refreshed := refreshTokenTracking(env.state, started.Add(time.Minute))
	if refreshed.Providers["codex"].Requests != 1 || refreshed.Providers["claude"].Requests != 1 {
		t.Fatalf("refresh double counted: %+v", refreshed.Providers)
	}
	if refreshed.UpdatedAt != summary.UpdatedAt {
		t.Fatalf("no-op refresh rewrote update time: before=%s after=%s", summary.UpdatedAt, refreshed.UpdatedAt)
	}
	restarted := readTokenTrackingSummary(env.state)
	if restarted.Providers["codex"].Total != 110 || restarted.Providers["claude"].Total != 55 {
		t.Fatalf("disk reload changed totals: %+v", restarted.Providers)
	}
	if err := os.RemoveAll(filepath.Join(env.codex, "sessions")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(env.codex, "archived_sessions")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(env.claude, "projects")); err != nil {
		t.Fatal(err)
	}
	deleted := refreshTokenTracking(env.state, started.Add(2*time.Minute))
	if deleted.Providers["codex"].Total != 110 || deleted.Providers["claude"].Total != 55 {
		t.Fatalf("deleted transcripts changed totals: %+v", deleted.Providers)
	}
	data, err := os.ReadFile(env.state)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"private-codex-session", "private-claude-message", "private-claude-session", "secret-project"} {
		if strings.Contains(string(data), raw) {
			t.Fatalf("tracking state persisted raw identifier %q", raw)
		}
	}
}

func TestTokenTrackingPauseResumeAndClear(t *testing.T) {
	env := setupTokenTrackingTest(t)
	start := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(env.codex, "sessions", "one.jsonl")
	lines := []string{
		codexMetaLine("session-a", "2026-09-08T11:00:00Z"),
		codexTokenLine("2026-09-08T11:01:00Z", testCodexUsage(10, 2, 0, 0), testCodexUsage(10, 2, 0, 0)),
	}
	writeCodexTranscript(t, path, start, lines...)
	if got := enableTokenTracking(env.state, start); !got.Enabled || got.Providers["codex"].Total != 12 {
		t.Fatalf("seed = %+v", got)
	}
	paused := pauseTokenTracking(env.state, start.Add(time.Minute))
	if paused.Enabled || paused.Providers["codex"].Total != 12 {
		t.Fatalf("pause = %+v", paused)
	}
	lines = append(lines,
		codexTokenLine("2026-09-08T12:02:00Z", testCodexUsage(30, 5, 0, 0), testCodexUsage(20, 3, 0, 0)))
	writeCodexTranscript(t, path, start.Add(2*time.Minute), lines...)
	stillPaused := refreshTokenTracking(env.state, start.Add(2*time.Minute))
	if stillPaused.Providers["codex"].Total != 12 {
		t.Fatalf("paused refresh changed total: %+v", stillPaused)
	}
	resumed := enableTokenTracking(env.state, start.Add(3*time.Minute))
	if !resumed.Enabled || resumed.Providers["codex"].Total != 12 {
		t.Fatalf("resume = %+v", resumed)
	}
	lines = append(lines,
		codexTokenLine("2026-09-08T12:04:00Z", testCodexUsage(37, 9, 0, 0), testCodexUsage(7, 4, 0, 0)))
	writeCodexTranscript(t, path, start.Add(4*time.Minute), lines...)
	after := refreshTokenTracking(env.state, start.Add(5*time.Minute))
	if after.Providers["codex"].Total != 23 || after.Providers["codex"].Requests != 2 {
		t.Fatalf("resume caught up paused usage or missed new usage: %+v", after.Providers["codex"])
	}
	cleared := clearTokenTracking(env.state, start.Add(6*time.Minute))
	if cleared.Enabled || cleared.StartedAt != "" || cleared.Providers["codex"].Total != 0 || cleared.Providers["claude"].Total != 0 {
		t.Fatalf("clear = %+v", cleared)
	}
}

func TestTokenTrackingClaudeStreamRevisionsUseMonotonicMaximum(t *testing.T) {
	env := setupTokenTrackingTest(t)
	start := mustParseTime(t, "2026-09-08T12:00:00Z")
	writeClaudeTrackingTranscript(t, filepath.Join(env.claude, "projects", "p", "stream.jsonl"),
		claudeTrackingLine("message-a", "session-a", "2026-09-08T11:00:00Z", 10, 0, 5),
		claudeTrackingLine("message-a", "session-a", "2026-09-08T11:00:01Z", 10, 8, 5),
		claudeTrackingLine("message-a", "session-a", "2026-09-08T11:00:02Z", 10, 2, 5))
	summary := enableTokenTracking(env.state, start)
	got := summary.Providers["claude"]
	if got.Input != 10 || got.Output != 8 || got.Cached != 5 || got.Total != 23 || got.Requests != 1 {
		t.Fatalf("stream revisions = %+v", got)
	}
}

func TestClaudeRollingRangesDeduplicateStreamRevisions(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	stableID := "message-a"
	key := tokenIdentityHash("claude-event", stableID)
	events := []tokenEvent{
		{Provider: "claude", Timestamp: now.Add(-time.Hour), Session: "a", TrackKey: key, Input: 10, Output: 0, Cached: 5},
		{Provider: "claude", Timestamp: now.Add(-time.Hour + time.Second), Session: "a", TrackKey: key, Input: 10, Output: 8, Cached: 5},
		{Provider: "claude", Timestamp: now.Add(-time.Hour + 2*time.Second), Session: "a", TrackKey: key, Input: 10, Output: 2, Cached: 5},
	}
	normalized, ambiguous := normalizeClaudeTokenEvents(events)
	if ambiguous != 0 || len(normalized) != 1 {
		t.Fatalf("normalized = %+v, ambiguous = %d", normalized, ambiguous)
	}
	provider := ProviderUsage{ID: "claude"}
	applyEvents(&provider, normalized, now, options{PeriodDays: 7, SessionHours: 5})
	if provider.Rolling.FiveHours.Total != 23 || provider.Rolling.FiveHours.Requests != 1 {
		t.Fatalf("rolling duplicate total = %+v", provider.Rolling.FiveHours)
	}
}

func TestTokenTrackingPartialSeedAndCorruptStateRecovery(t *testing.T) {
	env := setupTokenTrackingTest(t)
	start := mustParseTime(t, "2026-09-08T12:00:00Z")
	writeClaudeTrackingTranscript(t, filepath.Join(env.claude, "projects", "p", "partial.jsonl"),
		claudeTrackingLine("message-a", "session-a", "2026-09-08T11:00:00Z", 10, 2, 3),
		`{"type":"assistant"`)
	summary := enableTokenTracking(env.state, start)
	if !summary.Known || !summary.Enabled || summary.Providers["claude"].Total != 15 || len(summary.Errors) == 0 {
		t.Fatalf("partial seed = %+v", summary)
	}

	if err := os.WriteFile(env.state, []byte(`{"version":1,"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := readTokenTrackingSummary(env.state)
	if corrupt.Known || corrupt.Enabled || len(corrupt.Errors) == 0 {
		t.Fatalf("corrupt state did not fail closed: %+v", corrupt)
	}
	recovered := clearTokenTracking(env.state, start.Add(time.Minute))
	if !recovered.Known || recovered.Enabled || recovered.Providers["claude"].Total != 0 {
		t.Fatalf("clear did not recover corrupt state: %+v", recovered)
	}
}

func TestTokenTrackingConcurrentRefreshCountsOnce(t *testing.T) {
	env := setupTokenTrackingTest(t)
	start := mustParseTime(t, "2026-09-08T12:00:00Z")
	path := filepath.Join(env.codex, "sessions", "one.jsonl")
	lines := []string{
		codexMetaLine("session-a", "2026-09-08T11:00:00Z"),
		codexTokenLine("2026-09-08T11:01:00Z", testCodexUsage(10, 2, 0, 0), testCodexUsage(10, 2, 0, 0)),
	}
	writeCodexTranscript(t, path, start, lines...)
	enableTokenTracking(env.state, start)
	lines = append(lines, codexTokenLine("2026-09-08T12:01:00Z", testCodexUsage(20, 5, 0, 0), testCodexUsage(10, 3, 0, 0)))
	writeCodexTranscript(t, path, start.Add(time.Minute), lines...)

	results := make(chan TrackingSummary, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- refreshTokenTracking(env.state, start.Add(2*time.Minute))
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if !result.Known || result.Providers["codex"].Total != 25 || result.Providers["codex"].Requests != 2 {
			t.Fatalf("concurrent result = %+v", result)
		}
	}
	final := readTokenTrackingSummary(env.state)
	if final.Providers["codex"].Total != 25 || final.Providers["codex"].Requests != 2 {
		t.Fatalf("concurrent refresh double counted: %+v", final)
	}
}

func TestTokenTrackingStaleGenerationsCannotCommit(t *testing.T) {
	env := setupTokenTrackingTest(t)
	start := mustParseTime(t, "2026-09-08T12:00:00Z")
	enabled := enableTokenTracking(env.state, start)
	if !enabled.Enabled {
		t.Fatalf("enable = %+v", enabled)
	}
	state, err := loadTokenTrackingState(env.state)
	if err != nil {
		t.Fatal(err)
	}
	staleEpoch := state.Epoch
	staleScan := tokenTrackingScan{Events: []tokenEvent{{
		Provider: "codex", Timestamp: start.Add(10 * time.Minute),
		TrackKey: tokenIdentityHash("stale-one"), TrackFingerprint: tokenIdentityHash("stale-one-fingerprint"), Input: 999,
	}}}
	pauseTokenTracking(env.state, start.Add(time.Minute))
	resumed := enableTokenTracking(env.state, start.Add(2*time.Minute))
	if !resumed.Enabled {
		t.Fatalf("resume = %+v", resumed)
	}
	blocked := commitTokenTrackingRefresh(env.state, staleEpoch, staleScan, start.Add(11*time.Minute))
	if blocked.Providers["codex"].Total != 0 {
		t.Fatalf("pre-pause scan committed after resume: %+v", blocked)
	}

	state, err = loadTokenTrackingState(env.state)
	if err != nil {
		t.Fatal(err)
	}
	preClearEpoch := state.Epoch
	clearTokenTracking(env.state, start.Add(12*time.Minute))
	enableTokenTracking(env.state, start.Add(13*time.Minute))
	blocked = commitTokenTrackingRefresh(env.state, preClearEpoch, staleScan, start.Add(14*time.Minute))
	if blocked.Providers["codex"].Total != 0 {
		t.Fatalf("pre-clear scan committed into new period: %+v", blocked)
	}
}

func TestTokenTrackingInterruptedSeedCannotCommitAfterPause(t *testing.T) {
	env := setupTokenTrackingTest(t)
	start := mustParseTime(t, "2026-09-08T12:00:00Z")
	epoch := tokenIdentityHash("seed-epoch")
	state := defaultTokenTrackingState()
	state.Epoch = epoch
	state.Seeding = true
	state.StartedAt = start.Format(time.RFC3339Nano)
	if err := withTokenTrackingLock(env.state, func() error { return saveTokenTrackingState(env.state, state) }); err != nil {
		t.Fatal(err)
	}
	paused := pauseTokenTracking(env.state, start.Add(time.Minute))
	if paused.Enabled || len(paused.Errors) == 0 {
		t.Fatalf("interrupted seed pause = %+v", paused)
	}
	staleSeed := tokenTrackingScan{Events: []tokenEvent{{
		Provider: "codex", Timestamp: start.Add(-time.Minute),
		TrackKey: tokenIdentityHash("seed-event"), TrackFingerprint: tokenIdentityHash("seed-fingerprint"), Input: 10,
	}}}
	result := commitInitialTokenTrackingSeed(env.state, epoch, staleSeed, start)
	if result.Enabled || result.Providers["codex"].Total != 0 || len(result.Errors) == 0 {
		t.Fatalf("stale initial seed committed: %+v", result)
	}
}

func TestTokenTrackingAtomicWriteFailurePreservesStateAndPermissions(t *testing.T) {
	env := setupTokenTrackingTest(t)
	start := mustParseTime(t, "2026-09-08T12:00:00Z")
	enableTokenTracking(env.state, start)
	before, err := os.ReadFile(env.state)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(env.state); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions: info=%v err=%v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(env.state)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions: info=%v err=%v", info, err)
	}

	originalRename := tokenTrackingRename
	tokenTrackingRename = func(_, _ string) error { return errors.New("synthetic rename failure") }
	failed := pauseTokenTracking(env.state, start.Add(time.Minute))
	tokenTrackingRename = originalRename
	if failed.Known {
		t.Fatalf("write failure did not fail closed: %+v", failed)
	}
	after, err := os.ReadFile(env.state)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed atomic write changed the previous state")
	}
	loaded := readTokenTrackingSummary(env.state)
	if !loaded.Known || !loaded.Enabled {
		t.Fatalf("previous state was not preserved: %+v", loaded)
	}
}

func TestTokenTrackingRejectsFutureEventsAndCodexTimestampRewrite(t *testing.T) {
	state := defaultTokenTrackingState()
	state.Epoch = tokenIdentityHash("epoch")
	state.Enabled = true
	state.StartedAt = "2026-09-08T12:00:00Z"
	boundary := mustParseTime(t, "2026-09-08T12:00:00Z")
	key := tokenIdentityHash("event")
	firstFingerprint := tokenIdentityHash("snapshot-one")
	secondFingerprint := tokenIdentityHash("snapshot-two")
	applyTokenTrackingScan(&state, tokenTrackingScan{Events: []tokenEvent{
		{Provider: "codex", Timestamp: boundary.Add(-time.Minute), TrackKey: key, TrackFingerprint: firstFingerprint, Input: 10},
		{Provider: "codex", Timestamp: boundary.Add(-time.Minute), TrackKey: key, TrackFingerprint: secondFingerprint, Input: 20},
		{Provider: "codex", Timestamp: boundary.Add(time.Minute), TrackKey: tokenIdentityHash("future"), TrackFingerprint: tokenIdentityHash("future-fingerprint"), Input: 30},
	}}, boundary)
	summary := tokenTrackingSummary(state)
	if summary.Providers["codex"].Total != 10 || summary.Providers["codex"].Requests != 1 || len(summary.Errors) == 0 {
		t.Fatalf("rewrite/future handling = %+v", summary)
	}
}

func TestCodexTrackingAcceptsSharedTimestampsAndGuardsLaterChanges(t *testing.T) {
	boundary := mustParseTime(t, "2026-09-08T12:00:00Z")
	session := "session-a"
	snapshots := []codexUsageSnapshot{
		{Timestamp: boundary.Add(-time.Minute), Total: codexTokenUsage{Input: 10}, Last: codexTokenUsage{Input: 10}, HasLast: true},
		{Timestamp: boundary.Add(-time.Minute), Total: codexTokenUsage{Input: 20}, Last: codexTokenUsage{Input: 10}, HasLast: true},
	}
	events, incomplete := codexEventsFromSnapshots(session, snapshots, true)
	if incomplete != 0 || len(events) != 2 || events[0].TrackKey == events[1].TrackKey {
		t.Fatalf("shared timestamp identities: events=%+v incomplete=%d", events, incomplete)
	}
	state := defaultTokenTrackingState()
	state.Epoch = tokenIdentityHash("epoch")
	state.Enabled = true
	state.StartedAt = boundary.Format(time.RFC3339Nano)
	if !applyTokenTrackingScan(&state, tokenTrackingScan{Events: events}, boundary) {
		t.Fatal("initial same-timestamp scan reported no change")
	}
	seeded := tokenTrackingSummary(state)
	if seeded.Providers["codex"].Total != 20 || seeded.Providers["codex"].Requests != 2 || len(seeded.Errors) != 0 {
		t.Fatalf("same-timestamp seed = %+v", seeded)
	}
	if applyTokenTrackingScan(&state, tokenTrackingScan{Events: events}, boundary) {
		t.Fatal("identical refresh changed checkpoints")
	}

	// The raw snapshot key remains stable even if a retention-limited parser
	// would derive a different contribution from it on a later scan.
	changedDelta := events[1]
	changedDelta.Input = 999
	if applyTokenTrackingScan(&state, tokenTrackingScan{Events: []tokenEvent{changedDelta}}, boundary) {
		t.Fatal("retention-derived delta changed an existing snapshot checkpoint")
	}
	if got := tokenTrackingSummary(state).Providers["codex"].Total; got != 20 {
		t.Fatalf("retention-derived delta changed total to %d", got)
	}

	late := tokenEvent{
		Provider: "codex", Timestamp: boundary.Add(-time.Minute), Session: session,
		TrackKey: tokenIdentityHash("unseen-late-snapshot"), TrackFingerprint: tokenIdentityHash("unseen-late-fingerprint"), Input: 100,
	}
	newer := tokenEvent{
		Provider: "codex", Timestamp: boundary.Add(time.Second), Session: session,
		TrackKey: tokenIdentityHash("newer-snapshot"), TrackFingerprint: tokenIdentityHash("newer-fingerprint"), Input: 7,
	}
	if !applyTokenTrackingScan(&state, tokenTrackingScan{Events: []tokenEvent{late, newer}}, boundary.Add(time.Minute)) {
		t.Fatal("late/new scan reported no state change")
	}
	guarded := tokenTrackingSummary(state)
	if guarded.Providers["codex"].Total != 27 || guarded.Providers["codex"].Requests != 3 || len(guarded.Errors) == 0 {
		t.Fatalf("late/new checkpoint handling = %+v", guarded)
	}
}

type tokenTrackingTestEnv struct {
	state  string
	codex  string
	claude string
}

func setupTokenTrackingTest(t *testing.T) tokenTrackingTestEnv {
	t.Helper()
	root := t.TempDir()
	env := tokenTrackingTestEnv{
		state:  filepath.Join(root, "state", "dankaiusage", "token-tracking.json"),
		codex:  filepath.Join(root, "codex"),
		claude: filepath.Join(root, "claude"),
	}
	t.Setenv("CODEX_HOME", env.codex)
	t.Setenv("CLAUDE_CONFIG_DIR", env.claude)
	return env
}

func claudeTrackingLine(messageID, sessionID, timestamp string, input, output, cached int64) string {
	row := map[string]any{
		"type":      "assistant",
		"timestamp": timestamp,
		"uuid":      messageID,
		"sessionId": sessionID,
		"message": map[string]any{
			"model": "claude-test",
			"usage": map[string]any{
				"input_tokens":                input,
				"output_tokens":               output,
				"cache_creation_input_tokens": cached,
				"cache_read_input_tokens":     0,
			},
		},
	}
	data, _ := json.Marshal(row)
	return string(data)
}

func writeClaudeTrackingTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
