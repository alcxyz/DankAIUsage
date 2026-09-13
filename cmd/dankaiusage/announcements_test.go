package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type announcementRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn announcementRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestAnnouncementsDisabledMakesNoRequest(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	calls := 0
	result := collectAnnouncements(false, now, filepath.Join(t.TempDir(), "cache.json"), filepath.Join(t.TempDir(), "state.json"), announcementRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	}))
	if calls != 0 || result.Available || result.Stale || result.Message != "Announcements are disabled." || result.Events == nil {
		t.Fatalf("disabled result = %+v, calls = %d", result, calls)
	}
}

func TestAnnouncementsFetchIsFixedBoundedAndAllowlisted(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	cachePath, statePath := announcementTestPaths(t)
	body := announcementFixture(t, now, []map[string]any{
		announcementFixtureEvent("one", "openai-codex", "published", "/events/openai-codex-hard-reset-2026-09-12-test"),
		announcementFixtureEvent("revoked", "anthropic-claude", "retracted", "/events/anthropic-claude-hard-reset-2026-09-12-test"),
		announcementFixtureEvent("bad-link", "anthropic-claude", "published", "https://tokenresets.com/events/item?token=secret"),
	})
	requests := 0
	transport := announcementRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.String() != announcementEndpoint {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatalf("request sent private headers: %+v", request.Header)
		}
		state, err := loadAnnouncementState(statePath, now)
		if err != nil || !state.Failed {
			t.Fatalf("request was not durably reserved first: %+v (%v)", state, err)
		}
		return announcementResponse(http.StatusOK, body, map[string]string{"ETag": `"revision-1"`}), nil
	})

	result := collectAnnouncements(true, now, cachePath, statePath, transport)
	if requests != 1 || !result.Available || result.Stale || result.Message != "" || len(result.Events) != 1 {
		t.Fatalf("result = %+v, requests = %d", result, requests)
	}
	event := result.Events[0]
	if event.Provider != "codex" || event.Kind != "hard_reset" || event.ScopeLabel != "Plans: plus" || event.URL != "https://tokenresets.com/events/openai-codex-hard-reset-2026-09-12-test" {
		t.Fatalf("event = %+v", event)
	}
	cacheData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cacheData, []byte("server-secret")) || bytes.Contains(cacheData, []byte("credential")) {
		t.Fatalf("cache retained non-allowlisted remote fields: %s", cacheData)
	}
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stateData, []byte("one")) || bytes.Contains(stateData, []byte("revision-1")) {
		t.Fatalf("refresh reservation retained feed data: %s", stateData)
	}
}

func TestAnnouncementsDeduplicatesHighestRevisionAndHonorsRetraction(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	low := announcementFixtureEvent("same", "openai-codex", "published", "/events/old")
	low["revision"] = 1
	high := announcementFixtureEvent("same", "openai-codex", "published", "/events/new")
	high["revision"] = 2
	withdrawn := announcementFixtureEvent("withdrawn", "anthropic-claude", "published", "/events/withdrawn")
	withdrawn["revision"] = 1
	withdrawnHigh := announcementFixtureEvent("withdrawn", "anthropic-claude", "canceled", "/events/withdrawn")
	withdrawnHigh["revision"] = 2
	_, events, err := parseAnnouncementFeed(announcementFixture(t, now, []map[string]any{low, high, withdrawn, withdrawnHigh}), now)
	if err != nil || len(events) != 1 || events[0].URL != "https://tokenresets.com/events/new" || events[0].Revision != 2 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
}

func TestAnnouncementsReturnAtMostFiftyEvents(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	events := make([]map[string]any, 0, 60)
	for i := 0; i < 60; i++ {
		events = append(events, announcementFixtureEvent(string(rune('a'+i/26))+string(rune('a'+i%26)), "openai-codex", "published", "/events/item-"+string(rune('a'+i/26))+string(rune('a'+i%26))))
	}
	_, parsed, err := parseAnnouncementFeed(announcementFixture(t, now, events), now)
	if err != nil || len(parsed) != announcementMaxEvents {
		t.Fatalf("events = %d, err = %v", len(parsed), err)
	}
}

func TestAnnouncementsFreshCollectionReplacesRevokedCache(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	cachePath, statePath := announcementTestPaths(t)
	first := announcementFixture(t, now, []map[string]any{announcementFixtureEvent("one", "openai-codex", "published", "/events/one")})
	second := announcementFixture(t, now.Add(16*time.Minute), []map[string]any{announcementFixtureEvent("one", "openai-codex", "retracted", "/events/one")})
	bodies := [][]byte{first, second}
	transport := announcementRoundTripFunc(func(*http.Request) (*http.Response, error) {
		body := bodies[0]
		bodies = bodies[1:]
		return announcementResponse(http.StatusOK, body, nil), nil
	})
	if got := collectAnnouncements(true, now, cachePath, statePath, transport); len(got.Events) != 1 {
		t.Fatalf("first result = %+v", got)
	}
	got := collectAnnouncements(true, now.Add(16*time.Minute), cachePath, statePath, transport)
	if !got.Available || len(got.Events) != 0 {
		t.Fatalf("revoked event survived fresh replacement: %+v", got)
	}
}

func TestAnnouncementsRejectRedirectOversizeAndMalformedResponses(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	tests := []struct {
		name     string
		response *http.Response
	}{
		{"redirect", announcementResponse(http.StatusFound, nil, map[string]string{"Location": "https://example.com/private"})},
		{"oversize", announcementResponse(http.StatusOK, bytes.Repeat([]byte("x"), announcementMaxBytes+1), nil)},
		{"malformed", announcementResponse(http.StatusOK, []byte(`{"data":[],"meta":{"schema_version":"wrong"}}`), nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cachePath, statePath := announcementTestPaths(t)
			calls := 0
			result := collectAnnouncements(true, now, cachePath, statePath, announcementRoundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return test.response, nil
			}))
			if result.Available || !strings.Contains(result.Message, "temporarily unavailable") || calls != 1 {
				t.Fatalf("result = %+v, calls = %d", result, calls)
			}
		})
	}
}

func TestAnnouncementsStaleMetadataAndCacheRevalidation(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	cachePath, statePath := announcementTestPaths(t)
	body := announcementFixture(t, now.Add(-25*time.Hour), []map[string]any{announcementFixtureEvent("one", "anthropic-claude", "published", "/events/one")})
	result := collectAnnouncements(true, now, cachePath, statePath, announcementRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return announcementResponse(http.StatusOK, body, nil), nil
	}))
	if !result.Available || !result.Stale || result.Message != "Announcements may be out of date." {
		t.Fatalf("stale result = %+v", result)
	}

	var cache announcementCache
	data, err := os.ReadFile(cachePath)
	if err != nil || json.Unmarshal(data, &cache) != nil {
		t.Fatal("could not read cache")
	}
	cache.Events[0].URL = "https://evil.example/events/credential"
	data, _ = json.Marshal(cache)
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	result = collectAnnouncements(true, now.Add(time.Minute), cachePath, statePath, announcementRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("cooldown should prevent a request")
		return nil, nil
	}))
	if !result.Available || len(result.Events) != 0 || !result.Stale {
		t.Fatalf("untrusted cached URL was returned: %+v", result)
	}
}

func TestAnnouncementsFailedRefreshRemainsStaleThroughoutCooldown(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	cachePath, statePath := announcementTestPaths(t)
	body := announcementFixture(t, now, []map[string]any{announcementFixtureEvent("one", "openai-codex", "published", "/events/one")})
	calls := 0
	transport := announcementRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return announcementResponse(http.StatusOK, body, nil), nil
		}
		return announcementResponse(http.StatusBadGateway, nil, nil), nil
	})
	_ = collectAnnouncements(true, now, cachePath, statePath, transport)
	failed := collectAnnouncements(true, now.Add(16*time.Minute), cachePath, statePath, transport)
	cooling := collectAnnouncements(true, now.Add(17*time.Minute), cachePath, statePath, transport)
	if calls != 2 || !failed.Stale || !cooling.Stale || !strings.Contains(cooling.Message, "cached") {
		t.Fatalf("failed/cooling results = %+v / %+v, calls = %d", failed, cooling, calls)
	}
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	missing := collectAnnouncements(true, now.Add(18*time.Minute), cachePath, statePath, transport)
	if calls != 2 || missing.Available {
		t.Fatalf("cache removal bypassed reservation: %+v, calls = %d", missing, calls)
	}
}

func TestAnnouncementsConditionalETagAndNotModified(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	cachePath, statePath := announcementTestPaths(t)
	body := announcementFixture(t, now, []map[string]any{announcementFixtureEvent("one", "openai-codex", "published", "/events/one")})
	calls := 0
	transport := announcementRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return announcementResponse(http.StatusOK, body, map[string]string{"ETag": `W/"one"`}), nil
		}
		if request.Header.Get("If-None-Match") != `W/"one"` {
			t.Fatalf("If-None-Match = %q", request.Header.Get("If-None-Match"))
		}
		return announcementResponse(http.StatusNotModified, nil, nil), nil
	})
	_ = collectAnnouncements(true, now, cachePath, statePath, transport)
	result := collectAnnouncements(true, now.Add(16*time.Minute), cachePath, statePath, transport)
	if calls != 2 || !result.Available || result.Stale || len(result.Events) != 1 || result.FetchedAt != now.Add(16*time.Minute).Format(time.RFC3339) {
		t.Fatalf("304 result = %+v, calls = %d", result, calls)
	}
}

func TestAnnouncementsRetryAfterIsDurableAndCapped(t *testing.T) {
	now := mustParseTime(t, "2026-09-12T14:00:00Z")
	cachePath, statePath := announcementTestPaths(t)
	calls := 0
	transport := announcementRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return announcementResponse(http.StatusTooManyRequests, nil, map[string]string{"Retry-After": "999999"}), nil
	})
	first := collectAnnouncements(true, now, cachePath, statePath, transport)
	second := collectAnnouncements(true, now.Add(23*time.Hour), cachePath, statePath, transport)
	if first.Available || second.Available || calls != 1 {
		t.Fatalf("rate limit results = %+v / %+v, calls = %d", first, second, calls)
	}
	state, err := loadAnnouncementState(statePath, now)
	if err != nil {
		t.Fatal(err)
	}
	next, err := parseAnnouncementTime(state.NextAttemptAt)
	if err != nil || !next.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("next attempt = %v (%v)", next, err)
	}
}

func announcementTestPaths(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	return filepath.Join(root, "cache", "announcements.json"), filepath.Join(root, "state", "announcements-refresh.json")
}

func announcementFixture(t *testing.T, generatedAt time.Time, events []map[string]any) []byte {
	t.Helper()
	value := map[string]any{
		"data":       events,
		"meta":       map[string]any{"generated_at": generatedAt.Format(time.RFC3339), "schema_version": "1.0"},
		"credential": "server-secret",
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func announcementFixtureEvent(id, provider, status, link string) map[string]any {
	return map[string]any{
		"id":                    id,
		"provider":              map[string]any{"slug": provider, "credential": "server-secret"},
		"event_type":            "hard_reset",
		"status":                status,
		"title":                 "Usage limits reset",
		"summary":               "A public provider update.",
		"announced_at":          "2026-09-12T12:00:00Z",
		"effective_at":          nil,
		"observed_effective_at": "2026-09-12T13:00:00Z",
		"expected_by":           nil,
		"expires_at":            nil,
		"scope":                 map[string]any{"products": []string{}, "plans": []string{"plus"}, "windows": []string{}},
		"confidence":            map[string]any{"label": "verified", "credential": "server-secret"},
		"revision":              2,
		"links":                 map[string]any{"html": link, "credential": "server-secret"},
		"credential":            "server-secret",
	}
}

func announcementResponse(status int, body []byte, headers map[string]string) *http.Response {
	header := make(http.Header)
	for key, value := range headers {
		header.Set(key, value)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}
