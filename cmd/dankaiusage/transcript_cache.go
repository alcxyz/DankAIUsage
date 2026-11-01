package main

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The transcript parse cache keeps what the token parsers extracted from each
// transcript file, so a summary reads only new bytes of growing transcripts
// and nothing of unchanged ones. It never holds message text or other
// transcript content, only the parsed token records and parser state.
const (
	transcriptParseCacheVersion     = 1
	transcriptParseCacheLockTimeout = 2 * time.Second
	transcriptFingerprintWindow     = 4096
)

// openTranscript is the only way the token parsers open transcript files;
// tests replace it to count reads.
var openTranscript = os.Open

func transcriptParseCachePath() string {
	return filepath.Join(pluginStateDir(), "transcript-parse-cache.gob")
}

// transcriptFileState describes the parsed prefix of a transcript: the file
// size and modification time when it was parsed, the end of the last complete
// line consumed, and hashes of the first bytes and of the bytes just before
// that offset, which detect a rewritten file.
type transcriptFileState struct {
	Size    int64
	ModTime int64
	Offset  int64
	Head    uint64
	Tail    uint64
}

type transcriptParseCacheData struct {
	Version int
	Claude  map[string]claudeTranscriptCacheEntry
	Codex   map[string]codexTranscriptCacheEntry
}

type claudeTranscriptCacheEntry struct {
	File   transcriptFileState
	Events []claudeCachedEvent
	// Err is a decode error on a complete line. Parsing stops there, as the
	// stream decoder used to, so appended lines are never parsed.
	Err string
}

type claudeCachedEvent struct {
	Timestamp        int64
	Session          string
	Model            string
	Input            int64
	Output           int64
	Cached           int64
	TrackKey         []byte
	TrackFingerprint []byte
}

type codexTranscriptCacheEntry struct {
	File            transcriptFileState
	Snapshots       []codexCachedSnapshot
	SessionID       string
	SessionStarted  int64
	HasSessionStart bool
	HasSessionMeta  bool
	Model           string
	UsageRecords    int
	Malformed       int
}

type codexCachedSnapshot struct {
	Timestamp int64
	Total     codexTokenUsage
	Last      codexTokenUsage
	HasLast   bool
	Model     string
	SourceID  string
}

// transcriptParseCache is loaded once per summary run. Entries not used by
// any scan in the run (removed files, or files older than every scan's
// cutoff) are dropped when it is saved. A nil cache parses every file fully.
type transcriptParseCache struct {
	path       string
	data       transcriptParseCacheData
	claudeUsed map[string]bool
	codexUsed  map[string]bool
	changed    bool
}

func newTranscriptParseCache(path string) *transcriptParseCache {
	return &transcriptParseCache{
		path: path,
		data: transcriptParseCacheData{
			Version: transcriptParseCacheVersion,
			Claude:  make(map[string]claudeTranscriptCacheEntry),
			Codex:   make(map[string]codexTranscriptCacheEntry),
		},
		claudeUsed: make(map[string]bool),
		codexUsed:  make(map[string]bool),
	}
}

// loadTranscriptParseCache never fails: a missing, unreadable, corrupt or
// other-version cache yields an empty one that is rebuilt by the scans.
func loadTranscriptParseCache(path string) *transcriptParseCache {
	cache := newTranscriptParseCache(path)
	var data transcriptParseCacheData
	err := withStateFileLock(path, transcriptParseCacheLockTimeout, "transcript parse cache", func() error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		return gob.NewDecoder(bufio.NewReaderSize(file, 1<<20)).Decode(&data)
	})
	if err != nil || data.Version != transcriptParseCacheVersion {
		cache.changed = true
		return cache
	}
	if data.Claude != nil {
		cache.data.Claude = data.Claude
	}
	if data.Codex != nil {
		cache.data.Codex = data.Codex
	}
	return cache
}

// save writes the entries used in this run atomically. A failure only means
// the next run parses more.
func (cache *transcriptParseCache) save() error {
	if cache == nil {
		return nil
	}
	for path := range cache.data.Claude {
		if !cache.claudeUsed[path] {
			delete(cache.data.Claude, path)
			cache.changed = true
		}
	}
	for path := range cache.data.Codex {
		if !cache.codexUsed[path] {
			delete(cache.data.Codex, path)
			cache.changed = true
		}
	}
	if !cache.changed {
		return nil
	}
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(cache.data); err != nil {
		return err
	}
	return withStateFileLock(cache.path, transcriptParseCacheLockTimeout, "transcript parse cache", func() error {
		return atomicWriteStateFile(cache.path, ".transcript-parse-cache-*.tmp", buffer.Bytes())
	})
}

func (cache *transcriptParseCache) claudeEntry(path string) (claudeTranscriptCacheEntry, bool) {
	cache.claudeUsed[path] = true
	entry, ok := cache.data.Claude[path]
	return entry, ok
}

func (cache *transcriptParseCache) storeClaude(path string, entry claudeTranscriptCacheEntry) {
	cache.claudeUsed[path] = true
	cache.data.Claude[path] = entry
	cache.changed = true
}

func (cache *transcriptParseCache) forgetClaude(path string) {
	if _, ok := cache.data.Claude[path]; ok {
		delete(cache.data.Claude, path)
		cache.changed = true
	}
}

func (cache *transcriptParseCache) codexEntry(path string) (codexTranscriptCacheEntry, bool) {
	cache.codexUsed[path] = true
	entry, ok := cache.data.Codex[path]
	return entry, ok
}

func (cache *transcriptParseCache) storeCodex(path string, entry codexTranscriptCacheEntry) {
	cache.codexUsed[path] = true
	cache.data.Codex[path] = entry
	cache.changed = true
}

func (cache *transcriptParseCache) forgetCodex(path string) {
	if _, ok := cache.data.Codex[path]; ok {
		delete(cache.data.Codex, path)
		cache.changed = true
	}
}

// statTranscript returns the size and modification time used to validate a
// cache entry, or false when the path is not a regular file.
func statTranscript(path string) (int64, int64, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0, 0, false
	}
	return info.Size(), info.ModTime().UnixNano(), true
}

// transcriptPrefixUnchanged reports whether the bytes a cache entry was
// parsed from are still at the start of the file. Only growth is an append:
// a file that shrank, kept its size under a new modification time (a
// rewrite in place), or whose fingerprints differ is parsed again from the
// start.
func transcriptPrefixUnchanged(file io.ReaderAt, cached transcriptFileState, size, modTime int64) bool {
	if cached.Offset <= 0 || cached.Offset > cached.Size || size < cached.Size {
		return false
	}
	if size == cached.Size && modTime != cached.ModTime {
		return false
	}
	head, tail, err := transcriptFingerprint(file, cached.Offset)
	return err == nil && head == cached.Head && tail == cached.Tail
}

func transcriptFingerprint(file io.ReaderAt, offset int64) (uint64, uint64, error) {
	window := int64(transcriptFingerprintWindow)
	buffer := make([]byte, window)
	headLength := offset
	if headLength > window {
		headLength = window
	}
	head, err := hashTranscriptRange(file, 0, headLength, buffer)
	if err != nil {
		return 0, 0, err
	}
	tailStart := offset - window
	if tailStart < 0 {
		tailStart = 0
	}
	tail, err := hashTranscriptRange(file, tailStart, offset-tailStart, buffer)
	if err != nil {
		return 0, 0, err
	}
	return head, tail, nil
}

func hashTranscriptRange(file io.ReaderAt, start, length int64, buffer []byte) (uint64, error) {
	read, err := file.ReadAt(buffer[:length], start)
	if int64(read) < length {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	hash := fnv.New64a()
	_, _ = hash.Write(buffer[:length])
	return hash.Sum64(), nil
}

// readTranscriptLines calls visit for each line, without its newline. A final
// line without a newline is visited with complete set to false: it may still
// be being written. Lines longer than maxLine (when positive) are skipped. It
// returns the number of bytes up to and including the last newline visited
// or skipped; visit returning false stops reading after that line.
func readTranscriptLines(reader io.Reader, maxLine int, visit func(line []byte, complete bool) bool) (int64, error) {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	line := make([]byte, 0, 64*1024)
	overflow := false
	var consumed, pending int64
	for {
		fragment, err := buffered.ReadSlice('\n')
		pending += int64(len(fragment))
		if !overflow {
			if maxLine <= 0 || len(line)+len(fragment) <= maxLine {
				line = append(line, fragment...)
			} else {
				overflow = true
				line = line[:0]
			}
		}
		if err == nil || errors.Is(err, io.EOF) {
			complete := err == nil
			keepReading := true
			if !overflow && len(line) > 0 {
				keepReading = visit(bytes.TrimSuffix(line, []byte{'\n'}), complete)
			}
			if complete {
				consumed += pending
				pending = 0
			}
			line = line[:0]
			overflow = false
			if !keepReading {
				return consumed, nil
			}
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return consumed, nil
		default:
			return consumed, err
		}
	}
}

// cacheableTime reports whether a timestamp survives a UnixNano round trip.
func cacheableTime(value time.Time) bool {
	year := value.Year()
	return year > 1678 && year < 2262
}

func cachedTime(nanos int64) time.Time {
	return time.Unix(0, nanos).Local()
}

func newClaudeCachedEvents(events []tokenEvent) ([]claudeCachedEvent, bool) {
	cached := make([]claudeCachedEvent, 0, len(events))
	for _, event := range events {
		if !cacheableTime(event.Timestamp) {
			return nil, false
		}
		var trackKey, trackFingerprint []byte
		if event.TrackKey != "" {
			key, keyErr := hex.DecodeString(event.TrackKey)
			fingerprint, fingerprintErr := hex.DecodeString(event.TrackFingerprint)
			if keyErr != nil || fingerprintErr != nil || len(key) == 0 || len(fingerprint) == 0 {
				return nil, false
			}
			trackKey, trackFingerprint = key, fingerprint
		} else if event.TrackFingerprint != "" {
			return nil, false
		}
		cached = append(cached, claudeCachedEvent{
			Timestamp:        event.Timestamp.UnixNano(),
			Session:          event.Session,
			Model:            event.Model,
			Input:            event.Input,
			Output:           event.Output,
			Cached:           event.Cached,
			TrackKey:         trackKey,
			TrackFingerprint: trackFingerprint,
		})
	}
	return cached, true
}

func (entry claudeTranscriptCacheEntry) tokenEvents(project string) []tokenEvent {
	events := make([]tokenEvent, 0, len(entry.Events))
	for _, cached := range entry.Events {
		event := tokenEvent{
			Provider:  "claude",
			Timestamp: cachedTime(cached.Timestamp),
			Session:   cached.Session,
			Project:   project,
			Model:     cached.Model,
			Input:     cached.Input,
			Output:    cached.Output,
			Cached:    cached.Cached,
		}
		if len(cached.TrackKey) > 0 {
			event.TrackKey = hex.EncodeToString(cached.TrackKey)
			event.TrackFingerprint = hex.EncodeToString(cached.TrackFingerprint)
		}
		events = append(events, event)
	}
	return events
}

func newCodexCacheEntry(parser codexTranscriptParser, file transcriptFileState) (codexTranscriptCacheEntry, bool) {
	entry := codexTranscriptCacheEntry{
		File:           file,
		Snapshots:      make([]codexCachedSnapshot, 0, len(parser.snapshots)),
		SessionID:      parser.session.ID,
		HasSessionMeta: parser.hasSessionMeta,
		Model:          parser.model,
		UsageRecords:   parser.usageRecords,
		Malformed:      parser.malformed,
	}
	if !parser.session.StartedAt.IsZero() {
		if !cacheableTime(parser.session.StartedAt) {
			return codexTranscriptCacheEntry{}, false
		}
		entry.SessionStarted = parser.session.StartedAt.UnixNano()
		entry.HasSessionStart = true
	}
	for _, snapshot := range parser.snapshots {
		if !cacheableTime(snapshot.Timestamp) {
			return codexTranscriptCacheEntry{}, false
		}
		entry.Snapshots = append(entry.Snapshots, codexCachedSnapshot{
			Timestamp: snapshot.Timestamp.UnixNano(),
			Total:     snapshot.Total,
			Last:      snapshot.Last,
			HasLast:   snapshot.HasLast,
			Model:     snapshot.Model,
			SourceID:  snapshot.SourceID,
		})
	}
	return entry, true
}

// parser restores the parser state after the cached prefix, including the
// session metadata that appears only near the start of a file.
func (entry codexTranscriptCacheEntry) parser() codexTranscriptParser {
	parser := codexTranscriptParser{
		snapshots:      make([]codexUsageSnapshot, 0, len(entry.Snapshots)),
		session:        codexSessionMeta{ID: entry.SessionID},
		hasSessionMeta: entry.HasSessionMeta,
		model:          entry.Model,
		usageRecords:   entry.UsageRecords,
		malformed:      entry.Malformed,
	}
	if entry.HasSessionStart {
		parser.session.StartedAt = cachedTime(entry.SessionStarted)
	}
	for _, cached := range entry.Snapshots {
		parser.snapshots = append(parser.snapshots, codexUsageSnapshot{
			Timestamp: cachedTime(cached.Timestamp),
			Total:     cached.Total,
			Last:      cached.Last,
			HasLast:   cached.HasLast,
			Model:     cached.Model,
			SourceID:  cached.SourceID,
		})
	}
	return parser
}
