package main

import (
	"encoding/gob"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func countTranscriptOpens(t *testing.T) *int {
	t.Helper()
	opens := 0
	original := openTranscript
	openTranscript = func(path string) (*os.File, error) {
		opens++
		return original(path)
	}
	t.Cleanup(func() { openTranscript = original })
	return &opens
}

func appendTranscript(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func claudeResultJSON(t *testing.T, events []tokenEvent, err error) string {
	t.Helper()
	message := ""
	if err != nil {
		message = err.Error()
	}
	data, marshalErr := json.Marshal(struct {
		Events []tokenEvent
		Err    string
	}{events, message})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	return string(data)
}

func assertClaudeMatchesUncached(t *testing.T, path string, events []tokenEvent, err error) {
	t.Helper()
	wantEvents, wantErr := readClaudeTranscript(path, nil)
	if got, want := claudeResultJSON(t, events, err), claudeResultJSON(t, wantEvents, wantErr); got != want {
		t.Fatalf("cached result differs from a full parse:\n got %s\nwant %s", got, want)
	}
}

// poisonClaudeCache changes a cached event so a later result shows whether the
// cached prefix was reused or the file was parsed again.
func poisonClaudeCache(t *testing.T, cache *transcriptParseCache, path string) {
	t.Helper()
	entry, ok := cache.data.Claude[path]
	if !ok || len(entry.Events) == 0 {
		t.Fatalf("no cached Claude events for %s", path)
	}
	entry.Events[0].Input = 999999
	cache.data.Claude[path] = entry
}

func TestClaudeTranscriptCacheReusesUnchangedFileWithoutReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude", "projects", "demo", "session.jsonl")
	writeClaudeTrackingTranscript(t, path,
		claudeTrackingLine("m1", "s1", "2026-09-08T10:00:00Z", 10, 2, 3),
		claudeTrackingLine("m2", "s1", "2026-09-08T10:01:00Z", 20, 4, 6),
	)
	cache := newTranscriptParseCache(filepath.Join(t.TempDir(), "cache.gob"))
	opens := countTranscriptOpens(t)

	first, err := readClaudeTranscript(path, cache)
	if err != nil || len(first) != 2 || *opens != 1 {
		t.Fatalf("first read: events=%d err=%v opens=%d", len(first), err, *opens)
	}
	second, err := readClaudeTranscript(path, cache)
	if *opens != 1 {
		t.Fatalf("unchanged file was opened again: opens=%d", *opens)
	}
	if !reflect.DeepEqual(first, second) || err != nil {
		t.Fatalf("cached events differ: %+v vs %+v (%v)", first, second, err)
	}
	assertClaudeMatchesUncached(t, path, second, err)
}

func TestClaudeTranscriptCacheParsesOnlyAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "demo", "session.jsonl")
	writeClaudeTrackingTranscript(t, path,
		claudeTrackingLine("m1", "s1", "2026-09-08T10:00:00Z", 10, 2, 3),
	)
	cache := newTranscriptParseCache(filepath.Join(t.TempDir(), "cache.gob"))
	if _, err := readClaudeTranscript(path, cache); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, path, claudeTrackingLine("m2", "s1", "2026-09-08T10:01:00Z", 20, 4, 6)+"\n"+
		claudeTrackingLine("m1", "s1", "2026-09-08T10:02:00Z", 30, 5, 3)+"\n")

	// An incremental parse of the grown file matches a full one.
	events, err := readClaudeTranscript(path, cache)
	if len(events) != 3 {
		t.Fatalf("events after append = %d", len(events))
	}
	assertClaudeMatchesUncached(t, path, events, err)
	if cache.data.Claude[path].File.Offset != cache.data.Claude[path].File.Size {
		t.Fatalf("offset after complete lines = %+v", cache.data.Claude[path].File)
	}

	// Only the appended bytes are parsed: a poisoned cached prefix survives.
	poisonClaudeCache(t, cache, path)
	appendTranscript(t, path, claudeTrackingLine("m3", "s1", "2026-09-08T10:03:00Z", 1, 1, 1)+"\n")
	events, _ = readClaudeTranscript(path, cache)
	if len(events) != 4 || events[0].Input != 999999 || events[3].Input != 1 {
		t.Fatalf("incremental parse re-read the prefix: %+v", events)
	}
}

func TestClaudeTranscriptCacheLeavesPartialFinalLineForLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "demo", "session.jsonl")
	writeClaudeTrackingTranscript(t, path, claudeTrackingLine("m1", "s1", "2026-09-08T10:00:00Z", 10, 2, 3))
	complete := claudeTrackingLine("m2", "s1", "2026-09-08T10:01:00Z", 20, 4, 6)
	appendTranscript(t, path, complete[:len(complete)/2])
	cache := newTranscriptParseCache(filepath.Join(t.TempDir(), "cache.gob"))

	events, err := readClaudeTranscript(path, cache)
	if len(events) != 1 || err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("partial line: events=%d err=%v", len(events), err)
	}
	assertClaudeMatchesUncached(t, path, events, err)
	entry := cache.data.Claude[path]
	if entry.Err != "" || entry.File.Offset >= entry.File.Size || len(entry.Events) != 1 {
		t.Fatalf("partial line was cached: %+v", entry)
	}

	// Completing the line without a newline parses it, still uncommitted.
	appendTranscript(t, path, complete[len(complete)/2:])
	events, err = readClaudeTranscript(path, cache)
	if len(events) != 2 || err != nil {
		t.Fatalf("completed line: events=%d err=%v", len(events), err)
	}
	assertClaudeMatchesUncached(t, path, events, err)
	if entry := cache.data.Claude[path]; len(entry.Events) != 1 || entry.File.Offset >= entry.File.Size {
		t.Fatalf("line without newline was committed: %+v", entry.File)
	}
	appendTranscript(t, path, "\n")
	events, err = readClaudeTranscript(path, cache)
	assertClaudeMatchesUncached(t, path, events, err)
	if entry := cache.data.Claude[path]; len(entry.Events) != 2 || entry.File.Offset != entry.File.Size {
		t.Fatalf("completed line was not committed: %+v", entry.File)
	}
}

func TestClaudeTranscriptCacheKeepsMalformedLineStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "demo", "session.jsonl")
	writeClaudeTrackingTranscript(t, path,
		claudeTrackingLine("m1", "s1", "2026-09-08T10:00:00Z", 10, 2, 3),
		`{"type":"assistant",broken}`,
		claudeTrackingLine("m2", "s1", "2026-09-08T10:01:00Z", 20, 4, 6),
	)
	cache := newTranscriptParseCache(filepath.Join(t.TempDir(), "cache.gob"))
	events, err := readClaudeTranscript(path, cache)
	if len(events) != 1 || err == nil {
		t.Fatalf("malformed line: events=%d err=%v", len(events), err)
	}
	assertClaudeMatchesUncached(t, path, events, err)

	appendTranscript(t, path, claudeTrackingLine("m3", "s1", "2026-09-08T10:02:00Z", 1, 1, 1)+"\n")
	events, err = readClaudeTranscript(path, cache)
	assertClaudeMatchesUncached(t, path, events, err)
}

func TestClaudeTranscriptCacheReparsesRewrittenOrTruncatedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "demo", "session.jsonl")
	writeClaudeTrackingTranscript(t, path,
		claudeTrackingLine("m1", "s1", "2026-09-08T10:00:00Z", 10, 2, 3),
		claudeTrackingLine("m2", "s1", "2026-09-08T10:01:00Z", 20, 4, 6),
	)
	cache := newTranscriptParseCache(filepath.Join(t.TempDir(), "cache.gob"))
	if _, err := readClaudeTranscript(path, cache); err != nil {
		t.Fatal(err)
	}

	// Rewritten with different content and a larger size.
	poisonClaudeCache(t, cache, path)
	writeClaudeTrackingTranscript(t, path,
		claudeTrackingLine("r1", "s2", "2026-09-08T11:00:00Z", 7, 1, 1),
		claudeTrackingLine("r2", "s2", "2026-09-08T11:01:00Z", 8, 1, 1),
		claudeTrackingLine("r3", "s2", "2026-09-08T11:02:00Z", 9, 1, 1),
	)
	events, err := readClaudeTranscript(path, cache)
	if len(events) != 3 || events[0].Input == 999999 {
		t.Fatalf("rewritten file reused the cache: %+v", events)
	}
	assertClaudeMatchesUncached(t, path, events, err)

	// Truncated.
	poisonClaudeCache(t, cache, path)
	writeClaudeTrackingTranscript(t, path, claudeTrackingLine("r1", "s2", "2026-09-08T11:00:00Z", 7, 1, 1))
	events, err = readClaudeTranscript(path, cache)
	if len(events) != 1 || events[0].Input != 7 {
		t.Fatalf("truncated file reused the cache: %+v", events)
	}
	assertClaudeMatchesUncached(t, path, events, err)
}

func TestCodexTranscriptCachePreservesSessionMetaAcrossAppends(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	root := t.TempDir()
	path := filepath.Join(root, "sessions", "rollout.jsonl")
	writeCodexTranscript(t, path, now,
		codexMetaLine("session-a", "2026-09-08T08:00:00Z"),
		codexTurnContextLine("gpt-test"),
		codexTokenLine("2026-09-08T08:01:00Z", testCodexUsage(100, 10, 40, 2), testCodexUsage(100, 10, 40, 2)),
	)
	cache := newTranscriptParseCache(filepath.Join(t.TempDir(), "cache.gob"))
	opens := countTranscriptOpens(t)
	readCodexTranscript(path, cache)
	readCodexTranscript(path, cache)
	if *opens != 1 {
		t.Fatalf("unchanged Codex transcript opened %d times", *opens)
	}

	appendTranscript(t, path, codexTokenLine("2026-09-08T09:00:00Z", testCodexUsage(150, 30, 60, 5), testCodexUsage(50, 20, 20, 3))+"\n"+
		codexTurnContextLine("gpt-next")+"\n"+
		codexTokenLine("2026-09-08T09:01:00Z", testCodexUsage(160, 31, 60, 5), testCodexUsage(10, 1, 0, 0))+"\n")
	cached := cache.data.Codex[path]
	cached.Snapshots[0].SourceID = "cached-prefix"
	cache.data.Codex[path] = cached

	snapshots, session, stats := readCodexTranscript(path, cache)
	wantSnapshots, wantSession, wantStats := readCodexTranscript(path, nil)
	if session.ID != "session-a" || !session.StartedAt.Equal(wantSession.StartedAt) || session.ID != wantSession.ID {
		t.Fatalf("session meta = %+v, want %+v", session, wantSession)
	}
	if stats != wantStats || len(snapshots) != 3 || len(wantSnapshots) != 3 {
		t.Fatalf("stats = %+v want %+v; snapshots %d/%d", stats, wantStats, len(snapshots), len(wantSnapshots))
	}
	if snapshots[0].SourceID != "cached-prefix" {
		t.Fatal("incremental Codex read re-parsed the cached prefix")
	}
	if snapshots[1].Model != "gpt-test" || snapshots[2].Model != "gpt-next" {
		t.Fatalf("models across the append = %q, %q", snapshots[1].Model, snapshots[2].Model)
	}
	for index := 1; index < 3; index++ {
		if !reflect.DeepEqual(snapshots[index], wantSnapshots[index]) {
			t.Fatalf("snapshot %d = %+v, want %+v", index, snapshots[index], wantSnapshots[index])
		}
	}

	// A partial final line is parsed like a full read but not committed.
	appendTranscript(t, path, `{"timestamp":"2026-09-08T09:02:00Z","type":"event_msg","payload":{"type":"token_count"`)
	_, _, stats = readCodexTranscript(path, cache)
	_, _, wantStats = readCodexTranscript(path, nil)
	if stats != wantStats || stats.Malformed != 1 {
		t.Fatalf("partial line stats = %+v, want %+v", stats, wantStats)
	}
	if entry := cache.data.Codex[path]; entry.Malformed != 0 || entry.File.Offset >= entry.File.Size {
		t.Fatalf("partial Codex line was committed: %+v", entry.File)
	}
}

func TestTranscriptParseCacheDropsUnusedEntriesAndSurvivesReload(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, "state", "transcript-parse-cache.gob")
	kept := filepath.Join(root, "projects", "demo", "kept.jsonl")
	removed := filepath.Join(root, "projects", "demo", "removed.jsonl")
	writeClaudeTrackingTranscript(t, kept, claudeTrackingLine("m1", "s1", "2026-09-08T10:00:00Z", 10, 2, 3))
	writeClaudeTrackingTranscript(t, removed, claudeTrackingLine("m2", "s2", "2026-09-08T10:00:00Z", 20, 4, 6))

	cache := loadTranscriptParseCache(cachePath)
	want, _ := readClaudeTranscript(kept, cache)
	readClaudeTranscript(removed, cache)
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cachePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode = %v, %v", info, err)
	}

	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	opens := countTranscriptOpens(t)
	cache = loadTranscriptParseCache(cachePath)
	got, err := readClaudeTranscript(kept, cache)
	if *opens != 0 || err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded cache: opens=%d err=%v got=%+v want=%+v", *opens, err, got, want)
	}
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	cache = loadTranscriptParseCache(cachePath)
	if _, ok := cache.data.Claude[removed]; ok || len(cache.data.Claude) != 1 {
		t.Fatalf("cache entries after removal = %d", len(cache.data.Claude))
	}
}

func TestTranscriptParseCacheRebuildsCorruptOrOtherVersionCache(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, "transcript-parse-cache.gob")
	path := filepath.Join(root, "projects", "demo", "session.jsonl")
	writeClaudeTrackingTranscript(t, path, claudeTrackingLine("m1", "s1", "2026-09-08T10:00:00Z", 10, 2, 3))

	if err := os.WriteFile(cachePath, []byte("not a gob stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := loadTranscriptParseCache(cachePath)
	if len(cache.data.Claude) != 0 || len(cache.data.Codex) != 0 {
		t.Fatal("corrupt cache was used")
	}
	events, err := readClaudeTranscript(path, cache)
	assertClaudeMatchesUncached(t, path, events, err)
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if reloaded := loadTranscriptParseCache(cachePath); len(reloaded.data.Claude) != 1 {
		t.Fatal("rebuilt cache was not saved")
	}

	old := cache.data
	old.Version = transcriptParseCacheVersion + 1
	file, err := os.Create(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(file).Encode(old); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if reloaded := loadTranscriptParseCache(cachePath); len(reloaded.data.Claude) != 0 {
		t.Fatal("other-version cache was used")
	}
}

// The cached path must produce the same summary token output as a full parse,
// cold, warm after a reload, and after transcripts grow.
func TestTranscriptParseCacheSummaryTokensMatchUncached(t *testing.T) {
	now := mustParseTime(t, "2026-09-08T12:00:00Z")
	env := setupTokenTrackingTest(t)
	cachePath := filepath.Join(t.TempDir(), "transcript-parse-cache.gob")
	claudeA := filepath.Join(env.claude, "projects", "alpha", "a.jsonl")
	claudeB := filepath.Join(env.claude, "projects", "beta", "b.jsonl")
	writeClaudeTrackingTranscript(t, claudeA,
		claudeTrackingLine("m1", "s1", "2026-09-07T10:00:00Z", 10, 2, 3),
		claudeTrackingLine("m1", "s1", "2026-09-07T10:00:05Z", 12, 5, 3),
	)
	writeClaudeTrackingTranscript(t, claudeB,
		claudeTrackingLine("m1", "s1", "2026-09-07T10:00:06Z", 12, 5, 3),
		claudeTrackingLine("m2", "s2", "2026-09-08T09:00:00Z", 100, 20, 30),
	)
	codexA := filepath.Join(env.codex, "sessions", "a.jsonl")
	codexFork := filepath.Join(env.codex, "archived_sessions", "fork.jsonl")
	writeCodexTranscript(t, codexA, now,
		codexMetaLine("session-a", "2026-09-07T08:00:00Z"),
		codexTurnContextLine("gpt-test"),
		codexTokenLine("2026-09-07T08:01:00Z", testCodexUsage(100, 10, 40, 2), testCodexUsage(100, 10, 40, 2)),
	)
	writeCodexTranscript(t, codexFork, now,
		codexMetaLine("child", "2026-09-08T10:00:00Z"),
		codexMetaLine("session-a", "2026-09-07T08:00:00Z"),
		codexTokenLine("2026-09-07T08:01:00Z", testCodexUsage(100, 10, 40, 2), testCodexUsage(100, 10, 40, 2)),
		codexTokenLine("2026-09-08T10:01:00Z", testCodexUsage(130, 20, 50, 2), testCodexUsage(30, 10, 10, 0)),
	)

	summarize := func(cache *transcriptParseCache) string {
		t.Helper()
		opts := options{PeriodDays: 7}
		codexEvents, codexStats := collectCodexTranscriptEvents(env.codex, now.AddDate(0, 0, -91), cache)
		codex := ProviderUsage{ID: "codex"}
		applyEvents(&codex, codexEvents, now, opts)

		var claudeEvents []tokenEvent
		var claudeErrors []string
		for _, path := range []string{claudeA, claudeB} {
			events, err := readClaudeTranscript(path, cache)
			if err != nil {
				claudeErrors = append(claudeErrors, err.Error())
			}
			claudeEvents = append(claudeEvents, events...)
		}
		claudeEvents, ambiguous := normalizeClaudeTokenEvents(claudeEvents)
		claude := ProviderUsage{ID: "claude"}
		applyEvents(&claude, claudeEvents, now, opts)

		tracking := scanAllTokenEvents(now, cache)
		data, err := json.Marshal(map[string]any{
			"codex": codex, "codexStats": codexStats, "codexEvents": codexEvents,
			"claude": claude, "claudeEvents": claudeEvents, "claudeErrors": claudeErrors, "ambiguous": ambiguous,
			"tracking": tracking,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	check := func(stage string) {
		t.Helper()
		want := summarize(nil)
		cache := loadTranscriptParseCache(cachePath)
		if got := summarize(cache); got != want {
			t.Fatalf("%s: cached summary differs\n got %s\nwant %s", stage, got, want)
		}
		if err := cache.save(); err != nil {
			t.Fatal(err)
		}
	}
	check("cold")
	check("warm")
	appendTranscript(t, claudeB, claudeTrackingLine("m3", "s2", "2026-09-08T11:00:00Z", 5, 5, 5)+"\n")
	appendTranscript(t, codexA, codexTokenLine("2026-09-08T11:00:00Z", testCodexUsage(140, 15, 40, 2), testCodexUsage(40, 5, 0, 0))+"\n")
	if err := os.Chtimes(codexA, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check("grown")
	check("warm again")
}

func TestClaudeTranscriptCacheReparsesSameSizeRewrite(t *testing.T) {
	// An interior edit far from both fingerprint windows that keeps the
	// length but moves the modification time is a rewrite, not an append.
	var lines []string
	for i := range 200 {
		input := int64(10)
		if i == 100 {
			input = 11
		}
		lines = append(lines, claudeTrackingLine("m"+strconv.Itoa(1000+i), "s1", "2026-09-08T10:00:00Z", input, 1, 1))
	}
	path := filepath.Join(t.TempDir(), "projects", "demo", "session.jsonl")
	writeClaudeTrackingTranscript(t, path, lines...)
	cache := newTranscriptParseCache(filepath.Join(t.TempDir(), "cache.gob"))
	if events, err := readClaudeTranscript(path, cache); err != nil || len(events) != 200 {
		t.Fatalf("initial parse: events=%d err=%v", len(events), err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(data), `"input_tokens":11`, `"input_tokens":91`, 1)
	if rewritten == string(data) || len(rewritten) != len(data) {
		t.Fatal("fixture must allow a same-length interior edit")
	}
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	events, err := readClaudeTranscript(path, cache)
	assertClaudeMatchesUncached(t, path, events, err)
	if events[100].Input != 91 {
		t.Fatalf("a same-size rewrite must be parsed again: input=%d", events[100].Input)
	}
}
