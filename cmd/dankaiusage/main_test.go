package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestResolveBuildVersion(t *testing.T) {
	revision := "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
	clean := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.modified", Value: "false"},
	}}
	dirtyOutOfOrder := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.revision", Value: revision},
	}}
	for _, test := range []struct {
		name       string
		configured string
		info       *debug.BuildInfo
		want       string
	}{
		{name: "clean checkout", configured: "dev", info: clean, want: "dev-abcdef012345"},
		{name: "dirty checkout", configured: "dev", info: dirtyOutOfOrder, want: "dev-abcdef012345-dirty"},
		{name: "no metadata", configured: "dev", want: "dev"},
		{name: "short revision", configured: "dev", info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef0"}}}, want: "dev"},
		{name: "release override", configured: "1.2.3", info: dirtyOutOfOrder, want: "1.2.3"},
		{name: "packaged dev override", configured: "1.2.3-dev.abcdef0.dirty", info: clean, want: "1.2.3-dev.abcdef0.dirty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveBuildVersion(test.configured, test.info); got != test.want {
				t.Fatalf("resolveBuildVersion(%q) = %q, want %q", test.configured, got, test.want)
			}
		})
	}
}

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
	allowance := makeAllowance("weekly", 75, 100, resetAt)

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

func TestCodexSnapshotWeeklyAllowanceIgnoresNonWeekWindows(t *testing.T) {
	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	shortReset := int64(1779994842)
	weeklyReset := int64(1780528901)
	shortMins := int64(300)
	weeklyMins := int64(10080)
	snapshot := codexRateLimitSnapshot{
		LimitID:  "codex",
		PlanType: "pro",
		Primary: &codexRateLimitWindow{
			UsedPercent:        12.5,
			WindowDurationMins: &shortMins,
			ResetsAt:           &shortReset,
		},
		Secondary: &codexRateLimitWindow{
			UsedPercent:        44,
			WindowDurationMins: &weeklyMins,
			ResetsAt:           &weeklyReset,
		},
	}

	weekly := codexSnapshotWeeklyAllowance(snapshot, now)
	if !weekly.Known || weekly.Unit != "percent" || weekly.PercentRemaining != 56 || weekly.WindowMinutes != 10080 {
		t.Fatalf("weekly allowance = %+v", weekly)
	}

	// A lone non-week window is not presented as the weekly quota.
	snapshot.Secondary = nil
	if weekly := codexSnapshotWeeklyAllowance(snapshot, now); weekly.Known || weekly.ResetAt != "" {
		t.Fatalf("five-hour window became weekly: %+v", weekly)
	}
	monthMins := int64(30 * 24 * 60)
	snapshot.Primary.WindowDurationMins = &monthMins
	if weekly := codexSnapshotWeeklyAllowance(snapshot, now); weekly.Known {
		t.Fatalf("thirty-day window became weekly: %+v", weekly)
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

	weekly := codexSnapshotWeeklyAllowance(snapshot, now)
	if !weekly.Known || weekly.PercentRemaining != 99 || weekly.WindowMinutes != 10080 {
		t.Fatalf("weekly allowance = %+v", weekly)
	}
	buckets := makeQuotaBuckets(weekly, nil, nil)
	if len(buckets) != 1 || buckets[0].ID != "general-weekly" {
		t.Fatalf("quota buckets = %+v", buckets)
	}
}

func TestCodexSnapshotWindowWithoutDurationIsWeekly(t *testing.T) {
	now := mustParseTime(t, "2026-08-30T12:00:00Z")
	weeklyReset := int64(1788648672)
	zero := int64(0)
	for _, minutes := range []*int64{nil, &zero} {
		snapshot := codexRateLimitSnapshot{
			LimitID: "codex",
			Primary: &codexRateLimitWindow{UsedPercent: 30, WindowDurationMins: minutes, ResetsAt: &weeklyReset},
		}
		weekly := codexSnapshotWeeklyAllowance(snapshot, now)
		if !weekly.Known || weekly.Window != "weekly" || weekly.PercentRemaining != 70 {
			t.Fatalf("window without duration = %+v", weekly)
		}
	}
}

func TestCodexSnapshotExtraLimits(t *testing.T) {
	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	shortReset := int64(1779994842)
	weeklyReset := int64(1780528901)
	shortMins := int64(300)
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
				WindowDurationMins: &shortMins,
				ResetsAt:           &shortReset,
			},
			Secondary: &codexRateLimitWindow{
				UsedPercent:        80,
				WindowDurationMins: &weeklyMins,
				ResetsAt:           &weeklyReset,
			},
		},
		"short-only": {
			LimitID: "short-only",
			Primary: &codexRateLimitWindow{
				UsedPercent:        50,
				WindowDurationMins: &shortMins,
				ResetsAt:           &shortReset,
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
	weekly := makeSubscriptionAllowance("weekly", "test", 20, now.Add(7*24*time.Hour), 10080)
	scopedWeekly := makeSubscriptionAllowance("weekly", "test", 30, now.Add(7*24*time.Hour), 10080)
	additional := QuotaBucket{
		ID:        "credits",
		Label:     "Extra usage credits",
		Kind:      "credits",
		Allowance: makeAllowance("monthly", 25, 100, time.Time{}),
	}

	buckets := makeQuotaBuckets(weekly, []ExtraLimit{{
		ID:     "preview",
		Label:  "Preview",
		Weekly: &scopedWeekly,
	}}, []QuotaBucket{additional})

	if len(buckets) != 3 {
		t.Fatalf("quota buckets = %+v", buckets)
	}
	if buckets[0].Label != "Weekly" || buckets[1].Label != "Preview · weekly" || buckets[2].Kind != "credits" {
		t.Fatalf("quota bucket order = %+v", buckets)
	}
}

func TestParseClaudeOAuthUsage(t *testing.T) {
	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	data := []byte(`{
		"five_hour": null,
		"seven_day": {"utilization": 35.0, "resets_at": "2026-07-06T03:59:59.943679+00:00"},
		"seven_day_opus": {"utilization": 0.0, "resets_at": null},
		"seven_day_oauth_apps": null
	}`)

	weekly, _, _, err := parseClaudeOAuthUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	if !weekly.Known || weekly.PercentRemaining != 65 || weekly.WindowMinutes != 10080 {
		t.Fatalf("weekly = %+v", weekly)
	}
	if weekly.Source != claudeOAuthUsageSource {
		t.Fatalf("weekly metadata = %+v", weekly)
	}
	reset, err := time.Parse(time.RFC3339, weekly.ResetAt)
	if err != nil {
		t.Fatal(err)
	}
	if !reset.Equal(mustParseTime(t, "2026-07-06T03:59:59Z")) {
		t.Fatalf("weekly reset = %s", weekly.ResetAt)
	}
	buckets := makeQuotaBuckets(weekly, nil, nil)
	if len(buckets) != 1 || buckets[0].ID != "general-weekly" {
		t.Fatalf("quota buckets = %+v", buckets)
	}
}

func TestParseClaudeOAuthUsageIgnoresFiveHourWindows(t *testing.T) {
	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	data := []byte(`{
		"five_hour": {"utilization": 6.0, "resets_at": "2026-07-02T15:59:59Z"},
		"five_hour_opus": {"utilization": 9.0, "resets_at": "2026-07-02T15:59:59Z"},
		"seven_day": {"utilization": 35.0, "resets_at": "2026-07-06T03:59:59Z"},
		"limits": [
			{"kind": "session", "utilization": 6.0, "resets_at": "2026-07-02T15:59:59Z"},
			{"kind": "five_hour_sonnet", "utilization": 7.0, "resets_at": "2026-07-02T15:59:59Z"}
		]
	}`)

	weekly, extras, _, err := parseClaudeOAuthUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	if weekly.PercentRemaining != 65 || len(extras) != 0 {
		t.Fatalf("weekly = %+v, extras = %+v", weekly, extras)
	}
	if _, _, _, err := parseClaudeOAuthUsage([]byte(`{"five_hour": {"utilization": 6.0}}`), now); err == nil {
		t.Fatal("a five-hour window alone was accepted")
	}
}

func TestParseClaudeOAuthUsageScopedWeeklyLimit(t *testing.T) {
	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	data := []byte(`{
		"five_hour": null,
		"seven_day": {"utilization": 35.0, "resets_at": "2026-07-06T03:59:59Z"},
		"limits": [
			{"kind": "weekly_all", "utilization": 35.0, "resets_at": "2026-07-06T03:59:59Z"},
			{
				"kind": "weekly_scoped",
				"utilization": 42.0,
				"resets_at": "2026-07-07T00:00:00Z",
				"scope": {"model": {"id": "claude-fable", "display_name": "Fable"}}
			}
		]
	}`)

	_, extras, _, err := parseClaudeOAuthUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(extras) != 1 {
		t.Fatalf("extras = %+v", extras)
	}
	fable := findExtraLimit(t, extras, "claude-fable")
	if fable.Label != "Fable" {
		t.Fatalf("fable label = %q", fable.Label)
	}
	if fable.Weekly == nil || fable.Weekly.PercentRemaining != 58 || fable.Weekly.WindowMinutes != 10080 {
		t.Fatalf("fable weekly = %+v", fable.Weekly)
	}
}

func TestParseClaudeOAuthUsageSpendBucket(t *testing.T) {
	now := mustParseTime(t, "2026-08-30T12:00:00Z")
	data := []byte(`{
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

	_, extras, additional, err := parseClaudeOAuthUsage(data, now)
	if err != nil {
		t.Fatal(err)
	}
	buckets := makeQuotaBuckets(Allowance{}, extras, additional)
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
	if _, _, _, err := parseClaudeOAuthUsage([]byte(`{"seven_day_oauth_apps": null}`), now); err == nil {
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
		Body:      []byte(`{"five_hour":null,"seven_day":{"utilization":20,"resets_at":"2026-07-06T00:00:00Z"}}`),
	}
	saveClaudeOAuthUsageCache(claudeOAuthUsageCachePath(), cache)

	weekly, _, _, meta, _, err := collectClaudeSubscriptionLimits(now, usageRefreshDefaultInterval)
	if err != nil {
		t.Fatal(err)
	}
	if weekly.Source != claudeOAuthUsageSource || weekly.PercentRemaining != 80 {
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
	if _, _, _, _, err := collectClaudeOAuthLimits(now); err == nil {
		t.Fatal("expected error without credentials")
	}
	saved := loadClaudeOAuthUsageCache(claudeOAuthUsageCachePath())
	if saved.NextAttemptAt == "" || saved.LastError == "" {
		t.Fatalf("expected backoff marker, got %+v", saved)
	}
	if _, _, _, _, err := collectClaudeOAuthLimits(now.Add(30 * time.Second)); !errors.Is(err, errUsageRefreshCoolingDown) {
		t.Fatalf("expected durable refresh cooldown, got %v", err)
	}
}

func TestCollectClaudeReportsOAuthErrorWithoutFallback(t *testing.T) {
	claudeDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	if err := os.Mkdir(filepath.Join(claudeDir, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A statusline cache and prime timer left by an earlier version are
	// ignored and kept.
	legacy := map[string]string{
		"claude-statusline.json": `{"rate_limits":{"five_hour":{"used_percentage":20},"seven_day":{"used_percentage":75}}}`,
		"claude-prime.json":      `{"startedAt":"2026-05-28T11:00:00Z","resetAt":"2026-05-28T16:00:00Z","source":"claude-prime local usage"}`,
	}
	for name, body := range legacy {
		path := filepath.Join(stateDir, "dankaiusage", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	now := mustParseTime(t, "2026-05-28T12:00:00Z")
	provider := collectClaude(now, options{PeriodDays: 7})
	if provider.WeeklyLeft.Known || len(provider.QuotaBuckets) != 0 {
		t.Fatalf("limits without account data = %+v / %+v", provider.WeeklyLeft, provider.QuotaBuckets)
	}
	if !strings.Contains(stringValue(provider.Meta["limitError"]), "credentials") {
		t.Fatalf("limitError metadata = %v", provider.Meta["limitError"])
	}
	if provider.Meta["limitDataSource"] != "Anthropic usage API" {
		t.Fatalf("limitDataSource metadata = %v", provider.Meta["limitDataSource"])
	}
	if provider.Meta["tokenDataScope"] != "Claude Code local history only" {
		t.Fatalf("tokenDataScope metadata = %v", provider.Meta["tokenDataScope"])
	}
	if provider.Meta["tokenDataIncludesWeb"] != false {
		t.Fatalf("tokenDataIncludesWeb metadata = %v", provider.Meta["tokenDataIncludesWeb"])
	}
	for key := range provider.Meta {
		if strings.Contains(key, "statusline") || strings.Contains(key, "prime") || strings.Contains(key, "sessionFallback") {
			t.Fatalf("legacy metadata %q = %v", key, provider.Meta[key])
		}
	}
	for name, body := range legacy {
		data, err := os.ReadFile(filepath.Join(stateDir, "dankaiusage", name))
		if err != nil || string(data) != body {
			t.Fatalf("legacy state %s changed: %q, %v", name, data, err)
		}
	}
}

func TestApplyEventsLeavesProviderWindowsAlone(t *testing.T) {
	now := mustParseTime(t, "2026-06-24T12:00:00Z")
	provider := ProviderUsage{ID: "codex"}
	events := []tokenEvent{{Provider: "codex", Timestamp: now.Add(-time.Hour), Session: "a", Input: 5}}
	applyEvents(&provider, events, now, options{PeriodDays: 7})
	if provider.WeeklyLeft.Known || provider.WeeklyLeft.Window != "weekly" {
		t.Fatalf("weekly placeholder = %+v", provider.WeeklyLeft)
	}
	data, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"sessionLeft"`, `"session":`, `"fiveHours"`} {
		if strings.Contains(string(data), field) {
			t.Fatalf("summary still carries %s: %s", field, data)
		}
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

func TestCodexCreditsBucket(t *testing.T) {
	var limits codexRateLimitsResult
	if err := json.Unmarshal([]byte(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":21,"windowDurationMins":10080,"resetsAt":1790775838},"planType":"pro","credits":{"hasCredits":true,"unlimited":false,"balance":"12.5"}}}`), &limits); err != nil {
		t.Fatal(err)
	}
	bucket, ok := codexCreditsBucket(limits.RateLimits)
	if !ok || bucket.ID != "codex-credits" || bucket.Kind != "credits" || bucket.Allowance.Known {
		t.Fatalf("credits bucket = %+v ok=%v", bucket, ok)
	}
	if bucket.ValueLabel != "12.50" || bucket.Detail != "12.50 prepaid credits" || bucket.Allowance.Unit != "credits" ||
		bucket.Balance == nil || *bucket.Balance != 12.5 {
		t.Fatalf("credits labels = %+v", bucket)
	}

	// Cached snapshots keep the credits object across round trips.
	encoded, err := json.Marshal(limits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"credits":{"hasCredits":true,"unlimited":false,"balance":"12.5"}`) {
		t.Fatalf("credits not retained: %s", encoded)
	}

	numeric := codexRateLimitSnapshot{Credits: &codexCreditsSnapshot{HasCredits: true, Balance: json.RawMessage(`3`)}}
	if bucket, ok := codexCreditsBucket(numeric); !ok || bucket.ValueLabel != "3.00" {
		t.Fatalf("numeric balance = %+v ok=%v", bucket, ok)
	}
	unlimited := codexRateLimitSnapshot{Credits: &codexCreditsSnapshot{Unlimited: true}}
	if bucket, ok := codexCreditsBucket(unlimited); !ok || bucket.ValueLabel != "Unlimited" {
		t.Fatalf("unlimited = %+v ok=%v", bucket, ok)
	}
	for _, snapshot := range []codexRateLimitSnapshot{
		{},
		{Credits: &codexCreditsSnapshot{}},
		{Credits: &codexCreditsSnapshot{HasCredits: false, Balance: json.RawMessage(`"0"`)}},
	} {
		if _, ok := codexCreditsBucket(snapshot); ok {
			t.Fatalf("unexpected credits bucket for %+v", snapshot)
		}
	}
	malformed := codexRateLimitSnapshot{Credits: &codexCreditsSnapshot{HasCredits: true, Balance: json.RawMessage(`"lots"`)}}
	if bucket, ok := codexCreditsBucket(malformed); !ok || bucket.ValueLabel != "Available" {
		t.Fatalf("malformed balance = %+v ok=%v", bucket, ok)
	}
}

func TestParseClaudeSpendPrepaidBalance(t *testing.T) {
	balanceOnly := map[string]any{"spend": map[string]any{
		"enabled": true,
		"used":    map[string]any{"amount_minor": float64(0), "currency": "USD", "exponent": float64(2)},
		"limit":   nil,
		"balance": map[string]any{"amount_minor": float64(2500), "currency": "USD", "exponent": float64(2)},
	}}
	buckets := parseClaudeSpendBuckets(balanceOnly)
	if len(buckets) != 1 || buckets[0].Allowance.Known || buckets[0].ValueLabel != "$25.00" || buckets[0].Detail != "$25.00 prepaid balance" {
		t.Fatalf("balance-only buckets = %+v", buckets)
	}

	withLimit := map[string]any{"spend": map[string]any{
		"enabled": true,
		"used":    map[string]any{"amount_minor": float64(4092), "currency": "USD", "exponent": float64(2)},
		"limit":   map[string]any{"amount_minor": float64(10000), "currency": "USD", "exponent": float64(2)},
		"balance": map[string]any{"amount_minor": float64(2500), "currency": "USD", "exponent": float64(2)},
	}}
	buckets = parseClaudeSpendBuckets(withLimit)
	if len(buckets) != 1 || !buckets[0].Allowance.Known || buckets[0].Detail != "$59.08 remaining · $25.00 prepaid balance" {
		t.Fatalf("limit-and-balance buckets = %+v", buckets)
	}

	disabled := map[string]any{"spend": map[string]any{
		"enabled": false,
		"balance": map[string]any{"amount_minor": float64(2500), "currency": "USD", "exponent": float64(2)},
	}}
	if buckets := parseClaudeSpendBuckets(disabled); len(buckets) != 0 {
		t.Fatalf("disabled spend produced %+v", buckets)
	}
}
