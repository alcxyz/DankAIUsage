package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const codexTranscriptMaxLine = 1024 * 1024

var (
	codexTokenRecordMarker = []byte(`token_count`)
	codexSessionMetaMarker = []byte(`session_meta`)
	codexTurnContextMarker = []byte(`turn_context`)
)

type codexTranscriptStats struct {
	SourcesFound  int
	FilesFound    int
	FilesScanned  int
	UsageRecords  int
	UsableRecords int
	Malformed     int
	Incomplete    int
	ReadFailures  int
}

func (stats codexTranscriptStats) partial() bool {
	return stats.Malformed > 0 || stats.Incomplete > 0 || stats.ReadFailures > 0
}

type codexTokenUsage struct {
	Input     int64
	Output    int64
	Cached    int64
	Reasoning int64
}

func (usage codexTokenUsage) equal(other codexTokenUsage) bool {
	return usage == other
}

func (usage codexTokenUsage) atLeast(other codexTokenUsage) bool {
	return usage.Input >= other.Input &&
		usage.Output >= other.Output &&
		usage.Cached >= other.Cached &&
		usage.Reasoning >= other.Reasoning
}

func (usage codexTokenUsage) subtract(other codexTokenUsage) codexTokenUsage {
	return codexTokenUsage{
		Input:     usage.Input - other.Input,
		Output:    usage.Output - other.Output,
		Cached:    usage.Cached - other.Cached,
		Reasoning: usage.Reasoning - other.Reasoning,
	}
}

func (usage codexTokenUsage) hasTokens() bool {
	return usage.Input > 0 || usage.Output > 0 || usage.Cached > 0 || usage.Reasoning > 0
}

type codexWireUsage struct {
	Input     *int64 `json:"input_tokens"`
	Output    *int64 `json:"output_tokens"`
	Cached    *int64 `json:"cached_input_tokens"`
	Reasoning *int64 `json:"reasoning_output_tokens"`
}

func (usage *codexWireUsage) value() (codexTokenUsage, bool) {
	if usage == nil || (usage.Input == nil && usage.Output == nil && usage.Cached == nil && usage.Reasoning == nil) {
		return codexTokenUsage{}, false
	}
	values := []*int64{usage.Input, usage.Output, usage.Cached, usage.Reasoning}
	for _, value := range values {
		if value == nil {
			continue
		}
		if *value < 0 {
			return codexTokenUsage{}, false
		}
	}
	result := codexTokenUsage{}
	if usage.Input != nil {
		result.Input = *usage.Input
	}
	if usage.Output != nil {
		result.Output = *usage.Output
	}
	if usage.Cached != nil {
		result.Cached = *usage.Cached
	}
	if usage.Reasoning != nil {
		result.Reasoning = *usage.Reasoning
	}
	return result, true
}

type codexTranscriptRow struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Timestamp string `json:"timestamp"`
		ID        string `json:"id"`
		Model     string `json:"model"`
		Type      string `json:"type"`
		Info      *struct {
			Total *codexWireUsage `json:"total_token_usage"`
			Last  *codexWireUsage `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

type codexUsageSnapshot struct {
	Timestamp time.Time
	Total     codexTokenUsage
	Last      codexTokenUsage
	HasLast   bool
	Model     string
	SourceID  string
}

type codexSnapshotKey struct {
	Timestamp int64
	Total     codexTokenUsage
	Last      codexTokenUsage
	HasLast   bool
	SourceID  string
}

type codexSessionMeta struct {
	ID        string
	StartedAt time.Time
}

func collectCodexTranscriptEvents(root string, cutoff time.Time, cache *transcriptParseCache) ([]tokenEvent, codexTranscriptStats) {
	stats := codexTranscriptStats{}
	var files []string
	for _, source := range []string{filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions")} {
		info, err := os.Stat(source)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				stats.ReadFailures++
			}
			continue
		}
		if !info.IsDir() {
			stats.ReadFailures++
			continue
		}
		stats.SourcesFound++
		err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				stats.ReadFailures++
				return nil
			}
			if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			stats.FilesFound++
			fileInfo, statErr := entry.Info()
			if statErr != nil {
				stats.ReadFailures++
				return nil
			}
			if fileInfo.ModTime().Before(cutoff) {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			stats.ReadFailures++
		}
	}

	sort.Strings(files)
	bySession := make(map[string][]codexUsageSnapshot)
	trackableSessions := make(map[string]bool)
	var events []tokenEvent
	for _, path := range files {
		snapshots, session, fileStats := readCodexTranscript(path, cache)
		stats.FilesScanned++
		stats.UsageRecords += fileStats.UsageRecords
		stats.Malformed += fileStats.Malformed
		stats.ReadFailures += fileStats.ReadFailures
		sessionKey := session.ID
		if sessionKey == "" {
			sessionKey = path
		} else {
			trackableSessions[sessionKey] = true
		}
		if len(snapshots) > 0 && (session.ID == "" || session.StartedAt.IsZero()) {
			stats.Incomplete++
		}
		for _, snapshot := range snapshots {
			if !session.StartedAt.IsZero() && snapshot.Timestamp.Before(session.StartedAt) {
				continue
			}
			bySession[sessionKey] = append(bySession[sessionKey], snapshot)
			stats.UsableRecords++
		}
	}
	for session, snapshots := range bySession {
		sort.SliceStable(snapshots, func(i, j int) bool {
			return snapshots[i].Timestamp.Before(snapshots[j].Timestamp)
		})
		sessionEvents, incomplete := codexEventsFromSnapshots(session, snapshots, trackableSessions[session])
		stats.Incomplete += incomplete
		events = append(events, sessionEvents...)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
	return events, stats
}

// codexTranscriptParser holds the state of one file's line-by-line parse.
// Full and incremental reads use the same parser; an incremental read resumes
// from the state saved after the last complete line.
type codexTranscriptParser struct {
	snapshots      []codexUsageSnapshot
	session        codexSessionMeta
	hasSessionMeta bool
	model          string
	usageRecords   int
	malformed      int
}

func (parser *codexTranscriptParser) parseLine(line []byte) {
	if !bytes.Contains(line, codexTokenRecordMarker) &&
		!bytes.Contains(line, codexSessionMetaMarker) &&
		!bytes.Contains(line, codexTurnContextMarker) {
		return
	}
	var row codexTranscriptRow
	if json.Unmarshal(line, &row) != nil {
		parser.malformed++
		return
	}
	if row.Type == "session_meta" {
		// Fork rollouts begin with their own metadata, followed by copied
		// parent metadata and history. Only the first header owns this file.
		if parser.hasSessionMeta {
			return
		}
		parser.hasSessionMeta = true
		parser.session = codexSessionMeta{ID: row.Payload.ID}
		if timestamp, ok := parseTime(firstNonEmpty(row.Payload.Timestamp, row.Timestamp)); ok {
			parser.session.StartedAt = timestamp
		} else {
			parser.malformed++
		}
		parser.model = ""
		return
	}
	if row.Type == "turn_context" {
		parser.model = row.Payload.Model
		return
	}
	if row.Type != "event_msg" || row.Payload.Type != "token_count" || row.Payload.Info == nil {
		return
	}
	timestamp, ok := parseTime(row.Timestamp)
	if !ok {
		parser.malformed++
		return
	}
	total, ok := row.Payload.Info.Total.value()
	if !ok {
		if row.Payload.Info.Total != nil {
			parser.malformed++
		}
		return
	}
	last, hasLast := row.Payload.Info.Last.value()
	if row.Payload.Info.Last != nil && !hasLast {
		parser.malformed++
	}
	parser.usageRecords++
	parser.snapshots = append(parser.snapshots, codexUsageSnapshot{
		Timestamp: timestamp, Total: total, Last: last, HasLast: hasLast, Model: parser.model, SourceID: row.Payload.ID,
	})
}

func (parser codexTranscriptParser) result(readFailures int) ([]codexUsageSnapshot, codexSessionMeta, codexTranscriptStats) {
	stats := codexTranscriptStats{
		UsageRecords: parser.usageRecords,
		Malformed:    parser.malformed,
		ReadFailures: readFailures,
	}
	return parser.snapshots, parser.session, stats
}

// readCodexTranscript parses one transcript, reusing and extending its cache
// entry when cache is not nil. A final line without a newline is parsed but
// not cached, so it is parsed again once complete.
func readCodexTranscript(path string, cache *transcriptParseCache) ([]codexUsageSnapshot, codexSessionMeta, codexTranscriptStats) {
	var cached codexTranscriptCacheEntry
	hasCached := false
	size, modTime, regular := int64(0), int64(0), false
	if cache != nil {
		size, modTime, regular = statTranscript(path)
		if regular {
			cached, hasCached = cache.codexEntry(path)
			if hasCached && cached.File.Size == size && cached.File.ModTime == modTime && cached.File.Offset == size {
				return cached.parser().result(0)
			}
		} else {
			cache.forgetCodex(path)
		}
	}
	file, err := openTranscript(path)
	if err != nil {
		if cache != nil {
			cache.forgetCodex(path)
		}
		return nil, codexSessionMeta{}, codexTranscriptStats{ReadFailures: 1}
	}
	defer file.Close()

	parser := codexTranscriptParser{}
	start := int64(0)
	if hasCached && transcriptPrefixUnchanged(file, cached.File, size, modTime) {
		parser = cached.parser()
		start = cached.File.Offset
	}
	var reader io.Reader = file
	if regular {
		reader = io.NewSectionReader(file, start, size-start)
	}
	// The final line without a newline is parsed by a copy of the parser, so
	// the cached state stays at the last complete line.
	var final codexTranscriptParser
	hasFinal := false
	consumed, err := readTranscriptLines(reader, codexTranscriptMaxLine, func(line []byte, complete bool) bool {
		if complete {
			parser.parseLine(line)
			return true
		}
		final = parser
		final.snapshots = parser.snapshots[:len(parser.snapshots):len(parser.snapshots)]
		final.parseLine(line)
		hasFinal = true
		return false
	})
	if err != nil {
		if cache != nil {
			cache.forgetCodex(path)
		}
		return parser.result(1)
	}
	if cache != nil && regular {
		storeCodexCacheEntry(cache, path, file, parser, size, modTime, start+consumed)
	}
	if hasFinal {
		return final.result(0)
	}
	return parser.result(0)
}

func storeCodexCacheEntry(cache *transcriptParseCache, path string, file io.ReaderAt, parser codexTranscriptParser, size, modTime, offset int64) {
	state := transcriptFileState{Size: size, ModTime: modTime, Offset: offset}
	if offset > 0 {
		head, tail, err := transcriptFingerprint(file, offset)
		if err != nil {
			cache.forgetCodex(path)
			return
		}
		state.Head, state.Tail = head, tail
	}
	entry, ok := newCodexCacheEntry(parser, state)
	if !ok {
		cache.forgetCodex(path)
		return
	}
	cache.storeCodex(path, entry)
}

func codexEventsFromSnapshots(session string, snapshots []codexUsageSnapshot, trackable bool) ([]tokenEvent, int) {
	var events []tokenEvent
	var previous codexTokenUsage
	hasPrevious := false
	incomplete := 0
	seen := make(map[codexSnapshotKey]struct{})
	for _, snapshot := range snapshots {
		key := codexSnapshotKey{
			Timestamp: snapshot.Timestamp.UnixNano(), Total: snapshot.Total,
			Last: snapshot.Last, HasLast: snapshot.HasLast, SourceID: snapshot.SourceID,
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		usage := codexTokenUsage{}
		emit := true
		switch {
		case !hasPrevious:
			if snapshot.HasLast {
				usage = snapshot.Last
			} else if snapshot.Total.hasTokens() {
				emit = false
				incomplete++
			}
		case snapshot.Total.equal(previous):
			emit = false
		case snapshot.Total.atLeast(previous):
			usage = snapshot.Total.subtract(previous)
		case snapshot.HasLast && snapshot.Last.hasTokens():
			usage = snapshot.Last
		default:
			emit = false
			incomplete++
		}
		previous = snapshot.Total
		hasPrevious = true
		if emit && usage.hasTokens() {
			trackKey := ""
			trackFingerprint := ""
			if trackable {
				trackFingerprint = tokenIdentityHash(
					"codex-snapshot", session, snapshot.SourceID, snapshot.Timestamp.UTC().Format(time.RFC3339Nano),
					strconv.FormatInt(snapshot.Total.Input, 10), strconv.FormatInt(snapshot.Total.Output, 10),
					strconv.FormatInt(snapshot.Total.Cached, 10), strconv.FormatInt(snapshot.Total.Reasoning, 10),
					strconv.FormatInt(snapshot.Last.Input, 10), strconv.FormatInt(snapshot.Last.Output, 10),
					strconv.FormatInt(snapshot.Last.Cached, 10), strconv.FormatInt(snapshot.Last.Reasoning, 10),
					strconv.FormatBool(snapshot.HasLast),
				)
				if snapshot.SourceID != "" {
					trackKey = tokenIdentityHash("codex-event", session, snapshot.SourceID)
				} else {
					// Current Codex token_count rows have no native event ID and can
					// legitimately share timestamps. The raw cumulative snapshot is
					// the stable source identity; the computed delta is deliberately
					// absent so transcript retention cannot change an existing key.
					trackKey = tokenIdentityHash("codex-event-snapshot", trackFingerprint)
				}
			}
			events = append(events, tokenEvent{
				Provider: "codex", Timestamp: snapshot.Timestamp, Session: session,
				Model: snapshot.Model, Input: usage.Input, Output: usage.Output,
				Cached: usage.Cached, Reasoning: usage.Reasoning,
				TrackKey: trackKey, TrackFingerprint: trackFingerprint,
			})
		}
	}
	return events, incomplete
}
