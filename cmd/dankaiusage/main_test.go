package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLogFields(t *testing.T) {
	fields := parseLogFields(`event.name="codex.sse_event" input_token_count=123 output_token_count=45 cached_token_count=100 conversation.id=abc`)

	if fields["event.name"] != "codex.sse_event" {
		t.Fatalf("event.name = %q", fields["event.name"])
	}
	if got := intField(fields, "input_token_count"); got != 123 {
		t.Fatalf("input_token_count = %d", got)
	}
	if fields["conversation.id"] != "abc" {
		t.Fatalf("conversation.id = %q", fields["conversation.id"])
	}
}

func TestEventTotalProviderSemantics(t *testing.T) {
	codex := tokenEvent{Provider: "codex", Input: 100, Output: 20, Cached: 80, Reasoning: 5, Tool: 10}
	if got := eventTotal(codex); got != 120 {
		t.Fatalf("codex total = %d", got)
	}

	claude := tokenEvent{Provider: "claude", Input: 100, Output: 20, Cached: 80}
	if got := eventTotal(claude); got != 200 {
		t.Fatalf("claude total = %d", got)
	}
}

func TestMakeAllowance(t *testing.T) {
	resetAt := mustParseTime(t, "2026-05-28T12:00:00Z")
	allowance := makeAllowance("session", 75, 100, resetAt)

	if !allowance.Known {
		t.Fatal("allowance should be known")
	}
	if allowance.Remaining != 25 {
		t.Fatalf("remaining = %d", allowance.Remaining)
	}
	if allowance.PercentRemaining != 25 {
		t.Fatalf("percent remaining = %f", allowance.PercentRemaining)
	}

	unknown := makeAllowance("weekly", 75, 0, resetAt)
	if unknown.Known {
		t.Fatal("zero limit should be unknown")
	}
}

func TestCodexSnapshotAllowances(t *testing.T) {
	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	sessionReset := int64(1779994842)
	weeklyReset := int64(1780528901)
	sessionMins := int64(300)
	weeklyMins := int64(10080)
	snapshot := codexRateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "pro",
		Primary: &codexRateLimitWindow{
			UsedPercent:        12.5,
			WindowDurationMins: &sessionMins,
			ResetsAt:           &sessionReset,
		},
		Secondary: &codexRateLimitWindow{
			UsedPercent:        44,
			WindowDurationMins: &weeklyMins,
			ResetsAt:           &weeklyReset,
		},
	}

	session := codexSnapshotAllowances(snapshot, now)
	if !session.Known || session.Unit != "percent" || session.PercentRemaining != 87.5 {
		t.Fatalf("session allowance = %+v", session)
	}
	if session.WindowMinutes != 300 {
		t.Fatalf("session window minutes = %d", session.WindowMinutes)
	}
	weekly := codexSnapshotWeeklyAllowance(snapshot, now)
	if !weekly.Known || weekly.PercentRemaining != 56 {
		t.Fatalf("weekly allowance = %+v", weekly)
	}
}

func TestCodexSnapshotWeeklyOnly(t *testing.T) {
	now := mustParseTime(t, "2026-08-30T12:00:00Z")
	weeklyReset := int64(1788648672)
	weeklyMins := int64(10080)
	snapshot := codexRateLimitSnapshot{
		LimitID: "codex",
		Primary: &codexRateLimitWindow{
			UsedPercent:        1,
			WindowDurationMins: &weeklyMins,
			ResetsAt:           &weeklyReset,
		},
	}

	session, weekly := codexSnapshotWindowAllowances(snapshot, now)
	if session.Known || session.ResetAt != "" {
		t.Fatalf("weekly-only response fabricated a session allowance: %+v", session)
	}
	if !weekly.Known || weekly.PercentRemaining != 99 || weekly.WindowMinutes != 10080 {
		t.Fatalf("weekly allowance = %+v", weekly)
	}

	provider := ProviderUsage{ID: "codex", SessionLeft: session, WeeklyLeft: weekly}
	applyEvents(&provider, nil, now, options{PeriodDays: 7, SessionHours: 5})
	if provider.SessionLeft.Known || provider.SessionLeft.ResetAt != "" {
		t.Fatalf("local token history fabricated a session window: %+v", provider.SessionLeft)
	}
}

func TestCodexSnapshotExtraLimits(t *testing.T) {
	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	sessionReset := int64(1779994842)
	weeklyReset := int64(1780528901)
	sessionMins := int64(300)
	weeklyMins := int64(10080)
	extras := codexSnapshotExtraLimits(map[string]codexRateLimitSnapshot{
		"codex": {
			LimitID: "codex",
		},
		"gpt-5.3-codex-spark": {
			LimitID:   "gpt-5.3-codex-spark",
			LimitName: "GPT-5.3-Codex-Spark",
			Primary: &codexRateLimitWindow{
				UsedPercent:        25,
				WindowDurationMins: &sessionMins,
				ResetsAt:           &sessionReset,
			},
			Secondary: &codexRateLimitWindow{
				UsedPercent:        80,
				WindowDurationMins: &weeklyMins,
				ResetsAt:           &weeklyReset,
			},
		},
	}, "codex", now)

	if len(extras) != 1 {
		t.Fatalf("extra limits = %+v", extras)
	}
	spark := extras[0]
	if spark.ID != "gpt-5.3-codex-spark" || spark.Label != "GPT-5.3-Codex-Spark" {
		t.Fatalf("spark metadata = %+v", spark)
	}
	if spark.Session == nil || spark.Session.PercentRemaining != 75 || spark.Session.WindowMinutes != 300 {
		t.Fatalf("spark session = %+v", spark.Session)
	}
	if spark.Weekly == nil || spark.Weekly.PercentRemaining != 20 || spark.Weekly.WindowMinutes != 10080 {
		t.Fatalf("spark weekly = %+v", spark.Weekly)
	}
}

func TestCodexAvailableResets(t *testing.T) {
	now := mustParseTime(t, "2026-08-30T12:00:00Z")
	credits := codexRateLimitResetCredits{
		AvailableCount: 2,
		Credits: []codexRateLimitResetCredit{
			{
				ID:          "reset-1",
				ResetType:   "codexRateLimits",
				Status:      "available",
				ExpiresAt:   mustParseTime(t, "2026-09-21T12:00:00Z").Unix(),
				Title:       "Full reset",
				Description: "Refreshes eligible Codex limits.",
			},
			{
				ID:        "reset-expired",
				Status:    "available",
				ExpiresAt: mustParseTime(t, "2026-08-01T12:00:00Z").Unix(),
			},
		},
	}

	resets := codexAvailableResets(credits, now)
	if len(resets) != 1 {
		t.Fatalf("available resets = %+v", resets)
	}
	expiresAt, err := time.Parse(time.RFC3339, resets[0].ExpiresAt)
	if err != nil || resets[0].Title != "Full reset" || !expiresAt.Equal(mustParseTime(t, "2026-09-21T12:00:00Z")) {
		t.Fatalf("reset = %+v", resets[0])
	}
}

func TestMakeQuotaBucketsFlattensProviderLimits(t *testing.T) {
	now := mustParseTime(t, "2026-08-30T12:00:00Z")
	session := makeSubscriptionAllowance("session", "test", 10, now.Add(5*time.Hour), 300)
	weekly := makeSubscriptionAllowance("weekly", "test", 20, now.Add(7*24*time.Hour), 10080)
	scopedWeekly := makeSubscriptionAllowance("weekly", "test", 30, now.Add(7*24*time.Hour), 10080)
	additional := QuotaBucket{
		ID:        "credits",
		Label:     "Extra usage credits",
		Kind:      "credits",
		Allowance: makeAllowance("monthly", 25, 100, time.Time{}),
	}

	buckets := makeQuotaBuckets(session, weekly, []ExtraLimit{{
		ID:     "preview",
		Label:  "Preview",
		Weekly: &scopedWeekly,
	}}, []QuotaBucket{additional})

	if len(buckets) != 4 {
		t.Fatalf("quota buckets = %+v", buckets)
	}
	if buckets[0].Label != "5-hour" || buckets[1].Label != "Weekly" || buckets[2].Label != "Preview · weekly" || buckets[3].Kind != "credits" {
		t.Fatalf("quota bucket order = %+v", buckets)
	}
}

func TestParseClaudeStatusline(t *testing.T) {
	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	data := []byte(`{
		"version": "2.1.152",
		"model": {"display_name": "Sonnet"},
		"rate_limits": {
			"five_hour": {"used_percentage": 20, "resets_at": "2026-05-28T17:00:00Z"},
			"seven_day": {"usedPercent": 75, "resetsAt": 1780528901}
		}
	}`)

	limits, err := parseClaudeStatusline(data, now)
	if err != nil {
		t.Fatal(err)
	}
	if limits.Model != "Sonnet" || limits.Version != "2.1.152" {
		t.Fatalf("metadata = %+v", limits)
	}
	if limits.Session.PercentRemaining != 80 {
		t.Fatalf("session remaining = %f", limits.Session.PercentRemaining)
	}
	if limits.Weekly.PercentRemaining != 25 {
		t.Fatalf("weekly remaining = %f", limits.Weekly.PercentRemaining)
	}
}

func TestParseClaudeOAuthUsage(t *testing.T) {
	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	data := []byte(`{
		"five_hour": {"utilization": 6.0, "resets_at": "2026-07-02T15:59:59.943648+00:00"},
		"seven_day": {"utilization": 35.0, "resets_at": "2026-07-06T03:59:59.943679+00:00"},
		"seven_day_opus": {"utilization": 0.0, "resets_at": null},
		"seven_day_oauth_apps": null
	}`)

	session, weekly, _, _, err := parseClaudeOAuthUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Known || session.PercentRemaining != 94 {
		t.Fatalf("session = %+v", session)
	}
	if session.Source != claudeOAuthUsageSource || session.WindowMinutes != 300 {
		t.Fatalf("session metadata = %+v", session)
	}
	if !weekly.Known || weekly.PercentRemaining != 65 || weekly.WindowMinutes != 10080 {
		t.Fatalf("weekly = %+v", weekly)
	}
	reset, err := time.Parse(time.RFC3339, session.ResetAt)
	if err != nil {
		t.Fatal(err)
	}
	if !reset.Equal(mustParseTime(t, "2026-07-02T15:59:59Z")) {
		t.Fatalf("session reset = %s", session.ResetAt)
	}
}

func TestParseClaudeOAuthUsageScopedWeeklyLimit(t *testing.T) {
	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	data := []byte(`{
		"five_hour": {"utilization": 6.0, "resets_at": "2026-07-02T15:59:59Z"},
		"seven_day": {"utilization": 35.0, "resets_at": "2026-07-06T03:59:59Z"},
		"limits": [
			{"kind": "session", "utilization": 6.0, "resets_at": "2026-07-02T15:59:59Z"},
			{"kind": "weekly_all", "utilization": 35.0, "resets_at": "2026-07-06T03:59:59Z"},
			{
				"kind": "weekly_fable_only",
				"utilization": 42.0,
				"resets_at": "2026-07-07T00:00:00Z",
				"scope": {"model": {"id": "claude-fable", "display_name": "Fable"}}
			}
		]
	}`)

	_, _, extras, _, err := parseClaudeOAuthUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	fable := findExtraLimit(t, extras, "claude-fable")
	if fable.Label != "Fable" {
		t.Fatalf("fable label = %q", fable.Label)
	}
	if fable.Session != nil {
		t.Fatalf("fable session should be absent: %+v", fable.Session)
	}
	if fable.Weekly == nil || fable.Weekly.PercentRemaining != 58 || fable.Weekly.WindowMinutes != 10080 {
		t.Fatalf("fable weekly = %+v", fable.Weekly)
	}
}

func TestParseClaudeOAuthUsageSpendBucket(t *testing.T) {
	now := mustParseTime(t, "2026-08-30T12:00:00Z")
	data := []byte(`{
		"five_hour": {"utilization": 0, "resets_at": "2026-08-30T15:10:00Z"},
		"seven_day": {"utilization": 0, "resets_at": "2026-09-04T11:00:00Z"},
		"extra_usage": {
			"is_enabled": true,
			"monthly_limit": 10000,
			"used_credits": 9999,
			"currency": "USD",
			"decimal_places": 2
		},
		"spend": {
			"used": {"amount_minor": 4092, "currency": "USD", "exponent": 2},
			"limit": {"amount_minor": 10000, "currency": "USD", "exponent": 2},
			"percent": 41,
			"enabled": true
		}
	}`)

	_, _, extras, additional, err := parseClaudeOAuthUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	buckets := makeQuotaBuckets(Allowance{}, Allowance{}, extras, additional)
	if len(buckets) != 1 {
		t.Fatalf("quota buckets = %+v", buckets)
	}
	spend := buckets[0]
	if spend.ID != "claude-extra-usage" || spend.Kind != "credits" {
		t.Fatalf("spend metadata = %+v", spend)
	}
	if spend.Allowance.PercentUsed != 40.92 || spend.Allowance.PercentRemaining != 59.08 {
		t.Fatalf("spend allowance = %+v", spend.Allowance)
	}
	if spend.ValueLabel != "$40.92 / $100.00" || spend.Detail != "$59.08 remaining" {
		t.Fatalf("spend labels = %+v", spend)
	}
}

func TestParseClaudeOAuthUsageExtraUsageFallback(t *testing.T) {
	root := map[string]any{
		"extra_usage": map[string]any{
			"is_enabled":     true,
			"monthly_limit":  float64(5000),
			"used_credits":   float64(1250),
			"currency":       "USD",
			"decimal_places": float64(2),
		},
	}
	buckets := parseClaudeSpendBuckets(root)
	if len(buckets) != 1 || buckets[0].ValueLabel != "$12.50 / $50.00" {
		t.Fatalf("fallback buckets = %+v", buckets)
	}
}

func TestParseClaudeOAuthUsageWithoutWindows(t *testing.T) {
	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	if _, _, _, _, err := parseClaudeOAuthUsage([]byte(`{"seven_day_oauth_apps": null}`), now); err == nil {
		t.Fatal("expected error for payload without usage windows")
	}
}

func TestCollectClaudeSubscriptionLimitsUsesOAuthCache(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)

	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	cache := claudeOAuthUsageCache{
		FetchedAt: now.Add(-time.Minute).Format(time.RFC3339),
		Body:      []byte(`{"five_hour":{"utilization":10,"resets_at":"2026-07-02T15:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2026-07-06T00:00:00Z"}}`),
	}
	saveClaudeOAuthUsageCache(claudeOAuthUsageCachePath(), cache)

	session, weekly, _, _, meta, err := collectClaudeSubscriptionLimits(now)
	if err != nil {
		t.Fatal(err)
	}
	if session.Source != claudeOAuthUsageSource || session.PercentRemaining != 90 {
		t.Fatalf("session = %+v", session)
	}
	if weekly.PercentRemaining != 80 {
		t.Fatalf("weekly = %+v", weekly)
	}
	if meta["source"] != claudeOAuthUsageSource {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestCollectClaudeOAuthLimitsBacksOffWithoutCredentials(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)

	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	if _, _, _, _, _, err := collectClaudeOAuthLimits(now); err == nil {
		t.Fatal("expected error without credentials")
	}
	saved := loadClaudeOAuthUsageCache(claudeOAuthUsageCachePath())
	if saved.NextAttemptAt == "" || saved.LastError == "" {
		t.Fatalf("expected backoff marker, got %+v", saved)
	}
	if _, _, _, _, _, err := collectClaudeOAuthLimits(now.Add(30 * time.Second)); err == nil ||
		!strings.Contains(err.Error(), "backing off") {
		t.Fatalf("expected backoff error, got %v", err)
	}
}

func TestCollectClaudePreservesStatuslineErrorMetadata(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	if err := os.Mkdir(filepath.Join(claudeDir, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"statusLine":{"type":"command","command":"dankaiusage claude-statusline","padding":0}}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}

	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	provider := collectClaude(now, options{PeriodDays: 7, SessionHours: 5})
	if provider.Meta["statuslineConfigured"] != true {
		t.Fatalf("statuslineConfigured metadata missing: %+v", provider.Meta)
	}
	if provider.Meta["statuslineCommand"] != "dankaiusage claude-statusline" {
		t.Fatalf("statuslineCommand metadata = %v", provider.Meta["statuslineCommand"])
	}
	if provider.Meta["tokenDataScope"] != "Claude Code local history only" {
		t.Fatalf("tokenDataScope metadata = %v", provider.Meta["tokenDataScope"])
	}
	if provider.Meta["tokenDataIncludesWeb"] != false {
		t.Fatalf("tokenDataIncludesWeb metadata = %v", provider.Meta["tokenDataIncludesWeb"])
	}
	if provider.Meta["statuslineNextStep"] == "" {
		t.Fatalf("statuslineNextStep metadata missing: %+v", provider.Meta)
	}
	if !strings.Contains(stringValue(provider.Meta["limitError"]), "configured but has not run yet") {
		t.Fatalf("limitError metadata = %v", provider.Meta["limitError"])
	}
	wantCache := filepath.Join(stateDir, "dankaiusage", "claude-statusline.json")
	if provider.Meta["statuslineCache"] != wantCache {
		t.Fatalf("statuslineCache metadata = %v, want %s", provider.Meta["statuslineCache"], wantCache)
	}
}

func TestClaudePrimeArgs(t *testing.T) {
	args := claudePrimeArgs(claudePrimeOptions{
		Model: "sonnet",
	}, "Reply OK")

	want := []string{
		firstNonEmpty(commandPath("claude"), "claude"),
		"-p",
		"--safe-mode",
		"--no-session-persistence",
		"--tools", "",
		"--permission-mode", "dontAsk",
		"--system-prompt", "Reply with exactly OK.",
		"--output-format", "json",
		"--prompt-suggestions", "false",
		"--max-turns", "1",
		"--max-budget-usd", "0.001",
		"--model", "sonnet",
		"Reply OK",
	}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestClaudePrimeSessionFallback(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	now := mustParseTime(t, "2026-06-24T12:00:00Z")
	cache := claudePrimeCache{
		StartedAt: "2026-06-24T11:00:00Z",
		UsageAt:   "2026-06-24T11:00:10Z",
		ResetAt:   "2026-06-24T16:00:10Z",
		Source:    "claude-prime local usage",
	}
	if err := writeClaudePrimeCache(cache); err != nil {
		t.Fatal(err)
	}

	allowance, meta, ok := claudePrimeSessionFallback(now)
	if !ok {
		t.Fatal("expected active prime fallback")
	}
	if allowance.Source != "claude-prime local usage" || allowance.ResetAt == "" {
		t.Fatalf("allowance = %+v", allowance)
	}
	if meta["primeUsageAt"] != cache.UsageAt {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestCollectClaudeUsesPrimeFallbackOnStatuslineError(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	if err := os.Mkdir(filepath.Join(claudeDir, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"statusLine":{"type":"command","command":"dankaiusage claude-statusline","padding":0}}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	statuslinePath := filepath.Join(stateDir, "dankaiusage", "claude-statusline.json")
	if err := os.MkdirAll(filepath.Dir(statuslinePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statuslinePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := claudePrimeCache{
		StartedAt: "2026-06-24T11:00:00Z",
		UsageAt:   "2026-06-24T11:00:10Z",
		ResetAt:   "2026-06-24T16:00:10Z",
		Source:    claudePrimeSessionSource,
	}
	if err := writeClaudePrimeCache(cache); err != nil {
		t.Fatal(err)
	}

	now := mustParseTime(t, "2026-06-24T12:00:00Z")
	provider := collectClaude(now, options{PeriodDays: 7, SessionHours: 5})
	if provider.SessionLeft.Source != claudePrimeSessionSource {
		t.Fatalf("session source = %q, want %q", provider.SessionLeft.Source, claudePrimeSessionSource)
	}
	gotReset, err := time.Parse(time.RFC3339, provider.SessionLeft.ResetAt)
	if err != nil {
		t.Fatal(err)
	}
	if !gotReset.Equal(mustParseTime(t, "2026-06-24T16:00:10Z")) {
		t.Fatalf("session reset = %s", provider.SessionLeft.ResetAt)
	}
	if provider.Meta["sessionFallbackSource"] != claudePrimeSessionSource {
		t.Fatalf("fallback metadata = %+v", provider.Meta)
	}
	if !strings.Contains(stringValue(provider.Meta["limitError"]), "no rate limit data") {
		t.Fatalf("limitError metadata = %v", provider.Meta["limitError"])
	}
}

func TestClaudePrimeSkipsWhenOAuthSessionActive(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("PATH", t.TempDir())
	settings := []byte(`{"statusLine":{"type":"command","command":"dankaiusage claude-statusline","padding":0}}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	resetAt := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	body := []byte(`{"five_hour":{"utilization":40,"resets_at":"` + resetAt + `"},"seven_day":{"utilization":10,"resets_at":"` + resetAt + `"}}`)
	saveClaudeOAuthUsageCache(claudeOAuthUsageCachePath(), claudeOAuthUsageCache{
		FetchedAt: time.Now().Add(-time.Hour).Format(time.RFC3339),
		Body:      body,
	})

	result, err := primeClaudeStatusline(claudePrimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Message != "Claude session already active" {
		t.Fatalf("result = %+v", result)
	}
	if result.SessionLeft.Source != claudeOAuthUsageSource || result.SessionLeft.PercentRemaining != 60 {
		t.Fatalf("session = %+v", result.SessionLeft)
	}
}

func TestClaudePrimeHonorsRecentPrimeFloor(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("PATH", t.TempDir())
	settings := []byte(`{"statusLine":{"type":"command","command":"dankaiusage claude-statusline","padding":0}}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cache := claudePrimeCache{
		StartedAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
		UsageAt:   now.Add(-5 * time.Minute).Format(time.RFC3339),
		ResetAt:   now.Add(5 * time.Hour).Format(time.RFC3339),
		Source:    claudePrimeSessionSource,
	}
	if err := writeClaudePrimeCache(cache); err != nil {
		t.Fatal(err)
	}

	result, err := primeClaudeStatusline(claudePrimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Message != "Claude prime ran recently; waiting for account usage data" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAllowanceActive(t *testing.T) {
	now := mustParseTime(t, "2026-07-03T12:00:00Z")
	active := makeSubscriptionAllowance("session", claudeOAuthUsageSource, 50, now.Add(time.Hour), 300)
	if !allowanceActive(active, now) {
		t.Fatal("future reset should be active")
	}
	expired := makeSubscriptionAllowance("session", claudeOAuthUsageSource, 50, now.Add(-time.Minute), 300)
	if allowanceActive(expired, now) {
		t.Fatal("past reset should be inactive")
	}
	if allowanceActive(makeUnknownAllowance("session", now.Add(time.Hour)), now) {
		t.Fatal("unknown allowance should be inactive")
	}
}

func TestClaudePrimeSkipsWhenSessionFallbackActive(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("PATH", t.TempDir())
	settings := []byte(`{"statusLine":{"type":"command","command":"dankaiusage claude-statusline","padding":0}}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	cache := claudePrimeCache{
		StartedAt: "2026-06-24T11:00:00Z",
		UsageAt:   "2026-06-24T11:00:10Z",
		ResetAt:   time.Now().Add(time.Hour).Format(time.RFC3339),
		Source:    claudePrimeSessionSource,
	}
	if err := writeClaudePrimeCache(cache); err != nil {
		t.Fatal(err)
	}

	result, err := primeClaudeStatusline(claudePrimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Message != "Claude session timer already active" {
		t.Fatalf("result = %+v", result)
	}
	if result.SessionLeft.Source != claudePrimeSessionSource {
		t.Fatalf("session source = %q", result.SessionLeft.Source)
	}
}

func TestApplyEventsPreservesPrimeFallback(t *testing.T) {
	now := mustParseTime(t, "2026-06-24T12:00:00Z")
	resetAt := now.Add(3 * time.Hour)
	provider := ProviderUsage{
		SessionLeft: makeUnknownAllowance("session", resetAt),
	}
	provider.SessionLeft.Source = "claude-prime local usage"

	applyEvents(&provider, nil, now, options{PeriodDays: 7, SessionHours: 5})
	if provider.SessionLeft.ResetAt != resetAt.Format(time.RFC3339) {
		t.Fatalf("session reset = %s, want %s", provider.SessionLeft.ResetAt, resetAt.Format(time.RFC3339))
	}
	if provider.SessionLeft.Source != "claude-prime local usage" {
		t.Fatalf("session source = %s", provider.SessionLeft.Source)
	}
}

func findExtraLimit(t *testing.T, extras []ExtraLimit, id string) ExtraLimit {
	t.Helper()
	for _, extra := range extras {
		if extra.ID == id {
			return extra
		}
	}
	t.Fatalf("extra limit %q not found in %+v", id, extras)
	return ExtraLimit{}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
