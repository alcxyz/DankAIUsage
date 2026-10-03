package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic usage body in the shape of the OAuth usage endpoint with
// cedar_ember=1. Never derived from a live account.
const claudeResetUsageFixture = `{
  "five_hour": {"utilization": 25, "resets_at": "2026-10-03T06:00:00Z"},
  "seven_day": {"utilization": 16, "resets_at": "2026-10-09T11:00:00Z"},
  "cedar_ember": {
    "eligible": true,
    "ineligible_reason": null,
    "at_limit": false,
    "exhausted": [],
    "grants": [{
      "id": "launch-grant-1",
      "label": "Launch reset",
      "resets_total": 1,
      "resets_left": 1,
      "starts_at": "2026-09-22T16:00:00+00:00",
      "ends_at": "2026-10-22T16:00:00+00:00",
      "clears": ["five_hour", "seven_day", "seven_day_overage_included", "bogus"],
      "paused": false,
      "usable_now": true,
      "use_requires_limit": false,
      "percent_used": {"five_hour": 25}
    }, {
      "id": "Bad Id!",
      "resets_left": 1
    }, {
      "id": "spent-grant",
      "resets_left": 0
    }],
    "next_grant_id": "launch-grant-1",
    "weekly_resets_at": "2026-10-09T11:00:00+00:00",
    "cooldown_until": null
  }
}`

func writeClaudeResetUsageFixture(t *testing.T, dir string, body string) string {
	t.Helper()
	path := filepath.Join(dir, "claude-oauth-usage.json")
	cache := claudeOAuthUsageCache{
		FetchedAt:     "2026-10-03T05:00:00Z",
		NextAttemptAt: "2026-10-03T05:05:00Z",
		Body:          json.RawMessage(body),
		ClaudeVersion: "0.0.0-test",
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testClaudeResetDeps(t *testing.T, dir string, claim func(claudeResetClaimRequest) (claudeResetClaimResult, error)) (claudeResetDeps, *[]string) {
	t.Helper()
	var expired []string
	deps := claudeResetDeps{
		Now:            func() time.Time { return mustParseTime(t, "2026-10-03T05:01:00Z") },
		StatePath:      filepath.Join(dir, "claude-reset.json"),
		UsageCachePath: filepath.Join(dir, "claude-oauth-usage.json"),
		ReadToken:      func(time.Time) (string, string, error) { return "synthetic-test-only", "max", nil },
		OrganizationID: func() (string, error) { return "org-test", nil },
		ClaudeVersion:  func(string) string { return "0.0.0-test" },
		Claim:          claim,
		ExpireUsage: func(path string, _ time.Time) error {
			expired = append(expired, path)
			return nil
		},
	}
	return deps, &expired
}

func TestParseClaudeResetAvailability(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal([]byte(claudeResetUsageFixture), &root); err != nil {
		t.Fatal(err)
	}
	availability := parseClaudeResetAvailability(root)
	if !availability.Reported || !availability.Eligible || availability.AtLimit {
		t.Fatalf("unexpected availability: %+v", availability)
	}
	if len(availability.Grants) != 2 {
		t.Fatalf("malformed grant id should be dropped, got %+v", availability.Grants)
	}
	grant := availability.Grants[0]
	if grant.ID != "launch-grant-1" || grant.ResetsLeft != 1 || grant.UseRequiresLimit || !grant.UsableNow {
		t.Fatalf("unexpected grant: %+v", grant)
	}
	if strings.Join(grant.Clears, ",") != "five_hour,seven_day,seven_day_overage_included" {
		t.Fatalf("unknown limit kinds must be filtered: %v", grant.Clears)
	}
	if grant.EndsAt != "2026-10-22T16:00:00Z" {
		t.Fatalf("ends_at should normalise to RFC3339 UTC: %q", grant.EndsAt)
	}
	if availability.NextGrantID != "launch-grant-1" {
		t.Fatalf("next grant: %q", availability.NextGrantID)
	}

	now := mustParseTime(t, "2026-10-03T05:01:00Z")
	resets := claudeAvailableResets(availability, now)
	if len(resets) != 1 || resets[0].ResetType != claudeResetType || resets[0].ID != "launch-grant-1" || resets[0].ExpiresAt != "2026-10-22T16:00:00Z" {
		t.Fatalf("unexpected resets: %+v", resets)
	}
	if !strings.Contains(resets[0].Description, "clears 5-hour and weekly") || !strings.Contains(resets[0].Description, "usable any time") {
		t.Fatalf("description: %q", resets[0].Description)
	}
	meta := claudeResetMeta(availability, now)
	if meta["availableResetCount"] != 1 {
		t.Fatalf("availableResetCount: %v", meta["availableResetCount"])
	}
	inner := meta["claudeReset"].(map[string]any)
	if inner["eligible"] != true || inner["nextGrantId"] != "launch-grant-1" {
		t.Fatalf("claudeReset meta: %+v", inner)
	}

	expired := claudeAvailableResets(availability, mustParseTime(t, "2026-10-23T00:00:00Z"))
	if len(expired) != 0 {
		t.Fatalf("expired grants must not be spendable: %+v", expired)
	}
}

func TestParseClaudeResetAvailabilityIneligible(t *testing.T) {
	root := map[string]any{"cedar_ember": map[string]any{
		"eligible": false, "ineligible_reason": "surface", "grants": []any{map[string]any{"id": "g1", "resets_left": float64(1)}},
	}}
	availability := parseClaudeResetAvailability(root)
	if !availability.Reported || availability.Eligible || availability.IneligibleReason != "surface" {
		t.Fatalf("unexpected availability: %+v", availability)
	}
	now := mustParseTime(t, "2026-10-03T05:01:00Z")
	if resets := claudeAvailableResets(availability, now); len(resets) != 0 {
		t.Fatalf("ineligible accounts must list no spendable resets: %+v", resets)
	}
	meta := claudeResetMeta(availability, now)
	if _, ok := meta["availableResetCount"]; ok {
		t.Fatal("ineligible accounts must not report a spendable count")
	}
	if meta["claudeReset"].(map[string]any)["ineligibleReason"] != "surface" {
		t.Fatalf("meta: %+v", meta)
	}
	if plain := parseClaudeResetAvailability(map[string]any{"five_hour": map[string]any{}}); plain.Reported {
		t.Fatal("missing block must not be reported")
	}
}

func TestRunClaudeResetActionUseSuccess(t *testing.T) {
	dir := t.TempDir()
	writeClaudeResetUsageFixture(t, dir, claudeResetUsageFixture)
	var requests []claudeResetClaimRequest
	left := 0
	deps, expired := testClaudeResetDeps(t, dir, func(request claudeResetClaimRequest) (claudeResetClaimResult, error) {
		requests = append(requests, request)
		return claudeResetClaimResult{Result: "reset", ResetsLeft: &left, Cleared: []string{"five_hour", "seven_day"}}, nil
	})

	status, err := runClaudeResetAction("status", "", deps)
	if err != nil || !status.StateKnown || status.State != "idle" || !status.Eligible || len(status.Grants) != 2 {
		t.Fatalf("status: %+v error=%v", status, err)
	}

	status, err = runClaudeResetAction("use", "", deps)
	if err != nil {
		t.Fatalf("use: %+v error=%v", status, err)
	}
	if len(requests) != 1 || requests[0].GrantID != "launch-grant-1" || requests[0].OrganizationID != "org-test" || requests[0].Token != "synthetic-test-only" {
		t.Fatalf("unexpected claim requests: %+v", requests)
	}
	if !claudeResetRequestIDPattern.MatchString(requests[0].RequestID) {
		t.Fatalf("request id: %q", requests[0].RequestID)
	}
	if status.State != "used" || status.Outcome != "reset" || !status.UsageRefreshed || status.ResetsLeft == nil || *status.ResetsLeft != 0 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if !strings.Contains(status.Message, "Limits reset") || !strings.Contains(status.Message, "5-hour and weekly") {
		t.Fatalf("message: %q", status.Message)
	}
	if len(*expired) != 1 || (*expired)[0] != deps.UsageCachePath {
		t.Fatalf("usage cache should expire once after success: %v", *expired)
	}
	saved, loadErr := loadClaudeResetState(deps.StatePath)
	if loadErr != nil || saved.State != "used" || saved.GrantID != "launch-grant-1" || saved.RequestID != requests[0].RequestID {
		t.Fatalf("saved state: %+v error=%v", saved, loadErr)
	}
	info, statErr := os.Stat(deps.StatePath)
	if statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions: %v %v", info, statErr)
	}
}

func TestRunClaudeResetActionRetryReusesRequestID(t *testing.T) {
	dir := t.TempDir()
	writeClaudeResetUsageFixture(t, dir, claudeResetUsageFixture)
	var requests []claudeResetClaimRequest
	fail := true
	deps, expired := testClaudeResetDeps(t, dir, func(request claudeResetClaimRequest) (claudeResetClaimResult, error) {
		requests = append(requests, request)
		if fail {
			return claudeResetClaimResult{}, errors.New("Claude reset request failed: timeout")
		}
		return claudeResetClaimResult{Result: "already_used"}, nil
	})

	status, err := runClaudeResetAction("use", "", deps)
	if err == nil || status.State != "attempted" || status.Outcome != "error" || len(*expired) != 0 {
		t.Fatalf("transport failure should keep the attempt pending: %+v error=%v", status, err)
	}
	fail = false
	status, err = runClaudeResetAction("use", "launch-grant-1", deps)
	if err != nil || status.State != "used" || status.Outcome != "already_used" || !status.UsageRefreshed {
		t.Fatalf("retry: %+v error=%v", status, err)
	}
	if len(requests) != 2 || requests[0].RequestID != requests[1].RequestID {
		t.Fatalf("retry must reuse the request id: %+v", requests)
	}
}

func TestRunClaudeResetActionRefusesWithoutEligibleGrant(t *testing.T) {
	dir := t.TempDir()
	ineligible := strings.Replace(claudeResetUsageFixture, `"eligible": true`, `"eligible": false`, 1)
	ineligible = strings.Replace(ineligible, `"ineligible_reason": null`, `"ineligible_reason": "surface"`, 1)
	writeClaudeResetUsageFixture(t, dir, ineligible)
	calls := 0
	deps, expired := testClaudeResetDeps(t, dir, func(claudeResetClaimRequest) (claudeResetClaimResult, error) {
		calls++
		return claudeResetClaimResult{Result: "reset"}, nil
	})
	status, err := runClaudeResetAction("use", "", deps)
	if err == nil || calls != 0 || len(*expired) != 0 || status.State != "idle" {
		t.Fatalf("ineligible account must not send a claim: %+v error=%v calls=%d", status, err, calls)
	}
	if !strings.Contains(err.Error(), "claude.ai") {
		t.Fatalf("surface ineligibility should point at the web settings: %v", err)
	}

	writeClaudeResetUsageFixture(t, dir, claudeResetUsageFixture)
	status, err = runClaudeResetAction("use", "spent-grant", deps)
	if err == nil || calls != 0 || status.State != "idle" {
		t.Fatalf("spent grant must not be claimed: %+v error=%v", status, err)
	}
	if _, err := runClaudeResetAction("use", "", claudeResetDeps{StatePath: filepath.Join(t.TempDir(), "claude-reset.json"), Claim: deps.Claim}); err == nil || calls != 0 {
		t.Fatalf("missing usage cache must refuse: %v", err)
	}
	if _, err := runClaudeResetAction("bogus", "", deps); err == nil {
		t.Fatal("unknown action must fail")
	}
}

func TestRunClaudeResetActionOutcomes(t *testing.T) {
	cases := []struct {
		result    string
		wantState string
		wantErr   bool
		refreshed bool
	}{
		{"not_limited", "idle", false, false},
		{"cooldown", "idle", false, false},
		{"ineligible", "idle", true, false},
		{"unavailable", "failed", true, false},
		{"rate_limited", "failed", true, false},
		{"auth_error", "failed", true, false},
		{"already_used", "used", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.result, func(t *testing.T) {
			dir := t.TempDir()
			writeClaudeResetUsageFixture(t, dir, claudeResetUsageFixture)
			deps, expired := testClaudeResetDeps(t, dir, func(claudeResetClaimRequest) (claudeResetClaimResult, error) {
				return claudeResetClaimResult{Result: tc.result, Reason: tc.result}, nil
			})
			status, err := runClaudeResetAction("use", "", deps)
			if (err != nil) != tc.wantErr || status.State != tc.wantState || status.Outcome != tc.result {
				t.Fatalf("status=%+v error=%v", status, err)
			}
			if (len(*expired) == 1) != tc.refreshed {
				t.Fatalf("usage expiry: %v", *expired)
			}
			if status.Message == "" {
				t.Fatal("outcome must carry a message")
			}
		})
	}
}

type claudeResetClaimTransport struct {
	requests []*http.Request
	bodies   []string
	status   int
	response string
}

func (transport *claudeResetClaimTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	transport.requests = append(transport.requests, request)
	transport.bodies = append(transport.bodies, string(body))
	return &http.Response{
		StatusCode: transport.status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(transport.response)),
		Request:    request,
	}, nil
}

func TestClaimClaudeResetRequest(t *testing.T) {
	transport := &claudeResetClaimTransport{status: http.StatusOK, response: `{"result":"reset","reason":null,"resets_left":0,"cleared":["five_hour","seven_day","nope"],"weekly_resets_at":"2026-10-09T11:00:00+00:00"}`}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })

	result, err := claimClaudeReset(claudeResetClaimRequest{
		Token: "synthetic-test-only", Version: "0.0.0-test", OrganizationID: "org-test",
		GrantID: "launch-grant-1", RequestID: "11111111-2222-4333-8444-555555555555",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != "reset" || result.ResetsLeft == nil || *result.ResetsLeft != 0 || strings.Join(result.Cleared, ",") != "five_hour,seven_day" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("requests: %d", len(transport.requests))
	}
	request := transport.requests[0]
	if request.Method != http.MethodPost || request.URL.String() != "https://api.anthropic.com/api/organizations/org-test/reset_rate_limits" {
		t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
	}
	if request.Header.Get("Authorization") != "Bearer synthetic-test-only" || request.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
		t.Fatalf("auth headers: %v", request.Header)
	}
	if request.Header.Get("User-Agent") != "claude-cli/0.0.0-test (external, cli)" {
		t.Fatalf("user agent: %q", request.Header.Get("User-Agent"))
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(transport.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["program"] != "cedar_ember" || body["grant_id"] != "launch-grant-1" || body["request_id"] != "11111111-2222-4333-8444-555555555555" || len(body) != 3 {
		t.Fatalf("body: %v", body)
	}

	if _, err := claimClaudeReset(claudeResetClaimRequest{Token: "x", OrganizationID: "org", GrantID: "Bad Id", RequestID: "r"}); err == nil || len(transport.requests) != 1 {
		t.Fatal("malformed grant id must not be sent")
	}
	transport.status = http.StatusTooManyRequests
	if result, err := claimClaudeReset(claudeResetClaimRequest{Token: "x", OrganizationID: "org", GrantID: "g", RequestID: "r"}); err != nil || result.Result != "rate_limited" {
		t.Fatalf("429: %+v %v", result, err)
	}
	transport.status = http.StatusForbidden
	if result, err := claimClaudeReset(claudeResetClaimRequest{Token: "x", OrganizationID: "org", GrantID: "g", RequestID: "r"}); err != nil || result.Result != "auth_error" {
		t.Fatalf("403: %+v %v", result, err)
	}
	transport.status = http.StatusBadGateway
	if _, err := claimClaudeReset(claudeResetClaimRequest{Token: "x", OrganizationID: "org", GrantID: "g", RequestID: "r"}); err == nil {
		t.Fatal("5xx must be an error so the attempt stays pending")
	}
	transport.status = http.StatusOK
	transport.response = `{"result":"something_new"}`
	if result, err := claimClaudeReset(claudeResetClaimRequest{Token: "x", OrganizationID: "org", GrantID: "g", RequestID: "r"}); err != nil || result.Result != "unavailable" {
		t.Fatalf("unknown result must map to unavailable: %+v %v", result, err)
	}
}

func TestClaudeUsageFetchUsesClaudeCodeClientUserAgent(t *testing.T) {
	transport, now := setupClaudeRefreshTest(t)
	if _, _, _, _, _, _, err := collectClaudeOAuthLimitsWithPolicy(now, time.Minute, false); err != nil {
		t.Fatal(err)
	}
	if transport.calls.Load() != 1 {
		t.Fatalf("calls: %d", transport.calls.Load())
	}
	if transport.lastUserAgent == "" || !strings.HasPrefix(transport.lastUserAgent, "claude-cli/") || !strings.HasSuffix(transport.lastUserAgent, " (external, cli)") {
		t.Fatalf("user agent: %q", transport.lastUserAgent)
	}
	if !strings.Contains(transport.lastURL, "cedar_ember=1") {
		t.Fatalf("usage URL should request limit resets: %q", transport.lastURL)
	}
}

func TestExpireClaudeUsageCacheAllowsImmediateRefresh(t *testing.T) {
	dir := t.TempDir()
	path := writeClaudeResetUsageFixture(t, dir, claudeResetUsageFixture)
	now := mustParseTime(t, "2026-10-03T05:01:00Z")
	if err := expireClaudeUsageCache(path, now); err != nil {
		t.Fatal(err)
	}
	cache, err := loadClaudeOAuthUsageCacheStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Body) != 0 || !cache.Invalidated || cache.NextAttemptAt != now.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("cache: %+v", cache)
	}
	if resets, meta := claudeResetsFromUsageCache(path, now); resets != nil || meta != nil {
		t.Fatal("expired cache must not report resets")
	}
}

func TestClaudeOrganizationIDReadsConfig(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("HOME", t.TempDir())
	if _, err := claudeOrganizationID(); err == nil {
		t.Fatal("missing config must fail")
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"oauthAccount":{"organizationUuid":"org-synthetic-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := claudeOrganizationID()
	if err != nil || id != "org-synthetic-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}
