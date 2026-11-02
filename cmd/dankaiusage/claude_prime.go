package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Claude prime starts Claude's five-hour window with a tiny request, so the
// window runs while the user is away (ADR-0028). It decides only from the
// latest usage response the quota poll saved: it never requests usage itself
// and never falls back to a local timer, so an account without a five-hour
// window, or with an unknown, stale or failed usage state, is never primed.

const (
	defaultClaudePrimeModel = "sonnet"
	// Floor between prime requests, on top of waiting for a usage response
	// newer than the last prime.
	claudePrimeMinInterval = 15 * time.Minute
	// A usage response is fresh enough to prime from when it is younger than
	// twice the refresh interval, and at least this long.
	claudePrimeMinFreshness = 10 * time.Minute
	// When no usage response shows that the last prime started a window,
	// prime waits this long before trying again, so a prime that cannot start
	// one costs at most one request per window length.
	claudePrimeUnconfirmedRetry = 5 * time.Hour
	// Owner-chosen defaults for the extra guards (ADR-0028): prime from 07:00
	// to 23:00 local time, not when under 10% of the weekly limit is left, and
	// at most five times a local day.
	defaultClaudePrimeActiveFrom    = 7
	defaultClaudePrimeActiveUntil   = 23
	defaultClaudePrimeMinWeeklyLeft = 10
	defaultClaudePrimeDailyCap      = 5
)

// Reasons a prime check ends with. "primed" sent the request; every other
// reason skipped it.
const (
	claudePrimeReasonPrimed        = "primed"
	claudePrimeReasonUsageUnknown  = "usage_unknown"
	claudePrimeReasonUsageError    = "usage_error"
	claudePrimeReasonUsageStale    = "usage_stale"
	claudePrimeReasonSignIn        = "sign_in_mismatch"
	claudePrimeReasonAwaitingUsage = "awaiting_usage"
	claudePrimeReasonUnconfirmed   = "prime_unconfirmed"
	claudePrimeReasonInactiveHours = "outside_active_hours"
	claudePrimeReasonWeeklyLow     = "weekly_low"
	claudePrimeReasonDailyCap      = "daily_cap"
	claudePrimeReasonCLIMissing    = "cli_missing"
	claudePrimeReasonNoWindow      = "no_five_hour_window"
	claudePrimeReasonRunning       = "window_running"
)

type claudePrimeState struct {
	// PrimedAt is when the last prime request was sent, recorded before it
	// runs so a crash or failure cannot lead to an immediate repeat.
	PrimedAt string `json:"primedAt,omitempty"`
	// PrimedOrganization is the sign-in the last prime was sent for; only a
	// usage response for it can confirm that prime.
	PrimedOrganization string `json:"primedOrganization,omitempty"`
	// ConfirmedAt is when a usage response fetched after PrimedAt first
	// showed the five-hour window running; the summary records it.
	ConfirmedAt string `json:"confirmedAt,omitempty"`
	// PrimeTimes lists the prime requests of the last two days, for the
	// daily cap.
	PrimeTimes []string `json:"primeTimes,omitempty"`
	CheckedAt  string   `json:"checkedAt,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Message    string   `json:"message,omitempty"`
}

type claudePrimeResult struct {
	OK        bool       `json:"ok"`
	Primed    bool       `json:"primed"`
	Reason    string     `json:"reason"`
	Message   string     `json:"message"`
	CheckedAt string     `json:"checkedAt"`
	PrimedAt  string     `json:"primedAt,omitempty"`
	Session   *Allowance `json:"sessionLeft,omitempty"`
}

type claudePrimeOptions struct {
	Prompt          string
	Model           string
	Timeout         time.Duration
	RefreshInterval time.Duration
	Guards          claudePrimeGuards
}

// claudePrimeGuards are the optional limits on when prime may fire. Hours
// are local; ActiveFrom == ActiveUntil means all day, and a range may wrap
// past midnight. MinWeeklyLeft 0 turns the weekly guard off.
type claudePrimeGuards struct {
	ActiveFrom    int
	ActiveUntil   int
	MinWeeklyLeft float64
	DailyCap      int
}

// normalized clamps the guards to valid values, so a bad setting can only
// make prime stricter or keep the defaults.
func (g claudePrimeGuards) normalized() claudePrimeGuards {
	if g.ActiveFrom < 0 || g.ActiveFrom > 23 {
		g.ActiveFrom = defaultClaudePrimeActiveFrom
	}
	if g.ActiveUntil < 0 || g.ActiveUntil > 24 {
		g.ActiveUntil = defaultClaudePrimeActiveUntil
	}
	if g.ActiveUntil == 24 {
		g.ActiveUntil = 0
	}
	// 0 turns the weekly guard off on purpose; anything outside 0 to 100,
	// or not a number, keeps the default rather than turning it off.
	if math.IsNaN(g.MinWeeklyLeft) || g.MinWeeklyLeft < 0 || g.MinWeeklyLeft > 100 {
		g.MinWeeklyLeft = defaultClaudePrimeMinWeeklyLeft
	}
	if g.DailyCap < 1 || g.DailyCap > 24 {
		g.DailyCap = defaultClaudePrimeDailyCap
	}
	return g
}

func (g claudePrimeGuards) activeAt(t time.Time) bool {
	hour := t.Hour()
	switch {
	case g.ActiveFrom == g.ActiveUntil:
		return true
	case g.ActiveFrom < g.ActiveUntil:
		return hour >= g.ActiveFrom && hour < g.ActiveUntil
	default:
		return hour >= g.ActiveFrom || hour < g.ActiveUntil
	}
}

// primesToday counts the recorded primes on now's local date.
func primesToday(times []string, now time.Time) int {
	y, m, d := now.Date()
	count := 0
	for _, value := range times {
		t, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			continue
		}
		ty, tm, td := t.In(now.Location()).Date()
		if ty == y && tm == m && td == d {
			count++
		}
	}
	return count
}

// recentPrimeTimes keeps the primes of the last two days.
func recentPrimeTimes(times []string, now time.Time) []string {
	var out []string
	for _, value := range times {
		if t, err := time.Parse(time.RFC3339Nano, value); err == nil && now.Sub(t) < 48*time.Hour {
			out = append(out, value)
		}
	}
	return out
}

// claudePrimeInputs is everything a prime decision depends on, so the
// decision itself is a pure function.
type claudePrimeInputs struct {
	Now             time.Time
	Usage           claudeOAuthUsageCache
	UsageErr        error
	Organization    string
	OrganizationErr error
	LastPrimedAt    time.Time
	// PrimeConfirmed is true when a usage response after the last prime
	// showed the window running.
	PrimeConfirmed  bool
	Guards          claudePrimeGuards
	PrimesToday     int
	RefreshInterval time.Duration
}

type claudePrimeDecision struct {
	Fire    bool
	Reason  string
	Message string
	Session *Allowance
}

// runClaudePrimeRequest sends the prime request; tests replace it.
var runClaudePrimeRequest = runClaudePrimeCLI

func claudePrimeStatePath() string {
	return filepath.Join(pluginStateDir(), "claude-prime-state.json")
}

func runClaudePrimeCommand(args []string) {
	fs := flag.NewFlagSet("claude-prime", flag.ExitOnError)
	prompt := fs.String("prompt", "OK", "small prompt that starts the five-hour window")
	model := fs.String("model", defaultClaudePrimeModel, "Claude model alias or full model name used for the prime request")
	timeoutSeconds := fs.Int("timeout-seconds", 120, "maximum seconds to wait for Claude Code")
	refreshInterval := fs.Int("refresh-interval", int(usageRefreshDefaultInterval/time.Second), "provider usage refresh interval in seconds")
	activeFrom := fs.Int("active-from", defaultClaudePrimeActiveFrom, "local hour (0-23) from which prime may fire")
	activeUntil := fs.Int("active-until", defaultClaudePrimeActiveUntil, "local hour (0-24) until which prime may fire; equal to active-from means all day")
	minWeeklyLeft := fs.Float64("min-weekly-left", defaultClaudePrimeMinWeeklyLeft, "skip when less than this percent of the weekly limit is left; 0 turns it off")
	dailyCap := fs.Int("daily-cap", defaultClaudePrimeDailyCap, "most prime requests per local day")
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	_ = fs.Parse(args)

	result, err := primeClaude(claudePrimeOptions{
		Prompt:          *prompt,
		Model:           *model,
		Timeout:         time.Duration(maxInt(*timeoutSeconds, 1)) * time.Second,
		RefreshInterval: usageRefreshIntervalSeconds(*refreshInterval),
		Guards: claudePrimeGuards{
			ActiveFrom: *activeFrom, ActiveUntil: *activeUntil,
			MinWeeklyLeft: *minWeeklyLeft, DailyCap: *dailyCap,
		},
	}, time.Now)
	var data []byte
	var encodeErr error
	if *pretty {
		data, encodeErr = json.MarshalIndent(result, "", "  ")
	} else {
		data, encodeErr = json.Marshal(result)
	}
	if encodeErr != nil {
		fmt.Fprintf(os.Stderr, "encode claude prime result: %v\n", encodeErr)
		os.Exit(1)
	}
	fmt.Println(string(data))
	if err != nil {
		fmt.Fprintln(os.Stderr, result.Message)
		os.Exit(1)
	}
}

// primeClaude checks the latest usage response and sends a prime request only
// when it reports a five-hour window that is not running. A skip is a normal
// outcome (OK, not Primed); a missing claude CLI (cli_missing, nothing sent),
// a failed request or unusable local state is an error.
func primeClaude(opts claudePrimeOptions, clock usageRefreshClock) (claudePrimeResult, error) {
	opts.RefreshInterval = normalizeUsageRefreshInterval(opts.RefreshInterval)
	statePath := claudePrimeStatePath()
	var result claudePrimeResult
	var decision claudePrimeDecision
	// Decide and record the prime under the lock, so two callers cannot both
	// send one; the request itself runs after the lock is released.
	err := withUsageRefreshLock(statePath, func() error {
		now := clock()
		state, err := loadClaudePrimeState(statePath)
		if err != nil {
			return err
		}
		usage, usageErr := loadClaudeOAuthUsageCacheStrict(claudeOAuthUsageCachePath())
		organization, organizationErr := claudeOrganizationID()
		lastPrimedAt, _ := time.Parse(time.RFC3339Nano, state.PrimedAt)
		confirmedAt, _ := time.Parse(time.RFC3339Nano, state.ConfirmedAt)
		decision = decideClaudePrime(claudePrimeInputs{
			Now: now, Usage: usage, UsageErr: usageErr,
			Organization: organization, OrganizationErr: organizationErr,
			LastPrimedAt: lastPrimedAt, PrimeConfirmed: !lastPrimedAt.IsZero() && confirmedAt.After(lastPrimedAt),
			RefreshInterval: opts.RefreshInterval,
			Guards:          opts.Guards.normalized(), PrimesToday: primesToday(state.PrimeTimes, now),
		})
		// A missing CLI is known before anything is sent, so it is not
		// recorded as a prime: it neither counts toward the daily cap nor
		// starts the unconfirmed wait.
		if decision.Fire {
			if _, err := exec.LookPath("claude"); err != nil {
				decision.Fire = false
				decision.Reason = claudePrimeReasonCLIMissing
				decision.Message = "Claude CLI not found on the widget's PATH; nothing was sent"
			}
		}
		state.CheckedAt = now.UTC().Format(time.RFC3339Nano)
		state.Reason = decision.Reason
		state.Message = decision.Message
		if decision.Reason == claudePrimeReasonRunning {
			recordClaudePrimeConfirmation(&state, usage)
		}
		if decision.Fire {
			state.PrimedAt = state.CheckedAt
			state.PrimedOrganization = organization
			state.ConfirmedAt = ""
			state.PrimeTimes = append(recentPrimeTimes(state.PrimeTimes, now), state.CheckedAt)
			state.Message = "Prime request sent"
		}
		result = claudePrimeResult{
			OK: true, Reason: decision.Reason, Message: decision.Message,
			CheckedAt: state.CheckedAt, PrimedAt: state.PrimedAt, Session: decision.Session,
		}
		return saveClaudePrimeState(statePath, state)
	})
	if err != nil {
		result = claudePrimeResult{Reason: "local_state", Message: "Claude prime state is unusable: " + err.Error()}
		return result, err
	}
	if decision.Reason == claudePrimeReasonCLIMissing {
		result.OK = false
		return result, errors.New(result.Message)
	}
	if !decision.Fire {
		return result, nil
	}
	stderr, runErr := runClaudePrimeRequest(opts)
	if runErr != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = runErr.Error()
		}
		result.OK = false
		result.Message = "Claude prime request failed: " + msg
		return result, errors.New(result.Message)
	}
	result.Primed = true
	result.Message = "Five-hour window started; it shows after the next usage refresh"
	return result, nil
}

// decideClaudePrime fires only when the latest usage response is a recent
// success for the signed-in account, newer than the last prime, and reports a
// five-hour window that is not running. Anything unknown skips.
func decideClaudePrime(in claudePrimeInputs) claudePrimeDecision {
	skip := func(reason, message string) claudePrimeDecision {
		return claudePrimeDecision{Reason: reason, Message: message}
	}
	fetchedAt, err := time.Parse(time.RFC3339Nano, in.Usage.FetchedAt)
	if in.UsageErr != nil || err != nil || len(in.Usage.Body) == 0 || in.Usage.Invalidated || fetchedAt.After(in.Now) {
		return skip(claudePrimeReasonUsageUnknown, "Claude usage is unknown; prime waits for a usage response")
	}
	if in.Usage.LastError != "" {
		return skip(claudePrimeReasonUsageError, "The last Claude usage request failed; prime waits for a successful one")
	}
	freshness := max(2*normalizeUsageRefreshInterval(in.RefreshInterval), claudePrimeMinFreshness)
	if in.Now.Sub(fetchedAt) > freshness {
		return skip(claudePrimeReasonUsageStale, "The last Claude usage response is too old; prime waits for a fresh one")
	}
	if in.OrganizationErr != nil || in.Organization == "" || in.Usage.OrganizationID == "" || in.Usage.OrganizationID != in.Organization {
		return skip(claudePrimeReasonSignIn, "The Claude usage response is not from the current sign-in; prime waits for one that is")
	}
	if !in.LastPrimedAt.IsZero() && (!fetchedAt.After(in.LastPrimedAt) || in.Now.Sub(in.LastPrimedAt) < claudePrimeMinInterval) {
		return skip(claudePrimeReasonAwaitingUsage, "Prime ran recently; it waits for a usage response that shows the result")
	}
	session, weekly, _, _, err := parseClaudeOAuthUsage(in.Usage.Body, in.Now)
	if err != nil {
		return skip(claudePrimeReasonUsageUnknown, "Claude usage is unknown; prime waits for a usage response")
	}
	if !session.Known {
		return skip(claudePrimeReasonNoWindow, "This Claude account has no five-hour window, so prime stays idle")
	}
	resetAt, resetErr := time.Parse(time.RFC3339, session.ResetAt)
	// A window that resets even a moment from now is still running: a request
	// now would land in it and start nothing. Reset times are kept to whole
	// seconds, so the window may run up to a second past the stored one.
	if resetErr == nil && resetAt.Add(time.Second).After(in.Now) {
		return claudePrimeDecision{
			Reason:  claudePrimeReasonRunning,
			Message: "The five-hour window is already running",
			Session: &session,
		}
	}
	// A usage response after the last prime that showed the window running,
	// or the idle window's last reset after that prime, shows that a window
	// ran since then. Without either, the prime may have started nothing (for
	// example a request billed elsewhere), so wait out a full window.
	ranSincePrime := in.PrimeConfirmed || (resetErr == nil && resetAt.After(in.LastPrimedAt))
	if !in.LastPrimedAt.IsZero() && !ranSincePrime && in.Now.Sub(in.LastPrimedAt) < claudePrimeUnconfirmedRetry {
		return claudePrimeDecision{
			Reason:  claudePrimeReasonUnconfirmed,
			Message: "No usage response showed the last prime starting a window; prime waits five hours before trying again",
			Session: &session,
		}
	}
	// The owner's guards come last, in the ADR's order.
	if !in.Guards.activeAt(in.Now) {
		return claudePrimeDecision{
			Reason:  claudePrimeReasonInactiveHours,
			Message: fmt.Sprintf("Outside prime's active hours (%02d:00 to %02d:00)", in.Guards.ActiveFrom, in.Guards.ActiveUntil),
			Session: &session,
		}
	}
	if in.Guards.MinWeeklyLeft > 0 && weekly.Known && weekly.PercentRemaining < in.Guards.MinWeeklyLeft {
		return claudePrimeDecision{
			Reason:  claudePrimeReasonWeeklyLow,
			Message: fmt.Sprintf("Less than %.0f%% of the weekly limit is left, so a new five-hour window would not help", in.Guards.MinWeeklyLeft),
			Session: &session,
		}
	}
	if in.Guards.DailyCap > 0 && in.PrimesToday >= in.Guards.DailyCap {
		return claudePrimeDecision{
			Reason:  claudePrimeReasonDailyCap,
			Message: fmt.Sprintf("Prime already ran %d times today, its daily limit", in.PrimesToday),
			Session: &session,
		}
	}
	return claudePrimeDecision{Fire: true, Reason: claudePrimeReasonPrimed, Message: "Prime request sent", Session: &session}
}

// recordClaudePrimeConfirmation notes in state that usage fetched after the
// last prime shows the five-hour window running.
func recordClaudePrimeConfirmation(state *claudePrimeState, usage claudeOAuthUsageCache) bool {
	// Only a response for the sign-in that was primed confirms the prime.
	if state.PrimedAt == "" || state.ConfirmedAt != "" || state.PrimedOrganization == "" || usage.OrganizationID != state.PrimedOrganization {
		return false
	}
	primedAt, err := time.Parse(time.RFC3339Nano, state.PrimedAt)
	if err != nil {
		return false
	}
	fetchedAt, err := time.Parse(time.RFC3339Nano, usage.FetchedAt)
	if err != nil || !fetchedAt.After(primedAt) || len(usage.Body) == 0 {
		return false
	}
	session, _, _, _, err := parseClaudeOAuthUsage(usage.Body, fetchedAt)
	if err != nil || !session.Known {
		return false
	}
	resetAt, err := time.Parse(time.RFC3339, session.ResetAt)
	if err != nil || !resetAt.After(fetchedAt) {
		return false
	}
	state.ConfirmedAt = fetchedAt.UTC().Format(time.RFC3339Nano)
	return true
}

// noteClaudePrimeConfirmation runs with each summary. The widget does not
// ask prime while the window runs, so the summary is what sees a primed window
// running. It changes nothing unless prime has run and is unconfirmed.
func noteClaudePrimeConfirmation() {
	path := claudePrimeStatePath()
	if _, err := os.Stat(path); err != nil {
		return
	}
	_ = withUsageRefreshLock(path, func() error {
		state, err := loadClaudePrimeState(path)
		if err != nil {
			return nil
		}
		usage, err := loadClaudeOAuthUsageCacheStrict(claudeOAuthUsageCachePath())
		if err != nil || usage.LastError != "" || usage.Invalidated {
			return nil
		}
		if !recordClaudePrimeConfirmation(&state, usage) {
			return nil
		}
		return saveClaudePrimeState(path, state)
	})
}

func loadClaudePrimeState(path string) (claudePrimeState, error) {
	var state claudePrimeState
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, errors.New("could not read Claude prime state")
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return claudePrimeState{}, errors.New("Claude prime state is unreadable")
	}
	if state.PrimedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, state.PrimedAt); err != nil {
			return claudePrimeState{}, errors.New("Claude prime state has an invalid time")
		}
	}
	return state, nil
}

func saveClaudePrimeState(path string, state claudePrimeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := atomicWriteStateFile(path, ".claude-prime-*.tmp", data); err != nil {
		return errors.New("could not save Claude prime state")
	}
	return nil
}

func runClaudePrimeCLI(opts claudePrimeOptions) (string, error) {
	prompt := strings.TrimSpace(opts.Prompt)
	if prompt == "" {
		prompt = "Reply with exactly OK."
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := claudePrimeArgs(opts, prompt)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if dir := os.TempDir(); dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_, err := cmd.Output()
	if ctx.Err() != nil {
		return stderr.String(), ctx.Err()
	}
	return stderr.String(), err
}

func claudePrimeArgs(opts claudePrimeOptions, prompt string) []string {
	args := []string{
		firstNonEmpty(commandPath("claude"), "claude"),
		"-p",
		"--safe-mode",
		"--no-session-persistence",
		"--tools", "",
		"--permission-mode", "dontAsk",
		"--system-prompt", "Reply with exactly OK.",
		"--output-format", "json",
		"--prompt-suggestions", "false",
		"--max-budget-usd", "0.001",
	}
	model := firstNonEmpty(strings.TrimSpace(opts.Model), defaultClaudePrimeModel)
	args = append(args, "--model", model, prompt)
	return args
}
