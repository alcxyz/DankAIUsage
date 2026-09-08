package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
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
}

type codexSnapshotKey struct {
	Timestamp int64
	Total     codexTokenUsage
	Last      codexTokenUsage
	HasLast   bool
}

type codexSessionMeta struct {
	ID        string
	StartedAt time.Time
}

func collectCodexTranscriptEvents(root string, cutoff time.Time) ([]tokenEvent, codexTranscriptStats) {
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
	var events []tokenEvent
	for _, path := range files {
		snapshots, session, fileStats := readCodexTranscript(path)
		stats.FilesScanned++
		stats.UsageRecords += fileStats.UsageRecords
		stats.Malformed += fileStats.Malformed
		stats.ReadFailures += fileStats.ReadFailures
		sessionKey := session.ID
		if sessionKey == "" {
			sessionKey = path
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
		sessionEvents, incomplete := codexEventsFromSnapshots(session, snapshots)
		stats.Incomplete += incomplete
		events = append(events, sessionEvents...)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
	return events, stats
}

func readCodexTranscript(path string) ([]codexUsageSnapshot, codexSessionMeta, codexTranscriptStats) {
	stats := codexTranscriptStats{}
	file, err := os.Open(path)
	if err != nil {
		stats.ReadFailures++
		return nil, codexSessionMeta{}, stats
	}
	defer file.Close()

	var snapshots []codexUsageSnapshot
	var session codexSessionMeta
	var hasSessionMeta bool
	var model string
	err = readBoundedLines(file, codexTranscriptMaxLine, func(line []byte) {
		if !bytes.Contains(line, codexTokenRecordMarker) &&
			!bytes.Contains(line, codexSessionMetaMarker) &&
			!bytes.Contains(line, codexTurnContextMarker) {
			return
		}
		var row codexTranscriptRow
		if json.Unmarshal(line, &row) != nil {
			stats.Malformed++
			return
		}
		if row.Type == "session_meta" {
			// Fork rollouts begin with their own metadata, followed by copied
			// parent metadata and history. Only the first header owns this file.
			if hasSessionMeta {
				return
			}
			hasSessionMeta = true
			session = codexSessionMeta{ID: row.Payload.ID}
			if timestamp, ok := parseTime(firstNonEmpty(row.Payload.Timestamp, row.Timestamp)); ok {
				session.StartedAt = timestamp
			} else {
				stats.Malformed++
			}
			model = ""
			return
		}
		if row.Type == "turn_context" {
			model = row.Payload.Model
			return
		}
		if row.Type != "event_msg" || row.Payload.Type != "token_count" || row.Payload.Info == nil {
			return
		}
		timestamp, ok := parseTime(row.Timestamp)
		if !ok {
			stats.Malformed++
			return
		}
		total, ok := row.Payload.Info.Total.value()
		if !ok {
			if row.Payload.Info.Total != nil {
				stats.Malformed++
			}
			return
		}
		last, hasLast := row.Payload.Info.Last.value()
		if row.Payload.Info.Last != nil && !hasLast {
			stats.Malformed++
		}
		stats.UsageRecords++
		snapshots = append(snapshots, codexUsageSnapshot{
			Timestamp: timestamp, Total: total, Last: last, HasLast: hasLast, Model: model,
		})
	})
	if err != nil {
		stats.ReadFailures++
	}
	return snapshots, session, stats
}

func codexEventsFromSnapshots(session string, snapshots []codexUsageSnapshot) ([]tokenEvent, int) {
	var events []tokenEvent
	var previous codexTokenUsage
	hasPrevious := false
	incomplete := 0
	seen := make(map[codexSnapshotKey]struct{})
	for _, snapshot := range snapshots {
		key := codexSnapshotKey{
			Timestamp: snapshot.Timestamp.UnixNano(), Total: snapshot.Total,
			Last: snapshot.Last, HasLast: snapshot.HasLast,
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
			events = append(events, tokenEvent{
				Provider: "codex", Timestamp: snapshot.Timestamp, Session: session,
				Model: snapshot.Model, Input: usage.Input, Output: usage.Output,
				Cached: usage.Cached, Reasoning: usage.Reasoning,
			})
		}
	}
	return events, incomplete
}

// readBoundedLines keeps memory bounded even when transcript prompt records are
// very large. Oversized records are drained and ignored; token records are small.
func readBoundedLines(reader io.Reader, maxLine int, visit func([]byte)) error {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	line := make([]byte, 0, 64*1024)
	overflow := false
	for {
		fragment, err := buffered.ReadSlice('\n')
		if !overflow {
			if len(line)+len(fragment) <= maxLine {
				line = append(line, fragment...)
			} else {
				overflow = true
				line = line[:0]
			}
		}
		if err == nil || errors.Is(err, io.EOF) {
			if !overflow && len(line) > 0 {
				visit(bytes.TrimSuffix(line, []byte{'\n'}))
			}
			line = line[:0]
			overflow = false
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return nil
		default:
			return err
		}
	}
}
