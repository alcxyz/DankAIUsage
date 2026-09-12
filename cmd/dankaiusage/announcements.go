package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	announcementEndpoint       = "https://tokenresets.com/api/v1/events?limit=50"
	announcementCacheVersion   = 1
	announcementStateVersion   = 1
	announcementMaxEvents      = 50
	announcementMaxBytes       = 512 << 10
	announcementCooldown       = 15 * time.Minute
	announcementDatasetMaxAge  = 24 * time.Hour
	announcementMaxRetryAfter  = 24 * time.Hour
	announcementRequestTimeout = 8 * time.Second
)

var announcementEventPath = regexp.MustCompile(`^/events/[a-z0-9][a-z0-9-]{0,159}$`)
var announcementKind = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type AnnouncementEvent struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	AnnouncedAt string `json:"announcedAt"`
	EffectiveAt string `json:"effectiveAt,omitempty"`
	ExpectedBy  string `json:"expectedBy,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	Confidence  string `json:"confidence"`
	ScopeLabel  string `json:"scopeLabel"`
	URL         string `json:"url"`
	Revision    int    `json:"revision"`
}

type AnnouncementResult struct {
	Available bool                `json:"available"`
	Stale     bool                `json:"stale"`
	Message   string              `json:"message"`
	Events    []AnnouncementEvent `json:"events"`
	FetchedAt string              `json:"fetchedAt"`
}

type announcementCache struct {
	Version     int                 `json:"version"`
	FetchedAt   string              `json:"fetchedAt"`
	GeneratedAt string              `json:"generatedAt"`
	ETag        string              `json:"etag,omitempty"`
	Events      []AnnouncementEvent `json:"events"`
}

type announcementRefreshState struct {
	Version       int    `json:"version"`
	NextAttemptAt string `json:"nextAttemptAt,omitempty"`
	Failed        bool   `json:"failed,omitempty"`
}

type announcementFeed struct {
	Data []json.RawMessage `json:"data"`
	Meta struct {
		GeneratedAt   string `json:"generated_at"`
		SchemaVersion string `json:"schema_version"`
	} `json:"meta"`
}

type announcementWireEvent struct {
	ID       string `json:"id"`
	Provider struct {
		Slug string `json:"slug"`
	} `json:"provider"`
	EventType           string  `json:"event_type"`
	Status              string  `json:"status"`
	Title               string  `json:"title"`
	Summary             string  `json:"summary"`
	AnnouncedAt         string  `json:"announced_at"`
	EffectiveAt         *string `json:"effective_at"`
	ObservedEffectiveAt *string `json:"observed_effective_at"`
	ExpectedBy          *string `json:"expected_by"`
	ExpiresAt           *string `json:"expires_at"`
	Scope               struct {
		Products []string `json:"products"`
		Plans    []string `json:"plans"`
		Windows  []string `json:"windows"`
	} `json:"scope"`
	Confidence struct {
		Label string `json:"label"`
	} `json:"confidence"`
	Revision int `json:"revision"`
	Links    struct {
		HTML string `json:"html"`
	} `json:"links"`
}

type announcementHTTPError struct {
	retryAfter time.Duration
}

func (e announcementHTTPError) Error() string { return "announcement request failed" }

func announcementCacheDir() string {
	if value := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(value) {
		return filepath.Join(value, "dankaiusage")
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, ".cache", "dankaiusage")
}

func announcementsCachePath() string {
	dir := announcementCacheDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "announcements.json")
}

func announcementsStatePath() string {
	dir := pluginStateDir()
	if !filepath.IsAbs(dir) {
		return ""
	}
	return filepath.Join(dir, "announcements-refresh.json")
}

func runAnnouncementsCommand(args []string) {
	fs := flag.NewFlagSet("announcements", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	enabled := fs.Bool("enabled", false, "fetch public provider announcements")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		writeAnnouncementJSON(unavailableAnnouncements("Announcements are unavailable."))
		return
	}
	result := collectAnnouncements(*enabled, time.Now(), announcementsCachePath(), announcementsStatePath(), http.DefaultTransport)
	writeAnnouncementJSON(result)
}

func writeAnnouncementJSON(value AnnouncementResult) {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Println(`{"available":false,"stale":false,"message":"Announcements are unavailable.","events":[],"fetchedAt":""}`)
		return
	}
	fmt.Println(string(data))
}

func unavailableAnnouncements(message string) AnnouncementResult {
	return AnnouncementResult{Message: message, Events: []AnnouncementEvent{}}
}

func collectAnnouncements(enabled bool, now time.Time, cachePath, statePath string, transport http.RoundTripper) AnnouncementResult {
	if !enabled {
		return unavailableAnnouncements("Announcements are disabled.")
	}
	if !filepath.IsAbs(cachePath) || !filepath.IsAbs(statePath) {
		return unavailableAnnouncements("Announcements are temporarily unavailable.")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}

	var result AnnouncementResult
	operationErr := withUsageRefreshLock(statePath, func() error {
		cached, cacheOK := loadAnnouncementCache(cachePath, now)
		state, err := loadAnnouncementState(statePath, now)
		if err != nil {
			return err
		}
		nextAttempt, _ := parseAnnouncementTime(state.NextAttemptAt)
		if now.Before(nextAttempt) {
			result = announcementResultFromCache(cached, cacheOK, now, state.Failed)
			return nil
		}

		state.Version = announcementStateVersion
		state.NextAttemptAt = now.Add(announcementCooldown).UTC().Format(time.RFC3339Nano)
		state.Failed = true
		if err := saveAnnouncementState(statePath, state); err != nil {
			return errors.New("could not reserve announcement refresh")
		}

		fresh, notModified, etag, fetchErr := fetchAnnouncementFeed(now, cached, cacheOK, transport)
		if fetchErr != nil {
			var httpErr announcementHTTPError
			if errors.As(fetchErr, &httpErr) && httpErr.retryAfter > announcementCooldown {
				state.NextAttemptAt = now.Add(httpErr.retryAfter).UTC().Format(time.RFC3339Nano)
				if err := saveAnnouncementState(statePath, state); err != nil {
					return errors.New("could not save announcement backoff")
				}
			}
			result = announcementResultFromCache(cached, cacheOK, now, true)
			return nil
		}

		if notModified {
			cached.FetchedAt = now.UTC().Format(time.RFC3339)
			if etag != "" {
				cached.ETag = etag
			}
			if err := saveAnnouncementCache(cachePath, cached); err != nil {
				return errors.New("could not save announcement cache")
			}
			state.Failed = false
			if err := saveAnnouncementState(statePath, state); err != nil {
				return errors.New("could not complete announcement refresh")
			}
			result = announcementResultFromCache(cached, true, now, false)
			return nil
		}

		if err := saveAnnouncementCache(cachePath, fresh); err != nil {
			return errors.New("could not save announcement cache")
		}
		state.Failed = false
		if err := saveAnnouncementState(statePath, state); err != nil {
			return errors.New("could not complete announcement refresh")
		}
		result = announcementResultFromCache(fresh, true, now, false)
		return nil
	})
	if operationErr != nil {
		cached, ok := loadAnnouncementCache(cachePath, now)
		return announcementResultFromCache(cached, ok, now, true)
	}
	return result
}

func announcementResultFromCache(cache announcementCache, ok bool, now time.Time, refreshFailed bool) AnnouncementResult {
	if !ok {
		return unavailableAnnouncements("Announcements are temporarily unavailable.")
	}
	generatedAt, _ := parseAnnouncementTime(cache.GeneratedAt)
	fetchedAt, _ := parseAnnouncementTime(cache.FetchedAt)
	stale := refreshFailed || now.Sub(generatedAt) > announcementDatasetMaxAge || now.Sub(fetchedAt) > announcementDatasetMaxAge
	message := ""
	if refreshFailed {
		message = "Showing cached announcements; updates are temporarily unavailable."
	} else if stale {
		message = "Announcements may be out of date."
	}
	return AnnouncementResult{
		Available: true,
		Stale:     stale,
		Message:   message,
		Events:    cache.Events,
		FetchedAt: cache.FetchedAt,
	}
}

func fetchAnnouncementFeed(now time.Time, cached announcementCache, cacheOK bool, transport http.RoundTripper) (announcementCache, bool, string, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, announcementEndpoint, nil)
	if err != nil {
		return announcementCache{}, false, "", err
	}
	request.Header.Set("Accept", "application/json")
	if cacheOK && validAnnouncementETag(cached.ETag) {
		request.Header.Set("If-None-Match", cached.ETag)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   announcementRequestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return announcementCache{}, false, "", err
	}
	defer response.Body.Close()
	etag := response.Header.Get("ETag")
	if !validAnnouncementETag(etag) {
		etag = ""
	}
	if response.StatusCode == http.StatusNotModified {
		if !cacheOK {
			return announcementCache{}, false, "", announcementHTTPError{}
		}
		return announcementCache{}, true, etag, nil
	}
	if response.StatusCode != http.StatusOK {
		retry := time.Duration(0)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable {
			retry = parseAnnouncementRetryAfter(response.Header.Get("Retry-After"), now)
		}
		return announcementCache{}, false, "", announcementHTTPError{retryAfter: retry}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, announcementMaxBytes+1))
	if err != nil || len(body) > announcementMaxBytes {
		return announcementCache{}, false, "", errors.New("announcement response is invalid")
	}
	generatedAt, events, err := parseAnnouncementFeed(body, now)
	if err != nil {
		return announcementCache{}, false, "", err
	}
	return announcementCache{
		Version:     announcementCacheVersion,
		FetchedAt:   now.UTC().Format(time.RFC3339),
		GeneratedAt: generatedAt,
		ETag:        etag,
		Events:      events,
	}, false, etag, nil
}

func parseAnnouncementFeed(data []byte, now time.Time) (string, []AnnouncementEvent, error) {
	var feed announcementFeed
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&feed); err != nil {
		return "", nil, errors.New("announcement response is invalid")
	}
	if err := ensureAnnouncementJSONEOF(decoder); err != nil || feed.Data == nil || feed.Meta.SchemaVersion != "1.0" {
		return "", nil, errors.New("announcement response is invalid")
	}
	generatedAt, ok := validAnnouncementTimestamp(feed.Meta.GeneratedAt, false, now)
	if !ok || generatedAt.After(now.Add(5*time.Minute)) {
		return "", nil, errors.New("announcement response is invalid")
	}
	type candidate struct {
		index int
		wire  announcementWireEvent
	}
	byID := make(map[string]candidate)
	for index, raw := range feed.Data {
		var wire announcementWireEvent
		if json.Unmarshal(raw, &wire) != nil {
			continue
		}
		id, idOK := boundedPlainText(wire.ID, 128, false)
		if !idOK || wire.Revision < 1 || wire.Revision > 1_000_000 {
			continue
		}
		wire.ID = id
		previous, exists := byID[id]
		if !exists || wire.Revision > previous.wire.Revision || (wire.Revision == previous.wire.Revision && wire.Status != "published") {
			byID[id] = candidate{index: index, wire: wire}
		}
	}
	candidates := make([]candidate, 0, len(byID))
	for _, item := range byID {
		candidates = append(candidates, item)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].index < candidates[j].index })
	events := make([]AnnouncementEvent, 0, min(len(candidates), announcementMaxEvents))
	for _, item := range candidates {
		if len(events) == announcementMaxEvents {
			break
		}
		if item.wire.Status != "published" {
			continue
		}
		if event, ok := validateWireAnnouncement(item.wire, now); ok {
			events = append(events, event)
		}
	}
	return generatedAt.UTC().Format(time.RFC3339), events, nil
}

func validateWireAnnouncement(wire announcementWireEvent, now time.Time) (AnnouncementEvent, bool) {
	provider := map[string]string{"openai-codex": "codex", "anthropic-claude": "claude"}[wire.Provider.Slug]
	if provider == "" || !announcementKind.MatchString(wire.EventType) || wire.Revision < 1 || wire.Revision > 1_000_000 {
		return AnnouncementEvent{}, false
	}
	id, ok := boundedPlainText(wire.ID, 128, false)
	if !ok {
		return AnnouncementEvent{}, false
	}
	title, ok := boundedPlainText(wire.Title, 200, false)
	if !ok {
		return AnnouncementEvent{}, false
	}
	summary, ok := boundedPlainText(wire.Summary, 1200, true)
	if !ok {
		return AnnouncementEvent{}, false
	}
	announcedAt, ok := validAnnouncementTimestamp(wire.AnnouncedAt, false, now)
	if !ok {
		return AnnouncementEvent{}, false
	}
	effectiveAt, ok := optionalAnnouncementTimestamp(wire.EffectiveAt, now)
	if !ok {
		return AnnouncementEvent{}, false
	}
	if effectiveAt == "" {
		effectiveAt, ok = optionalAnnouncementTimestamp(wire.ObservedEffectiveAt, now)
		if !ok {
			return AnnouncementEvent{}, false
		}
	}
	expectedBy, ok := optionalAnnouncementTimestamp(wire.ExpectedBy, now)
	if !ok {
		return AnnouncementEvent{}, false
	}
	expiresAt, ok := optionalAnnouncementTimestamp(wire.ExpiresAt, now)
	if !ok {
		return AnnouncementEvent{}, false
	}
	if wire.Confidence.Label != "reported" && wire.Confidence.Label != "verified" {
		return AnnouncementEvent{}, false
	}
	scopeLabel, ok := makeAnnouncementScopeLabel(wire.Scope.Products, wire.Scope.Plans, wire.Scope.Windows)
	if !ok {
		return AnnouncementEvent{}, false
	}
	publicURL, ok := validateAnnouncementURL(wire.Links.HTML)
	if !ok {
		return AnnouncementEvent{}, false
	}
	return AnnouncementEvent{
		ID:          id,
		Provider:    provider,
		Kind:        wire.EventType,
		Title:       title,
		Summary:     summary,
		AnnouncedAt: announcedAt.UTC().Format(time.RFC3339),
		EffectiveAt: effectiveAt,
		ExpectedBy:  expectedBy,
		ExpiresAt:   expiresAt,
		Confidence:  wire.Confidence.Label,
		ScopeLabel:  scopeLabel,
		URL:         publicURL,
		Revision:    wire.Revision,
	}, true
}

func validateCachedAnnouncement(event AnnouncementEvent, now time.Time) (AnnouncementEvent, bool) {
	providerSlug := map[string]string{"codex": "openai-codex", "claude": "anthropic-claude"}[event.Provider]
	if providerSlug == "" {
		return AnnouncementEvent{}, false
	}
	wire := announcementWireEvent{
		ID:          event.ID,
		EventType:   event.Kind,
		Status:      "published",
		Title:       event.Title,
		Summary:     event.Summary,
		AnnouncedAt: event.AnnouncedAt,
		Revision:    event.Revision,
	}
	wire.Provider.Slug = providerSlug
	wire.Confidence.Label = event.Confidence
	wire.Links.HTML = event.URL
	if event.EffectiveAt != "" {
		wire.EffectiveAt = &event.EffectiveAt
	}
	if event.ExpectedBy != "" {
		wire.ExpectedBy = &event.ExpectedBy
	}
	if event.ExpiresAt != "" {
		wire.ExpiresAt = &event.ExpiresAt
	}
	validated, ok := validateWireAnnouncement(wire, now)
	if !ok {
		return AnnouncementEvent{}, false
	}
	scope, ok := boundedPlainText(event.ScopeLabel, 500, false)
	if !ok {
		return AnnouncementEvent{}, false
	}
	validated.ScopeLabel = scope
	return validated, true
}

func makeAnnouncementScopeLabel(products, plans, windows []string) (string, bool) {
	type scopePart struct {
		label  string
		values []string
	}
	parts := []scopePart{{"Products", products}, {"Plans", plans}, {"Windows", windows}}
	labels := make([]string, 0, 3)
	for _, part := range parts {
		if len(part.values) > 10 {
			return "", false
		}
		clean := make([]string, 0, len(part.values))
		for _, value := range part.values {
			value, ok := boundedPlainText(value, 64, false)
			if !ok {
				return "", false
			}
			clean = append(clean, value)
		}
		if len(clean) > 0 {
			labels = append(labels, part.label+": "+strings.Join(clean, ", "))
		}
	}
	if len(labels) == 0 {
		return "Scope not specified", true
	}
	label := strings.Join(labels, " · ")
	if len([]rune(label)) > 500 {
		return "", false
	}
	return label, true
}

func boundedPlainText(value string, maxRunes int, allowEmpty bool) (string, bool) {
	if value != strings.TrimSpace(value) || (!allowEmpty && value == "") || len([]rune(value)) > maxRunes {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, true
}

func validAnnouncementTimestamp(value string, allowEmpty bool, _ time.Time) (time.Time, bool) {
	if value == "" {
		return time.Time{}, allowEmpty
	}
	if len(value) > 64 {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}

func optionalAnnouncementTimestamp(value *string, now time.Time) (string, bool) {
	if value == nil || *value == "" {
		return "", true
	}
	parsed, ok := validAnnouncementTimestamp(*value, false, now)
	if !ok {
		return "", false
	}
	return parsed.UTC().Format(time.RFC3339), true
}

func validateAnnouncementURL(value string) (string, bool) {
	if len(value) == 0 || len(value) > 256 {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return "", false
	}
	if parsed.IsAbs() {
		if parsed.Scheme != "https" || parsed.Host != "tokenresets.com" {
			return "", false
		}
	} else if parsed.Host != "" || parsed.Scheme != "" {
		return "", false
	}
	if !announcementEventPath.MatchString(parsed.Path) {
		return "", false
	}
	return "https://tokenresets.com" + parsed.Path, true
}

func validAnnouncementETag(value string) bool {
	if value == "" {
		return false
	}
	if len(value) > 200 {
		return false
	}
	if strings.HasPrefix(value, "W/") {
		value = value[2:]
	}
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for _, r := range value[1 : len(value)-1] {
		if r < 0x21 || r > 0x7e || r == '"' {
			return false
		}
	}
	return true
}

func parseAnnouncementRetryAfter(value string, now time.Time) time.Duration {
	var delay time.Duration
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		maxSeconds := int64(announcementMaxRetryAfter / time.Second)
		if seconds >= maxSeconds {
			return announcementMaxRetryAfter
		}
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil && at.After(now) {
		delay = at.Sub(now)
	}
	if delay > announcementMaxRetryAfter {
		return announcementMaxRetryAfter
	}
	return delay
}

func loadAnnouncementCache(path string, now time.Time) (announcementCache, bool) {
	data, err := readBoundedAnnouncementFile(path)
	if err != nil {
		return announcementCache{}, false
	}
	var cache announcementCache
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&cache) != nil || ensureAnnouncementJSONEOF(decoder) != nil || cache.Version != announcementCacheVersion || len(cache.Events) > announcementMaxEvents {
		return announcementCache{}, false
	}
	fetchedAt, err := parseAnnouncementTime(cache.FetchedAt)
	if err != nil || fetchedAt.After(now.Add(5*time.Minute)) {
		return announcementCache{}, false
	}
	generatedAt, err := parseAnnouncementTime(cache.GeneratedAt)
	if err != nil || generatedAt.After(now.Add(5*time.Minute)) {
		return announcementCache{}, false
	}
	if cache.ETag != "" && !validAnnouncementETag(cache.ETag) {
		cache.ETag = ""
	}
	validatedByID := make(map[string]AnnouncementEvent, len(cache.Events))
	for _, event := range cache.Events {
		if event, ok := validateCachedAnnouncement(event, now); ok {
			if previous, exists := validatedByID[event.ID]; !exists || event.Revision > previous.Revision {
				validatedByID[event.ID] = event
			}
		}
	}
	validated := make([]AnnouncementEvent, 0, len(validatedByID))
	seen := make(map[string]bool, len(validatedByID))
	for _, event := range cache.Events {
		selected, ok := validatedByID[event.ID]
		if ok && !seen[event.ID] && event.Revision == selected.Revision {
			validated = append(validated, selected)
			seen[event.ID] = true
		}
	}
	cache.FetchedAt = fetchedAt.UTC().Format(time.RFC3339)
	cache.GeneratedAt = generatedAt.UTC().Format(time.RFC3339)
	cache.Events = validated
	return cache, true
}

func loadAnnouncementState(path string, now time.Time) (announcementRefreshState, error) {
	data, err := readBoundedAnnouncementFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return announcementRefreshState{Version: announcementStateVersion}, nil
	}
	if err != nil {
		return announcementRefreshState{}, err
	}
	var state announcementRefreshState
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&state) != nil || ensureAnnouncementJSONEOF(decoder) != nil || state.Version != announcementStateVersion {
		return announcementRefreshState{}, errors.New("announcement refresh state is invalid")
	}
	if state.NextAttemptAt != "" {
		next, err := parseAnnouncementTime(state.NextAttemptAt)
		if err != nil || next.After(now.Add(announcementMaxRetryAfter+time.Minute)) {
			return announcementRefreshState{}, errors.New("announcement refresh state is invalid")
		}
		state.NextAttemptAt = next.UTC().Format(time.RFC3339Nano)
	}
	return state, nil
}

func readBoundedAnnouncementFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("announcement file is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, announcementMaxBytes+1))
	if err != nil || len(data) > announcementMaxBytes {
		return nil, errors.New("announcement file is invalid")
	}
	return data, nil
}

func saveAnnouncementCache(path string, cache announcementCache) error {
	if !filepath.IsAbs(path) {
		return errors.New("announcement cache path is invalid")
	}
	data, err := json.Marshal(cache)
	if err != nil || len(data) > announcementMaxBytes {
		return errors.New("announcement cache is invalid")
	}
	return atomicWriteUsageRefreshFile(path, data)
}

func saveAnnouncementState(path string, state announcementRefreshState) error {
	if !filepath.IsAbs(path) {
		return errors.New("announcement state path is invalid")
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > 4096 {
		return errors.New("announcement state is invalid")
	}
	return atomicWriteUsageRefreshFile(path, data)
}

func parseAnnouncementTime(value string) (time.Time, error) {
	if value == "" || len(value) > 64 {
		return time.Time{}, errors.New("announcement timestamp is invalid")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New("announcement timestamp is invalid")
	}
	return parsed, nil
}

func ensureAnnouncementJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("announcement JSON has trailing data")
	}
	return nil
}
