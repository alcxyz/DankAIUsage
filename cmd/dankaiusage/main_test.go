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
		Primary: codexRateLimitWindow{
			UsedPercent:        12.5,
			WindowDurationMins: &sessionMins,
			ResetsAt:           &sessionReset,
		},
		Secondary: codexRateLimitWindow{
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

	session, weekly, err := parseClaudeOAuthUsage(data, now)
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

func TestParseClaudeOAuthUsageWithoutWindows(t *testing.T) {
	now := mustParseTime(t, "2026-07-02T12:00:00Z")
	if _, _, err := parseClaudeOAuthUsage([]byte(`{"seven_day_oauth_apps": null}`), now); err == nil {
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

	session, weekly, meta, err := collectClaudeSubscriptionLimits(now)
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
	if _, _, _, err := collectClaudeOAuthLimits(now); err == nil {
		t.Fatal("expected error without credentials")
	}
	saved := loadClaudeOAuthUsageCache(claudeOAuthUsageCachePath())
	if saved.NextAttemptAt == "" || saved.LastError == "" {
		t.Fatalf("expected backoff marker, got %+v", saved)
	}
	if _, _, _, err := collectClaudeOAuthLimits(now.Add(30 * time.Second)); err == nil ||
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

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
