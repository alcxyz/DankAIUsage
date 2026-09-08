package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	tokenTrackingStateVersion = 1
	tokenTrackingLockTimeout  = 2 * time.Second
)

var tokenTrackingRename = os.Rename

type TrackingSummary struct {
	Known     bool                    `json:"known"`
	Enabled   bool                    `json:"enabled"`
	StartedAt string                  `json:"startedAt,omitempty"`
	UpdatedAt string                  `json:"updatedAt,omitempty"`
	Providers map[string]PeriodTotals `json:"providers"`
	Errors    []string                `json:"errors,omitempty"`
}

type tokenTrackingRecord struct {
	Fingerprint string `json:"fingerprint"`
	Timestamp   string `json:"timestamp"`
	Session     string `json:"session,omitempty"`
	Input       int64  `json:"input"`
	Output      int64  `json:"output"`
	Cached      int64  `json:"cached"`
	Reasoning   int64  `json:"reasoning"`
	Tool        int64  `json:"tool"`
}

type tokenTrackingProviderState struct {
	Records map[string]tokenTrackingRecord `json:"records"`
}

type tokenTrackingState struct {
	Version          int                                   `json:"version"`
	Epoch            string                                `json:"epoch"`
	Enabled          bool                                  `json:"enabled"`
	Seeding          bool                                  `json:"seeding,omitempty"`
	StartedAt        string                                `json:"startedAt,omitempty"`
	UpdatedAt        string                                `json:"updatedAt,omitempty"`
	AcceptAfter      string                                `json:"acceptAfter,omitempty"`
	Providers        map[string]tokenTrackingProviderState `json:"providers"`
	CoverageWarnings []string                              `json:"coverageWarnings,omitempty"`
	LastErrors       []string                              `json:"lastErrors,omitempty"`
}

type tokenTrackingScan struct {
	Events   []tokenEvent
	Warnings []string
}

func tokenTrackingPath() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "dankaiusage", "token-tracking.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dankaiusage", "token-tracking.json")
}

func defaultTokenTrackingState() tokenTrackingState {
	return tokenTrackingState{
		Version: tokenTrackingStateVersion,
		Providers: map[string]tokenTrackingProviderState{
			"codex":  {Records: map[string]tokenTrackingRecord{}},
			"claude": {Records: map[string]tokenTrackingRecord{}},
		},
	}
}

func unknownTrackingSummary(err error) TrackingSummary {
	summary := TrackingSummary{Known: false, Providers: defaultTrackingProviderTotals()}
	if err != nil {
		summary.Errors = []string{formatTokenTrackingError(err)}
	}
	return summary
}

func defaultTrackingProviderTotals() map[string]PeriodTotals {
	return map[string]PeriodTotals{"codex": {}, "claude": {}}
}

func runTokenTrackingCommand(args []string) {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("tracking", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	failed := false
	var result TrackingSummary
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		result = unknownTrackingSummary(errors.New("invalid tracking arguments"))
		failed = true
	} else {
		now := time.Now()
		switch action {
		case "status":
			result = readTokenTrackingSummary(tokenTrackingPath())
		case "enable":
			result = enableTokenTracking(tokenTrackingPath(), now)
		case "pause":
			result = pauseTokenTracking(tokenTrackingPath(), now)
		case "clear":
			result = clearTokenTracking(tokenTrackingPath(), now)
		default:
			result = unknownTrackingSummary(fmt.Errorf("unknown tracking action %q", action))
			failed = true
		}
	}
	if !result.Known {
		failed = true
	}
	payload := struct {
		Tracking TrackingSummary `json:"tracking"`
	}{Tracking: result}
	var data []byte
	if *pretty {
		data, _ = json.MarshalIndent(payload, "", "  ")
	} else {
		data, _ = json.Marshal(payload)
	}
	fmt.Println(string(data))
	if failed {
		os.Exit(1)
	}
}

func readTokenTrackingSummary(path string) TrackingSummary {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return tokenTrackingSummary(defaultTokenTrackingState())
	}
	var state tokenTrackingState
	err := withTokenTrackingLock(path, func() error {
		loaded, err := loadTokenTrackingState(path)
		if err != nil {
			return err
		}
		state = loaded
		return nil
	})
	if err != nil {
		return unknownTrackingSummary(err)
	}
	return tokenTrackingSummary(state)
}

func enableTokenTracking(path string, now time.Time) TrackingSummary {
	boundary := now.UTC()
	var seedEpoch string
	var shouldSeed bool
	var immediate *TrackingSummary
	err := withTokenTrackingLock(path, func() error {
		state, err := loadTokenTrackingState(path)
		if err != nil {
			return err
		}
		if state.Enabled {
			summary := tokenTrackingSummary(state)
			immediate = &summary
			return nil
		}
		epoch, err := newTokenTrackingEpoch()
		if err != nil {
			return errors.New("could not create tracking generation")
		}
		state.Epoch = epoch
		state.LastErrors = nil
		if state.StartedAt == "" || state.Seeding {
			state = defaultTokenTrackingState()
			state.Epoch = epoch
			state.Seeding = true
			state.StartedAt = boundary.Format(time.RFC3339Nano)
			seedEpoch = epoch
			shouldSeed = true
		} else {
			state.Enabled = true
			state.Seeding = false
			state.AcceptAfter = boundary.Format(time.RFC3339Nano)
			state.UpdatedAt = boundary.Format(time.RFC3339Nano)
		}
		if err := saveTokenTrackingState(path, state); err != nil {
			return errors.New("could not save token tracking state")
		}
		if !shouldSeed {
			summary := tokenTrackingSummary(state)
			immediate = &summary
		}
		return nil
	})
	if err != nil {
		return unknownTrackingSummary(err)
	}
	if immediate != nil {
		return *immediate
	}
	if !shouldSeed {
		return readTokenTrackingSummary(path)
	}

	scan := scanAllTokenEvents(boundary)
	return commitInitialTokenTrackingSeed(path, seedEpoch, scan, boundary)
}

func commitInitialTokenTrackingSeed(path, seedEpoch string, scan tokenTrackingScan, boundary time.Time) TrackingSummary {
	var result TrackingSummary
	err := withTokenTrackingLock(path, func() error {
		state, err := loadTokenTrackingState(path)
		if err != nil {
			return err
		}
		if state.Epoch != seedEpoch || !state.Seeding || state.Enabled {
			result = tokenTrackingSummary(state)
			return nil
		}
		applyTokenTrackingScan(&state, scan, boundary)
		state.Enabled = true
		state.Seeding = false
		state.UpdatedAt = boundary.Format(time.RFC3339Nano)
		state.CoverageWarnings = appendUniqueStrings(state.CoverageWarnings, scan.Warnings...)
		state.LastErrors = append([]string(nil), scan.Warnings...)
		if err := saveTokenTrackingState(path, state); err != nil {
			return errors.New("could not save initial token tracking state")
		}
		result = tokenTrackingSummary(state)
		return nil
	})
	if err != nil {
		return unknownTrackingSummary(err)
	}
	return result
}

func pauseTokenTracking(path string, now time.Time) TrackingSummary {
	var result TrackingSummary
	err := withTokenTrackingLock(path, func() error {
		state, err := loadTokenTrackingState(path)
		if err != nil {
			return err
		}
		epoch, err := newTokenTrackingEpoch()
		if err != nil {
			return errors.New("could not create tracking generation")
		}
		state.Epoch = epoch
		state.Enabled = false
		if state.Seeding {
			state.CoverageWarnings = appendUniqueStrings(state.CoverageWarnings,
				"Initial retained-history seed was cancelled before completion")
		}
		state.Seeding = false
		state.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		state.LastErrors = nil
		if err := saveTokenTrackingState(path, state); err != nil {
			return errors.New("could not save paused token tracking state")
		}
		result = tokenTrackingSummary(state)
		return nil
	})
	if err != nil {
		return unknownTrackingSummary(err)
	}
	return result
}

func clearTokenTracking(path string, now time.Time) TrackingSummary {
	var result TrackingSummary
	err := withTokenTrackingLock(path, func() error {
		epoch, err := newTokenTrackingEpoch()
		if err != nil {
			return errors.New("could not create tracking generation")
		}
		state := defaultTokenTrackingState()
		state.Epoch = epoch
		state.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		if err := saveTokenTrackingState(path, state); err != nil {
			return errors.New("could not clear token tracking state")
		}
		result = tokenTrackingSummary(state)
		return nil
	})
	if err != nil {
		return unknownTrackingSummary(err)
	}
	return result
}

func refreshTokenTracking(path string, now time.Time) TrackingSummary {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return tokenTrackingSummary(defaultTokenTrackingState())
	}
	var snapshot tokenTrackingState
	err := withTokenTrackingLock(path, func() error {
		state, err := loadTokenTrackingState(path)
		if err != nil {
			return err
		}
		snapshot = state
		return nil
	})
	if err != nil {
		return unknownTrackingSummary(err)
	}
	if !snapshot.Enabled {
		return tokenTrackingSummary(snapshot)
	}

	boundary := now.UTC()
	scan := scanAllTokenEvents(boundary)
	return commitTokenTrackingRefresh(path, snapshot.Epoch, scan, boundary)
}

func commitTokenTrackingRefresh(path, expectedEpoch string, scan tokenTrackingScan, boundary time.Time) TrackingSummary {
	var result TrackingSummary
	err := withTokenTrackingLock(path, func() error {
		state, err := loadTokenTrackingState(path)
		if err != nil {
			return err
		}
		if !state.Enabled || state.Epoch != expectedEpoch {
			result = tokenTrackingSummary(state)
			return nil
		}
		if updatedAt, err := time.Parse(time.RFC3339Nano, state.UpdatedAt); err == nil && updatedAt.After(boundary) {
			result = tokenTrackingSummary(state)
			return nil
		}
		changed := applyTokenTrackingScan(&state, scan, boundary)
		if !equalStrings(state.LastErrors, scan.Warnings) {
			state.LastErrors = append([]string(nil), scan.Warnings...)
			changed = true
		}
		if !changed {
			result = tokenTrackingSummary(state)
			return nil
		}
		state.UpdatedAt = boundary.Format(time.RFC3339Nano)
		if err := saveTokenTrackingState(path, state); err != nil {
			return errors.New("could not save refreshed token tracking state")
		}
		result = tokenTrackingSummary(state)
		return nil
	})
	if err != nil {
		return unknownTrackingSummary(err)
	}
	return result
}

func scanAllTokenEvents(_ time.Time) tokenTrackingScan {
	scan := tokenTrackingScan{}
	codexEvents, codexStats := collectCodexTranscriptEvents(codexHome(), time.Time{})
	scan.Events = append(scan.Events, codexEvents...)
	if codexStats.SourcesFound == 0 {
		scan.Warnings = append(scan.Warnings, "Codex: no retained transcript directory was found")
	} else if codexStats.partial() {
		scan.Warnings = append(scan.Warnings, "Codex: retained transcript scan was partial")
	}

	claudeEvents, claudeFiles, claudeErrors := scanClaudeTokenEvents(claudeHome())
	claudeEvents, ambiguousClaude := normalizeClaudeTokenEvents(claudeEvents)
	scan.Events = append(scan.Events, claudeEvents...)
	if claudeFiles == 0 {
		scan.Warnings = append(scan.Warnings, "Claude: no retained project transcripts were found")
	} else if claudeErrors > 0 {
		scan.Warnings = append(scan.Warnings, "Claude: retained transcript scan was partial")
	}
	if ambiguousClaude > 0 {
		scan.Warnings = append(scan.Warnings, fmt.Sprintf("Claude: %d ambiguous message revisions were ignored to avoid inflating totals", ambiguousClaude))
	}

	untrackable := map[string]int{}
	for _, event := range scan.Events {
		if event.TrackKey == "" {
			untrackable[event.Provider]++
		}
	}
	for _, provider := range []string{"codex", "claude"} {
		if untrackable[provider] > 0 {
			scan.Warnings = append(scan.Warnings, fmt.Sprintf("%s: %d parsed token events lacked a stable identity and were not tracked", providerDisplayName(provider), untrackable[provider]))
		}
	}
	sort.Strings(scan.Warnings)
	return scan
}

func scanClaudeTokenEvents(root string) ([]tokenEvent, int, int) {
	projects := filepath.Join(root, "projects")
	var events []tokenEvent
	files := 0
	failures := 0
	err := filepath.WalkDir(projects, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			failures++
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		files++
		fileEvents, err := readClaudeJSONL(path)
		if err != nil {
			failures++
		}
		events = append(events, fileEvents...)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		failures++
	}
	return events, files, failures
}

func applyTokenTrackingScan(state *tokenTrackingState, scan tokenTrackingScan, boundary time.Time) bool {
	acceptAfter, _ := time.Parse(time.RFC3339Nano, state.AcceptAfter)
	conflicts := map[string]int{}
	lateOrChanged := map[string]int{}
	invalid := map[string]int{}
	changed := false
	// Snapshot this watermark before applying the scan. That lets a first seed
	// accept multiple legitimate snapshots sharing one timestamp, while later
	// scans reject unseen old fingerprints that may be transcript corrections.
	codexWatermarks := map[string]time.Time{}
	for _, record := range state.Providers["codex"].Records {
		if record.Session == "" {
			continue
		}
		timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err == nil && timestamp.After(codexWatermarks[record.Session]) {
			codexWatermarks[record.Session] = timestamp
		}
	}
	for _, event := range scan.Events {
		if event.TrackKey == "" || event.Timestamp.After(boundary) || (!acceptAfter.IsZero() && event.Timestamp.Before(acceptAfter)) {
			continue
		}
		provider, ok := state.Providers[event.Provider]
		if !ok {
			continue
		}
		if event.Input < 0 || event.Output < 0 || event.Cached < 0 || event.Reasoning < 0 || event.Tool < 0 {
			invalid[event.Provider]++
			continue
		}
		record := tokenTrackingRecord{
			Fingerprint: event.TrackFingerprint,
			Timestamp:   event.Timestamp.UTC().Format(time.RFC3339Nano),
			Input:       event.Input,
			Output:      event.Output,
			Cached:      event.Cached,
			Reasoning:   event.Reasoning,
			Tool:        event.Tool,
		}
		if event.Session != "" {
			record.Session = tokenIdentityHash("tracked-session", event.Provider, event.Session)
		}
		existing, exists := provider.Records[event.TrackKey]
		if !exists {
			if event.Provider == "codex" {
				if watermark, ok := codexWatermarks[record.Session]; ok && !event.Timestamp.After(watermark) {
					lateOrChanged[event.Provider]++
					continue
				}
			}
			provider.Records[event.TrackKey] = record
			state.Providers[event.Provider] = provider
			changed = true
			continue
		}
		if event.Provider == "claude" {
			switch {
			case trackingRecordAtLeast(record, existing):
				existingAt, _ := time.Parse(time.RFC3339Nano, existing.Timestamp)
				if existingAt.After(event.Timestamp) {
					record.Timestamp = existing.Timestamp
				}
				if record.Session == "" {
					record.Session = existing.Session
				}
				provider.Records[event.TrackKey] = record
				state.Providers[event.Provider] = provider
				if record != existing {
					changed = true
				}
			case trackingRecordAtLeast(existing, record):
				// An older streamed revision or replay contributes nothing new.
			default:
				// Mixed increases and decreases are corrections, not a safe delta.
				conflicts[event.Provider]++
			}
		} else if existing.Fingerprint != record.Fingerprint {
			conflicts[event.Provider]++
		}
	}
	for _, provider := range []string{"codex", "claude"} {
		if conflicts[provider] > 0 {
			before := len(state.CoverageWarnings)
			state.CoverageWarnings = appendUniqueStrings(state.CoverageWarnings, fmt.Sprintf(
				"%s: %d rewritten token checkpoints were ignored to avoid inflating the tracked total", providerDisplayName(provider), conflicts[provider]))
			changed = changed || len(state.CoverageWarnings) != before
		}
		if invalid[provider] > 0 {
			before := len(state.CoverageWarnings)
			state.CoverageWarnings = appendUniqueStrings(state.CoverageWarnings, fmt.Sprintf(
				"%s: %d token events with invalid counts were not tracked", providerDisplayName(provider), invalid[provider]))
			changed = changed || len(state.CoverageWarnings) != before
		}
		if lateOrChanged[provider] > 0 {
			before := len(state.CoverageWarnings)
			state.CoverageWarnings = appendUniqueStrings(state.CoverageWarnings, fmt.Sprintf(
				"%s: %d late or changed token checkpoints were ignored to avoid inflating the tracked total", providerDisplayName(provider), lateOrChanged[provider]))
			changed = changed || len(state.CoverageWarnings) != before
		}
	}
	return changed
}

func tokenTrackingSummary(state tokenTrackingState) TrackingSummary {
	errors := appendUniqueStrings(append([]string(nil), state.CoverageWarnings...), state.LastErrors...)
	if state.Seeding {
		errors = appendUniqueStrings(errors, "Initial retained-history seed is incomplete; enable tracking again to retry")
	}
	summary := TrackingSummary{
		Known:     true,
		Enabled:   state.Enabled,
		StartedAt: state.StartedAt,
		UpdatedAt: state.UpdatedAt,
		Providers: defaultTrackingProviderTotals(),
		Errors:    errors,
	}
	for _, providerID := range []string{"codex", "claude"} {
		provider := state.Providers[providerID]
		totals := PeriodTotals{}
		sessions := map[string]struct{}{}
		for _, record := range provider.Records {
			event := tokenEvent{
				Provider: providerID,
				Input:    record.Input, Output: record.Output, Cached: record.Cached,
				Reasoning: record.Reasoning, Tool: record.Tool,
			}
			if timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp); err == nil {
				event.Timestamp = timestamp
			}
			addEvent(&totals, event)
			if record.Session != "" {
				sessions[record.Session] = struct{}{}
			}
		}
		totals.Sessions = int64(len(sessions))
		summary.Providers[providerID] = totals
	}
	return summary
}

func loadTokenTrackingState(path string) (tokenTrackingState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultTokenTrackingState(), nil
	}
	if err != nil {
		return tokenTrackingState{}, errors.New("could not read token tracking state")
	}
	var state tokenTrackingState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return tokenTrackingState{}, errors.New("saved token tracking state is unreadable")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return tokenTrackingState{}, errors.New("saved token tracking state has trailing data")
	}
	if err := validateTokenTrackingState(state); err != nil {
		return tokenTrackingState{}, err
	}
	return state, nil
}

func validateTokenTrackingState(state tokenTrackingState) error {
	if state.Version != tokenTrackingStateVersion {
		return errors.New("saved token tracking state version is unsupported")
	}
	if (state.Enabled || state.Seeding || state.StartedAt != "") && !validTokenHash(state.Epoch) {
		return errors.New("saved token tracking state has an invalid generation")
	}
	if state.Enabled && state.Seeding {
		return errors.New("saved token tracking state has conflicting modes")
	}
	if (state.Enabled || state.Seeding) && state.StartedAt == "" {
		return errors.New("saved token tracking state has no start timestamp")
	}
	if state.AcceptAfter != "" && state.StartedAt == "" {
		return errors.New("saved token tracking state has a resume timestamp but no start timestamp")
	}
	if len(state.Providers) != 2 {
		return errors.New("saved token tracking state has unexpected provider checkpoints")
	}
	hasRecords := false
	for _, value := range []string{state.StartedAt, state.UpdatedAt, state.AcceptAfter} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return errors.New("saved token tracking state has an invalid timestamp")
			}
		}
	}
	for _, providerID := range []string{"codex", "claude"} {
		provider, ok := state.Providers[providerID]
		if !ok || provider.Records == nil {
			return errors.New("saved token tracking state is missing provider checkpoints")
		}
		var input, output, cached, reasoning, tool int64
		var total int64
		for key, record := range provider.Records {
			hasRecords = true
			if !validTokenHash(key) || !validTokenHash(record.Fingerprint) || (record.Session != "" && !validTokenHash(record.Session)) {
				return errors.New("saved token tracking state has an invalid checkpoint")
			}
			if _, err := time.Parse(time.RFC3339Nano, record.Timestamp); err != nil {
				return errors.New("saved token tracking state has an invalid checkpoint timestamp")
			}
			values := []int64{record.Input, record.Output, record.Cached, record.Reasoning, record.Tool}
			for _, value := range values {
				if value < 0 {
					return errors.New("saved token tracking state has a negative token count")
				}
			}
			if !safeTrackingAdd(&input, record.Input) || !safeTrackingAdd(&output, record.Output) ||
				!safeTrackingAdd(&cached, record.Cached) || !safeTrackingAdd(&reasoning, record.Reasoning) ||
				!safeTrackingAdd(&tool, record.Tool) {
				return errors.New("saved token tracking state token totals overflow")
			}
			if !safeTrackingAdd(&total, record.Input) || !safeTrackingAdd(&total, record.Output) ||
				(providerID == "claude" && !safeTrackingAdd(&total, record.Cached)) {
				return errors.New("saved token tracking state provider total overflows")
			}
		}
	}
	if hasRecords && state.StartedAt == "" {
		return errors.New("saved token tracking state has checkpoints but no start timestamp")
	}
	return nil
}

func saveTokenTrackingState(path string, state tokenTrackingState) error {
	if err := validateTokenTrackingState(state); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".token-tracking-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := tokenTrackingRename(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func withTokenTrackingLock(path string, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("could not create token tracking directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return errors.New("could not protect token tracking directory")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.New("could not open token tracking lock")
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return errors.New("could not protect token tracking lock")
	}
	deadline := time.Now().Add(tokenTrackingLockTimeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return errors.New("could not lock token tracking state")
		}
		if !time.Now().Before(deadline) {
			return errors.New("timed out waiting for token tracking lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func tokenIdentityHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func newTokenTrackingEpoch() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func validTokenHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeTrackingAdd(total *int64, value int64) bool {
	if value > 0 && *total > math.MaxInt64-value {
		return false
	}
	*total += value
	return true
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	out := make([]string, 0, len(values)+len(additions))
	for _, value := range append(values, additions...) {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func providerDisplayName(provider string) string {
	if provider == "codex" {
		return "Codex"
	}
	if provider == "claude" {
		return "Claude"
	}
	return provider
}

func trackingRecordAtLeast(left, right tokenTrackingRecord) bool {
	return left.Input >= right.Input && left.Output >= right.Output && left.Cached >= right.Cached &&
		left.Reasoning >= right.Reasoning && left.Tool >= right.Tool
}

func formatTokenTrackingError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if strings.Contains(message, "token tracking") {
		return message
	}
	return "token tracking unavailable: " + message
}
