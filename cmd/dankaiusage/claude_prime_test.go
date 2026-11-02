package main

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	primeUsageNoWindow = `{"five_hour":null,"seven_day":{"utilization":25,"resets_at":"2026-11-05T08:00:00Z"},
		"limits":[{"kind":"weekly_all","percent":25},{"kind":"weekly_scoped","label":"Fable only","percent":40}]}`
	primeUsageIdleWindow    = `{"five_hour":{"utilization":0,"resets_at":null},"seven_day":{"utilization":25,"resets_at":"2026-11-05T08:00:00Z"}}`
	primeUsageEndedWindow   = `{"five_hour":{"utilization":60,"resets_at":"2026-11-02T09:00:00Z"},"seven_day":{"utilization":25,"resets_at":"2026-11-05T08:00:00Z"}}`
	primeUsageRunningWindow = `{"five_hour":{"utilization":10,"resets_at":"2026-11-02T13:00:00Z"},"seven_day":{"utilization":25,"resets_at":"2026-11-05T08:00:00Z"}}`
)

func primeUsage(body string, fetchedAt time.Time) claudeOAuthUsageCache {
	return claudeOAuthUsageCache{
		FetchedAt:      fetchedAt.UTC().Format(time.RFC3339Nano),
		NextAttemptAt:  fetchedAt.Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
		Body:           json.RawMessage(body),
		OrganizationID: "org-prime",
	}
}

func TestDecideClaudePrime(t *testing.T) {
	now := mustParseTime(t, "2026-11-02T10:00:00Z")
	fresh := now.Add(-time.Minute)
	base := func(body string) claudePrimeInputs {
		return claudePrimeInputs{Now: now, Usage: primeUsage(body, fresh), Organization: "org-prime", RefreshInterval: 5 * time.Minute}
	}
	tests := []struct {
		name   string
		edit   func(*claudePrimeInputs)
		body   string
		fire   bool
		reason string
	}{
		{name: "idle five-hour window", body: primeUsageIdleWindow, fire: true, reason: claudePrimeReasonPrimed},
		{name: "ended five-hour window", body: primeUsageEndedWindow, fire: true, reason: claudePrimeReasonPrimed},
		{name: "no five-hour window", body: primeUsageNoWindow, reason: claudePrimeReasonNoWindow},
		{name: "five_hour key absent", body: `{"seven_day":{"utilization":25}}`, reason: claudePrimeReasonNoWindow},
		{name: "running window", body: primeUsageRunningWindow, reason: claudePrimeReasonRunning},
		{name: "no usage response", body: primeUsageIdleWindow, reason: claudePrimeReasonUsageUnknown,
			edit: func(in *claudePrimeInputs) { in.Usage = claudeOAuthUsageCache{} }},
		{name: "unreadable usage cache", body: primeUsageIdleWindow, reason: claudePrimeReasonUsageUnknown,
			edit: func(in *claudePrimeInputs) { in.UsageErr = errUsageRefreshState }},
		{name: "invalidated usage", body: primeUsageIdleWindow, reason: claudePrimeReasonUsageUnknown,
			edit: func(in *claudePrimeInputs) { in.Usage.Invalidated = true }},
		{name: "usage from the future", body: primeUsageIdleWindow, reason: claudePrimeReasonUsageUnknown,
			edit: func(in *claudePrimeInputs) { in.Usage.FetchedAt = now.Add(time.Hour).Format(time.RFC3339) }},
		{name: "unsupported usage body", body: `{"unexpected":true}`, reason: claudePrimeReasonUsageUnknown},
		{name: "last usage request failed", body: primeUsageIdleWindow, reason: claudePrimeReasonUsageError,
			edit: func(in *claudePrimeInputs) { in.Usage.LastError = "Claude usage API returned HTTP 429" }},
		{name: "stale usage", body: primeUsageIdleWindow, reason: claudePrimeReasonUsageStale,
			edit: func(in *claudePrimeInputs) { in.Usage.FetchedAt = now.Add(-11 * time.Minute).Format(time.RFC3339) }},
		{name: "long refresh interval widens freshness", body: primeUsageIdleWindow, fire: true, reason: claudePrimeReasonPrimed,
			edit: func(in *claudePrimeInputs) {
				in.RefreshInterval = 30 * time.Minute
				in.Usage.FetchedAt = now.Add(-50 * time.Minute).Format(time.RFC3339)
			}},
		{name: "another sign-in", body: primeUsageIdleWindow, reason: claudePrimeReasonSignIn,
			edit: func(in *claudePrimeInputs) { in.Organization = "org-other" }},
		{name: "sign-in unknown", body: primeUsageIdleWindow, reason: claudePrimeReasonSignIn,
			edit: func(in *claudePrimeInputs) { in.Organization, in.OrganizationErr = "", errors.New("unknown") }},
		{name: "usage without a sign-in tag", body: primeUsageIdleWindow, reason: claudePrimeReasonSignIn,
			edit: func(in *claudePrimeInputs) { in.Usage.OrganizationID = "" }},
		{name: "usage older than the last prime", body: primeUsageIdleWindow, reason: claudePrimeReasonAwaitingUsage,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt = now.Add(-30 * time.Second) }},
		{name: "fresh usage but prime within the floor", body: primeUsageIdleWindow, reason: claudePrimeReasonAwaitingUsage,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt = now.Add(-10 * time.Minute) }},
		{name: "window ran since the last prime", body: primeUsageEndedWindow, fire: true, reason: claudePrimeReasonPrimed,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt = now.Add(-5*time.Hour - 30*time.Minute) }},
		{name: "window seen running since the last prime", body: primeUsageIdleWindow, fire: true, reason: claudePrimeReasonPrimed,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt, in.PrimeConfirmed = now.Add(-5*time.Hour), true }},
		{name: "confirmed prime still waits for newer usage", body: primeUsageIdleWindow, reason: claudePrimeReasonAwaitingUsage,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt, in.PrimeConfirmed = now.Add(-30*time.Second), true }},
		{name: "no window seen since the last prime", body: primeUsageIdleWindow, reason: claudePrimeReasonUnconfirmed,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt = now.Add(-time.Hour) }},
		{name: "ended window predates the last prime", body: primeUsageEndedWindow, reason: claudePrimeReasonUnconfirmed,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt = now.Add(-30 * time.Minute) }},
		{name: "unconfirmed prime retried after five hours", body: primeUsageIdleWindow, fire: true, reason: claudePrimeReasonPrimed,
			edit: func(in *claudePrimeInputs) { in.LastPrimedAt = now.Add(-5 * time.Hour) }},
		{name: "truncated reset second still running", body: primeUsageIdleWindow, reason: claudePrimeReasonRunning,
			edit: func(in *claudePrimeInputs) {
				in.Now = in.Now.Add(100 * time.Millisecond)
				in.Usage.Body = json.RawMessage(`{"five_hour":{"utilization":95,"resets_at":"2026-11-02T10:00:00.943648Z"},"seven_day":{"utilization":25}}`)
			}},
		{name: "window resetting in seconds is still running", body: primeUsageIdleWindow, reason: claudePrimeReasonRunning,
			edit: func(in *claudePrimeInputs) {
				in.Usage.Body = json.RawMessage(`{"five_hour":{"utilization":95,"resets_at":"2026-11-02T10:00:30Z"},"seven_day":{"utilization":25}}`)
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := base(test.body)
			if test.edit != nil {
				test.edit(&in)
			}
			got := decideClaudePrime(in)
			if got.Fire != test.fire || got.Reason != test.reason || got.Message == "" {
				t.Fatalf("decision = %+v, want fire=%v reason=%s", got, test.fire, test.reason)
			}
		})
	}
}

type primeTestEnv struct {
	state string
	calls int
}

// setupPrimeTest isolates every path prime reads: plugin state, the Claude
// config and home (sign-in), and PATH with a stand-in claude that is never run.
func setupPrimeTest(t *testing.T) *primeTestEnv {
	t.Helper()
	root := t.TempDir()
	env := &primeTestEnv{state: filepath.Join(root, "state")}
	claudeDir := filepath.Join(root, "claude")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{env.state, claudeDir, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", env.state)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(claudeDir, ".claude.json"), []byte(`{"oauthAccount":{"organizationUuid":"org-prime"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := runClaudePrimeRequest
	runClaudePrimeRequest = func(claudePrimeOptions) (string, error) {
		env.calls++
		return "", nil
	}
	t.Cleanup(func() { runClaudePrimeRequest = previous })
	return env
}

func writePrimeUsage(t *testing.T, cache claudeOAuthUsageCache) {
	t.Helper()
	path := claudeOAuthUsageCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveClaudeOAuthUsageCache(path, cache); err != nil {
		t.Fatal(err)
	}
}

// The 2.0.0 failure mode: on an account without a five-hour window, prime
// kept firing about every five hours through its local timer. Checked every
// five minutes for two days with fresh usage, it must never send a request,
// including with an expired timer left by that version.
func TestClaudePrimeNeverFiresWithoutFiveHourWindow(t *testing.T) {
	env := setupPrimeTest(t)
	legacy := filepath.Join(env.state, "dankaiusage", "claude-prime.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"startedAt":"2026-11-01T03:00:00Z","resetAt":"2026-11-01T08:00:00Z","source":"claude-prime local usage"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	start := mustParseTime(t, "2026-11-01T00:00:00Z")
	for step := range 2 * 24 * 12 {
		now := start.Add(time.Duration(step) * 5 * time.Minute)
		writePrimeUsage(t, primeUsage(primeUsageNoWindow, now.Add(-30*time.Second)))
		result, err := primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, func() time.Time { return now })
		if err != nil || !result.OK || result.Primed || result.Reason != claudePrimeReasonNoWindow {
			t.Fatalf("step %d: result = %+v, err = %v", step, result, err)
		}
	}
	if env.calls != 0 {
		t.Fatalf("prime sent %d requests on an account without a five-hour window", env.calls)
	}
	state, err := loadClaudePrimeState(claudePrimeStatePath())
	if err != nil || state.PrimedAt != "" || state.Reason != claudePrimeReasonNoWindow {
		t.Fatalf("state = %+v, err = %v", state, err)
	}
}

func TestClaudePrimeNeverFiresOnUnknownOrFailedUsage(t *testing.T) {
	now := mustParseTime(t, "2026-11-02T10:00:00Z")
	clock := func() time.Time { return now }
	cases := map[string]func(t *testing.T){
		"no usage cache": func(t *testing.T) {},
		"failed refresh": func(t *testing.T) {
			cache := primeUsage(primeUsageIdleWindow, now.Add(-time.Minute))
			cache.LastError = "Claude usage API returned HTTP 503"
			writePrimeUsage(t, cache)
		},
		"stale usage": func(t *testing.T) {
			writePrimeUsage(t, primeUsage(primeUsageIdleWindow, now.Add(-2*time.Hour)))
		},
		"corrupt cache": func(t *testing.T) {
			path := claudeOAuthUsageCachePath()
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"body":`), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			env := setupPrimeTest(t)
			prepare(t)
			result, err := primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, clock)
			if err != nil || result.Primed || result.Reason == claudePrimeReasonPrimed || env.calls != 0 {
				t.Fatalf("result = %+v, err = %v, calls = %d", result, err, env.calls)
			}
		})
	}
}

func TestClaudePrimeFiresOnceForAnIdleWindow(t *testing.T) {
	env := setupPrimeTest(t)
	now := mustParseTime(t, "2026-11-02T10:00:00Z")
	writePrimeUsage(t, primeUsage(primeUsageIdleWindow, now.Add(-time.Minute)))
	result, err := primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, func() time.Time { return now })
	if err != nil || !result.Primed || env.calls != 1 || result.PrimedAt == "" {
		t.Fatalf("first prime = %+v, err = %v, calls = %d", result, err, env.calls)
	}

	// The same usage response cannot prime again, however often it is asked.
	for _, later := range []time.Duration{time.Minute, 20 * time.Minute} {
		at := now.Add(later)
		if later > 10*time.Minute {
			writePrimeUsage(t, primeUsage(primeUsageIdleWindow, now.Add(-time.Minute)))
		}
		result, err = primeClaude(claudePrimeOptions{RefreshInterval: 30 * time.Minute}, func() time.Time { return at })
		if err != nil || result.Primed || result.Reason != claudePrimeReasonAwaitingUsage {
			t.Fatalf("repeat after %s = %+v, err = %v", later, result, err)
		}
	}
	// A newer response showing the running window settles it.
	at := now.Add(25 * time.Minute)
	writePrimeUsage(t, primeUsage(primeUsageRunningWindow, at.Add(-time.Minute)))
	result, err = primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, func() time.Time { return at })
	if err != nil || result.Primed || result.Reason != claudePrimeReasonRunning || result.Session == nil {
		t.Fatalf("running window = %+v, err = %v", result, err)
	}
	if env.calls != 1 {
		t.Fatalf("calls = %d, want 1", env.calls)
	}
}

func TestClaudePrimeRecordsFailedRequest(t *testing.T) {
	env := setupPrimeTest(t)
	runClaudePrimeRequest = func(claudePrimeOptions) (string, error) {
		env.calls++
		return "budget exceeded", errors.New("exit status 1")
	}
	now := mustParseTime(t, "2026-11-02T10:00:00Z")
	writePrimeUsage(t, primeUsage(primeUsageIdleWindow, now.Add(-time.Minute)))
	result, err := primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, func() time.Time { return now })
	if err == nil || result.OK || result.Primed || env.calls != 1 {
		t.Fatalf("failed prime = %+v, err = %v", result, err)
	}
	// The failed request may still have started the window: no retry until a
	// newer usage response.
	result, err = primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, func() time.Time { return now.Add(time.Minute) })
	if err != nil || result.Reason != claudePrimeReasonAwaitingUsage || env.calls != 1 {
		t.Fatalf("retry = %+v, err = %v, calls = %d", result, err, env.calls)
	}
}

func TestClaudePrimeArgs(t *testing.T) {
	args := claudePrimeArgs(claudePrimeOptions{}, "OK")
	want := map[string]string{"--max-budget-usd": "0.001", "--model": defaultClaudePrimeModel, "--tools": ""}
	for i := 0; i < len(args)-1; i++ {
		if value, ok := want[args[i]]; ok {
			if args[i+1] != value {
				t.Fatalf("%s = %q, want %q", args[i], args[i+1], value)
			}
			delete(want, args[i])
		}
	}
	if len(want) != 0 || args[len(args)-1] != "OK" {
		t.Fatalf("missing args %v in %v", want, args)
	}
}

// A primed window that ran and ended may come back as resets_at null. The
// summary saw it running after the prime, so the next idle window is primed
// at once rather than five hours after the last prime.
func TestClaudePrimeConfirmedBySummaryPrimesTheNextWindow(t *testing.T) {
	env := setupPrimeTest(t)
	noteClaudePrimeConfirmation()
	if _, err := os.Stat(claudePrimeStatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("summary created prime state without a prime: %v", err)
	}
	primed := mustParseTime(t, "2026-11-02T10:00:00Z")
	writePrimeUsage(t, primeUsage(primeUsageIdleWindow, primed.Add(-time.Minute)))
	if result, err := primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, func() time.Time { return primed }); err != nil || !result.Primed {
		t.Fatalf("prime = %+v, err = %v", result, err)
	}
	running := `{"five_hour":{"utilization":1,"resets_at":"2026-11-02T15:00:00Z"},"seven_day":{"utilization":25}}`
	// Another sign-in's running window does not confirm this prime.
	other := primeUsage(running, primed.Add(3*time.Minute))
	other.OrganizationID = "org-other"
	writePrimeUsage(t, other)
	noteClaudePrimeConfirmation()
	if state, _ := loadClaudePrimeState(claudePrimeStatePath()); state.ConfirmedAt != "" || state.PrimedOrganization != "org-prime" {
		t.Fatalf("another sign-in confirmed the prime: %+v", state)
	}
	// The next poll for the primed sign-in shows the window running; the
	// summary records it.
	writePrimeUsage(t, primeUsage(running, primed.Add(5*time.Minute)))
	noteClaudePrimeConfirmation()
	state, err := loadClaudePrimeState(claudePrimeStatePath())
	if err != nil || state.ConfirmedAt == "" {
		t.Fatalf("state = %+v, err = %v", state, err)
	}
	// After the window ends, idle with no reset time: prime again at once.
	at := primed.Add(5*time.Hour - 20*time.Minute)
	writePrimeUsage(t, primeUsage(primeUsageIdleWindow, at.Add(-time.Minute)))
	result, err := primeClaude(claudePrimeOptions{RefreshInterval: 5 * time.Minute}, func() time.Time { return at })
	if err != nil || !result.Primed || env.calls != 2 {
		t.Fatalf("next window = %+v, err = %v, calls = %d", result, err, env.calls)
	}
	state, _ = loadClaudePrimeState(claudePrimeStatePath())
	if state.ConfirmedAt != "" {
		t.Fatalf("a new prime keeps the old confirmation: %+v", state)
	}
}

func TestClaudePrimeGuards(t *testing.T) {
	zone := time.FixedZone("test", 3600)
	at := func(hour int) time.Time { return time.Date(2026, 11, 2, hour, 30, 0, 0, zone) }
	guards := claudePrimeGuards{ActiveFrom: 7, ActiveUntil: 23, MinWeeklyLeft: 10, DailyCap: 5}.normalized()
	for hour, want := range map[int]bool{6: false, 7: true, 12: true, 22: true, 23: false, 2: false} {
		if got := guards.activeAt(at(hour)); got != want {
			t.Fatalf("07-23 active at %02d:30 = %v, want %v", hour, got, want)
		}
	}
	night := claudePrimeGuards{ActiveFrom: 22, ActiveUntil: 6}.normalized()
	for hour, want := range map[int]bool{21: false, 22: true, 2: true, 6: false} {
		if got := night.activeAt(at(hour)); got != want {
			t.Fatalf("22-06 active at %02d:30 = %v, want %v", hour, got, want)
		}
	}
	if !(claudePrimeGuards{ActiveFrom: 9, ActiveUntil: 9}).normalized().activeAt(at(3)) {
		t.Fatal("equal hours mean all day")
	}
	if g := (claudePrimeGuards{ActiveFrom: 0, ActiveUntil: 24}).normalized(); !g.activeAt(at(23)) || !g.activeAt(at(0)) {
		t.Fatalf("0-24 is all day: %+v", g)
	}
	if g := (claudePrimeGuards{ActiveFrom: 40, ActiveUntil: -1, MinWeeklyLeft: 150, DailyCap: -3}).normalized(); g.ActiveFrom != 7 || g.ActiveUntil != 23 || g.MinWeeklyLeft != 10 || g.DailyCap != 5 {
		t.Fatalf("invalid guards normalize to %+v", g)
	}
	// An invalid weekly threshold keeps the default instead of turning the
	// guard off; only 0 turns it off.
	for _, value := range []float64{-5, math.NaN(), 101} {
		if g := (claudePrimeGuards{MinWeeklyLeft: value}).normalized(); g.MinWeeklyLeft != 10 {
			t.Fatalf("min weekly left %v normalizes to %v", value, g.MinWeeklyLeft)
		}
	}
	if g := (claudePrimeGuards{MinWeeklyLeft: 0, DailyCap: 99}).normalized(); g.MinWeeklyLeft != 0 || g.DailyCap != 5 {
		t.Fatalf("guards normalize to %+v", g)
	}

	now := at(12)
	base := claudePrimeInputs{Now: now, Usage: primeUsage(primeUsageIdleWindow, now.Add(-time.Minute)), Organization: "org-prime",
		RefreshInterval: 5 * time.Minute, Guards: guards}
	if got := decideClaudePrime(base); !got.Fire {
		t.Fatalf("idle window within the guards = %+v", got)
	}
	night1 := base
	night1.Now = at(2)
	night1.Usage = primeUsage(primeUsageIdleWindow, night1.Now.Add(-time.Minute))
	if got := decideClaudePrime(night1); got.Fire || got.Reason != claudePrimeReasonInactiveHours {
		t.Fatalf("outside active hours = %+v", got)
	}
	low := base
	low.Usage = primeUsage(`{"five_hour":{"utilization":0,"resets_at":null},"seven_day":{"utilization":95}}`, now.Add(-time.Minute))
	if got := decideClaudePrime(low); got.Fire || got.Reason != claudePrimeReasonWeeklyLow {
		t.Fatalf("weekly nearly used = %+v", got)
	}
	low.Guards.MinWeeklyLeft = 0
	if got := decideClaudePrime(low); !got.Fire {
		t.Fatalf("weekly guard off = %+v", got)
	}
	noWeekly := base
	noWeekly.Usage = primeUsage(`{"five_hour":{"utilization":0,"resets_at":null}}`, now.Add(-time.Minute))
	if got := decideClaudePrime(noWeekly); !got.Fire {
		t.Fatalf("unknown weekly does not block = %+v", got)
	}
	capped := base
	capped.PrimesToday = 5
	if got := decideClaudePrime(capped); got.Fire || got.Reason != claudePrimeReasonDailyCap {
		t.Fatalf("daily cap = %+v", got)
	}
	// No window still says so first, whatever the guards.
	none := night1
	none.Usage = primeUsage(primeUsageNoWindow, night1.Now.Add(-time.Minute))
	if got := decideClaudePrime(none); got.Reason != claudePrimeReasonNoWindow {
		t.Fatalf("no window outside active hours = %+v", got)
	}
	// An unconfirmed prime is reported before the guards (ADR-0028 order).
	unconfirmed := night1
	unconfirmed.LastPrimedAt = night1.Now.Add(-time.Hour)
	if got := decideClaudePrime(unconfirmed); got.Reason != claudePrimeReasonUnconfirmed {
		t.Fatalf("unconfirmed prime outside active hours = %+v", got)
	}

	today := []string{
		time.Date(2026, 11, 1, 23, 50, 0, 0, zone).UTC().Format(time.RFC3339Nano),
		time.Date(2026, 11, 2, 0, 10, 0, 0, zone).UTC().Format(time.RFC3339Nano),
		time.Date(2026, 11, 2, 9, 0, 0, 0, zone).UTC().Format(time.RFC3339Nano),
		time.Date(2026, 10, 30, 9, 0, 0, 0, zone).UTC().Format(time.RFC3339Nano),
	}
	if got := primesToday(today, now); got != 2 {
		t.Fatalf("primes today = %d, want 2 (local date)", got)
	}
	if got := recentPrimeTimes(today, now); len(got) != 3 {
		t.Fatalf("recent primes = %v", got)
	}
}

func TestClaudePrimeDailyCapAcrossRequests(t *testing.T) {
	env := setupPrimeTest(t)
	opts := claudePrimeOptions{RefreshInterval: 5 * time.Minute, Guards: claudePrimeGuards{ActiveFrom: 0, ActiveUntil: 0, DailyCap: 2}}
	start := mustParseTime(t, "2026-11-02T01:00:00Z")
	for i := range 4 {
		now := start.Add(time.Duration(i) * 6 * time.Hour)
		writePrimeUsage(t, primeUsage(primeUsageIdleWindow, now.Add(-time.Minute)))
		if _, err := primeClaude(opts, func() time.Time { return now.In(time.UTC) }); err != nil {
			t.Fatal(err)
		}
	}
	if env.calls != 2 {
		t.Fatalf("calls = %d, want the daily cap of 2", env.calls)
	}
	state, _ := loadClaudePrimeState(claudePrimeStatePath())
	if state.Reason != claudePrimeReasonDailyCap || len(state.PrimeTimes) != 2 {
		t.Fatalf("state = %+v", state)
	}
}
