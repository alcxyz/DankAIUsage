package main

import (
	"bytes"
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
	"strings"
	"syscall"
	"time"
)

// Claude limit resets are the "cedar_ember" program of the OAuth usage API:
// the usage response lists granted resets when asked with cedar_ember=1, and
// a grant is redeemed with one POST to the organization's reset_rate_limits
// endpoint. Both are the requests Claude Code's /limit-reset command makes.
const (
	claudeResetProgram      = "cedar_ember"
	claudeResetType         = "claudeLimitReset"
	claudeResetClaimURL     = "https://api.anthropic.com/api/organizations/%s/reset_rate_limits"
	claudeResetClaimTimeout = 25 * time.Second
	claudeResetStateVersion = 1
	claudeResetLockTimeout  = 2 * time.Second
	claudeResetSource       = "claude usage api"
)

// Reset availability is a separate, low-cadence request under Claude Code's
// client identity (ADR-0023). The quota poll never carries that identity, so
// a 429 on this check cannot blank the quota windows.
const (
	claudeResetAvailabilityURL      = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1"
	claudeResetAvailabilityInterval = 30 * time.Minute
	claudeResetAvailabilityBackoff  = time.Hour
	claudeResetAvailabilityStaleTTL = 6 * time.Hour
)

func claudeResetAvailabilityCachePath() string {
	return filepath.Join(pluginStateDir(), "claude-reset-availability.json")
}

// claudeResetAvailabilityCadence spaces reset checks at least half an hour
// apart and no closer than two quota intervals.
func claudeResetAvailabilityCadence(interval time.Duration) time.Duration {
	return max(claudeResetAvailabilityInterval, 2*normalizeUsageRefreshInterval(interval))
}

var (
	claudeResetGrantIDPattern   = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	claudeResetRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	claudeOrganizationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	claudeResetLimitKinds       = []string{
		"five_hour", "seven_day", "seven_day_overage_included", "seven_day_opus",
		"seven_day_sonnet", "seven_day_cowork", "seven_day_omelette", "seven_day_oauth_apps",
	}
)

type claudeResetGrant struct {
	ID               string   `json:"id"`
	Label            string   `json:"label,omitempty"`
	ResetsTotal      int      `json:"resetsTotal"`
	ResetsLeft       int      `json:"resetsLeft"`
	StartsAt         string   `json:"startsAt,omitempty"`
	EndsAt           string   `json:"endsAt,omitempty"`
	Clears           []string `json:"clears,omitempty"`
	Blocking         []string `json:"blocking,omitempty"`
	Paused           bool     `json:"paused"`
	UsableNow        bool     `json:"usableNow"`
	UseRequiresLimit bool     `json:"useRequiresLimit"`
}

type claudeResetAvailability struct {
	Reported         bool
	Eligible         bool
	IneligibleReason string
	AtLimit          bool
	Exhausted        []string
	Grants           []claudeResetGrant
	NextGrantID      string
	WeeklyResetsAt   string
	CooldownUntil    string
}

type claudeResetState struct {
	Version        int    `json:"version"`
	State          string `json:"state"`
	GrantID        string `json:"grantId,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	OrganizationID string `json:"organizationId,omitempty"`
	AttemptedAt    string `json:"attemptedAt,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Message        string `json:"message"`
	ResetsLeft     *int   `json:"resetsLeft,omitempty"`
}

type claudeResetStatus struct {
	OK                bool               `json:"ok"`
	StateKnown        bool               `json:"stateKnown"`
	Reported          bool               `json:"reported"`
	Eligible          bool               `json:"eligible"`
	IneligibleReason  string             `json:"ineligibleReason,omitempty"`
	AtLimit           bool               `json:"atLimit"`
	Grants            []claudeResetGrant `json:"grants"`
	NextGrantID       string             `json:"nextGrantId,omitempty"`
	CooldownUntil     string             `json:"cooldownUntil,omitempty"`
	UsageFetchedAt    string             `json:"usageFetchedAt,omitempty"`
	State             string             `json:"state"`
	Message           string             `json:"message"`
	GrantID           string             `json:"grantId,omitempty"`
	Outcome           string             `json:"outcome,omitempty"`
	Reason            string             `json:"reason,omitempty"`
	LastAttemptAt     string             `json:"lastAttemptAt,omitempty"`
	ResetsLeft        *int               `json:"resetsLeft,omitempty"`
	UsageRefreshed    bool               `json:"usageRefreshed,omitempty"`
	Requested         bool               `json:"requested"`
	AvailabilityError string             `json:"availabilityError,omitempty"`
	// StateUnreadable marks a saved record the helper could not read; only
	// forget recovers from it. Lock or directory failures leave it unset.
	StateUnreadable bool   `json:"stateUnreadable,omitempty"`
	Error           string `json:"error,omitempty"`
}

type claudeResetClaimRequest struct {
	Token          string
	Version        string
	OrganizationID string
	GrantID        string
	RequestID      string
}

type claudeResetClaimResult struct {
	Result         string
	Reason         string
	ResetsLeft     *int
	Cleared        []string
	WeeklyResetsAt string
	CooldownUntil  string
}

type claudeResetDeps struct {
	Now                   func() time.Time
	StatePath             string
	UsageCachePath        string
	AvailabilityCachePath string
	RefreshInterval       time.Duration
	ReadToken             func(time.Time) (string, string, error)
	OrganizationID        func() (string, error)
	ClaudeVersion         func(string) string
	Claim                 func(claudeResetClaimRequest) (claudeResetClaimResult, error)
	ExpireUsage           func(string, time.Time) error
}

func defaultClaudeResetDeps() claudeResetDeps {
	return claudeResetDeps{
		Now:                   time.Now,
		StatePath:             claudeResetStatePath(),
		UsageCachePath:        claudeOAuthUsageCachePath(),
		AvailabilityCachePath: claudeResetAvailabilityCachePath(),
		RefreshInterval:       usageRefreshDefaultInterval,
		ReadToken:             readClaudeOAuthToken,
		OrganizationID:        claudeOrganizationID,
		ClaudeVersion:         claudeCodeVersionString,
		Claim:                 claimClaudeReset,
		ExpireUsage:           expireClaudeUsageCache,
	}
}

func runClaudeResetCommand(args []string) {
	fs := flag.NewFlagSet("claude-reset", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	grant := fs.String("grant", "", "grant to use; defaults to the grant Claude offers next")
	refreshInterval := fs.Int("refresh-interval", int(usageRefreshDefaultInterval/time.Second), "provider usage refresh interval in seconds")
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		writeClaudeResetStatus(claudeResetStatus{State: "error", Message: "Invalid claude-reset arguments", Error: "invalid arguments"}, false, *pretty)
		return
	}
	deps := defaultClaudeResetDeps()
	deps.RefreshInterval = usageRefreshIntervalSeconds(*refreshInterval)
	status, err := runClaudeResetAction(action, *grant, deps)
	writeClaudeResetStatus(status, err == nil, *pretty)
}

func writeClaudeResetStatus(status claudeResetStatus, success, pretty bool) {
	status.OK = success
	if status.Grants == nil {
		status.Grants = []claudeResetGrant{}
	}
	var data []byte
	var err error
	if pretty {
		data, err = json.MarshalIndent(status, "", "  ")
	} else {
		data, err = json.Marshal(status)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode claude reset status")
		os.Exit(1)
	}
	fmt.Println(string(data))
	if !success {
		os.Exit(1)
	}
}

func runClaudeResetAction(action, grantID string, deps claudeResetDeps) (claudeResetStatus, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.StatePath == "" {
		deps.StatePath = claudeResetStatePath()
	}
	if deps.UsageCachePath == "" {
		if deps.StatePath != claudeResetStatePath() {
			deps.UsageCachePath = filepath.Join(filepath.Dir(deps.StatePath), "claude-oauth-usage.json")
		} else {
			deps.UsageCachePath = claudeOAuthUsageCachePath()
		}
	}
	if deps.AvailabilityCachePath == "" {
		if deps.StatePath != claudeResetStatePath() {
			deps.AvailabilityCachePath = filepath.Join(filepath.Dir(deps.StatePath), "claude-reset-availability.json")
		} else {
			deps.AvailabilityCachePath = claudeResetAvailabilityCachePath()
		}
	}
	deps.RefreshInterval = normalizeUsageRefreshInterval(deps.RefreshInterval)
	if deps.ReadToken == nil {
		deps.ReadToken = readClaudeOAuthToken
	}
	if deps.OrganizationID == nil {
		deps.OrganizationID = claudeOrganizationID
	}
	if deps.ClaudeVersion == nil {
		deps.ClaudeVersion = claudeCodeVersionString
	}
	if deps.Claim == nil {
		deps.Claim = claimClaudeReset
	}
	if deps.ExpireUsage == nil {
		deps.ExpireUsage = expireClaudeUsageCache
	}

	var state claudeResetState
	var actionErr error
	stateKnown := false
	stateUnreadable := false
	refreshUsage := false
	requested := false
	err := withStateFileLock(deps.StatePath, claudeResetLockTimeout, "Claude reset state", func() error {
		loaded, err := loadClaudeResetState(deps.StatePath)
		if err != nil && action != "forget" {
			stateUnreadable = true
			return errors.New("saved Claude reset state is unreadable; check Settings → Usage on claude.ai, then run `dankaiusage claude-reset forget` to discard it")
		}
		if err == nil {
			state = loaded
			stateKnown = true
		}
		switch action {
		case "status":
			return nil
		case "use":
			requested, refreshUsage, actionErr = useClaudeReset(&state, grantID, false, deps)
			return nil
		case "retry":
			// Resends an unconfirmed attempt only; it can never start a new one.
			requested, refreshUsage, actionErr = useClaudeReset(&state, grantID, true, deps)
			return nil
		case "forget":
			// Drops an unconfirmed attempt record without contacting Claude.
			// For an account switch or an unreadable record; the server keeps
			// the real outcome.
			state = defaultClaudeResetState()
			state.Message = "Earlier reset attempt forgotten; check Settings → Usage on claude.ai for its outcome"
			if err := saveClaudeResetState(deps.StatePath, state); err != nil {
				return errors.New("could not save Claude reset state")
			}
			stateKnown = true
			return nil
		default:
			actionErr = fmt.Errorf("unknown claude-reset action %q", action)
			return nil
		}
	})
	availability, fetchedAt := claudeResetAvailabilityFromCache(deps.AvailabilityCachePath)
	status := publicClaudeResetStatus(state, availability, fetchedAt, loadClaudeOAuthUsageCache(deps.AvailabilityCachePath).LastError)
	status.StateKnown = stateKnown
	status.StateUnreadable = stateUnreadable
	status.Requested = requested
	if err != nil {
		if !stateKnown {
			status.State = "error"
			status.Message = "Could not inspect saved Claude reset state; the last outcome is unknown"
		}
		status.Error = err.Error()
		return status, err
	}
	// Expiring the usage cache uses the shared refresh lock and runs after the
	// reset lock is released. Failure here cannot cause another account change.
	if refreshUsage {
		// The grant list changed too; let the next summary re-check it.
		_ = deps.ExpireUsage(deps.AvailabilityCachePath, deps.Now())
		if expireErr := deps.ExpireUsage(deps.UsageCachePath, deps.Now()); expireErr == nil {
			status.UsageRefreshed = true
		} else {
			status.Message += " · usage refresh could not be scheduled"
		}
	}
	if actionErr != nil {
		status.Error = actionErr.Error()
		// A refusal before any request is the whole story; do not echo an
		// older saved outcome above it.
		if (action == "use" || action == "retry") && !requested {
			status.Message = actionErr.Error()
		}
	}
	return status, actionErr
}

// useClaudeReset redeems one grant. State is saved as "attempted" before the
// request so a crash or timeout leaves a visible record. An unconfirmed
// attempt is always retried first, with the same grant and request id and
// regardless of what the usage cache offers now, so a reset the server may
// already have applied is reconciled instead of a second one being spent.
// It reports whether a request was sent, whether usage should refresh, and
// the outcome error.
func useClaudeReset(state *claudeResetState, grantID string, retryOnly bool, deps claudeResetDeps) (bool, bool, error) {
	now := deps.Now()
	pending := state.State == "attempted" && claudeResetGrantIDPattern.MatchString(state.GrantID) &&
		claudeResetRequestIDPattern.MatchString(state.RequestID)
	if retryOnly && !pending {
		return false, false, errors.New("no unconfirmed Claude reset attempt to retry; refresh to see the current state")
	}
	// Credentials are read before anything is saved, so a sign-in problem
	// leaves an earlier unconfirmed attempt, and its request id, untouched.
	token, _, err := deps.ReadToken(now)
	if err != nil {
		return false, false, errors.New("Couldn't reset with this login · nothing was sent · " + err.Error())
	}
	organization, err := deps.OrganizationID()
	if err != nil {
		return false, false, errors.New("Couldn't reset with this login · nothing was sent · " + err.Error())
	}
	var grant claudeResetGrant
	requestID := ""
	if pending {
		if grantID != "" && grantID != state.GrantID {
			return false, false, fmt.Errorf("an earlier attempt to use reset %s is unconfirmed; retry it before choosing another reset", state.GrantID)
		}
		if state.OrganizationID != "" && state.OrganizationID != organization {
			return false, false, errors.New("an earlier reset attempt belongs to a different Claude account; sign back in to retry it, or run `dankaiusage claude-reset forget`")
		}
		grant = claudeResetGrant{ID: state.GrantID}
		requestID = state.RequestID
	} else {
		availability, ok := claudeResetAvailabilityForUse(deps.AvailabilityCachePath, now, deps.RefreshInterval, organization, true)
		if !ok {
			return false, false, errors.New("Claude usage data is stale or not tied to this sign-in; refresh usage first")
		}
		if !availability.Reported {
			return false, false, errors.New("Claude reset availability is unknown; refresh usage first")
		}
		if !availability.Eligible {
			return false, false, errors.New(claudeResetIneligibleMessage(availability.IneligibleReason))
		}
		selected, ok := selectClaudeResetGrant(availability, grantID, now)
		if !ok {
			if grantID != "" {
				return false, false, errors.New("that Claude reset is not available; refresh usage and check the offered reset")
			}
			return false, false, errors.New("no usable Claude reset is available")
		}
		grant = selected
		id, err := newUUID()
		if err != nil {
			return false, false, errors.New("could not create a reset request id")
		}
		requestID = id
	}

	*state = claudeResetState{
		Version:        claudeResetStateVersion,
		State:          "attempted",
		GrantID:        grant.ID,
		RequestID:      requestID,
		OrganizationID: organization,
		AttemptedAt:    now.UTC().Format(time.RFC3339),
		Message:        "Reset requested; waiting for Claude to confirm",
	}
	if err := saveClaudeResetState(deps.StatePath, *state); err != nil {
		return false, false, errors.New("could not save Claude reset state before the request")
	}
	// The same Claude Code version as the availability check, so both requests
	// come from one consistent client identity.
	cachedVersion := loadClaudeOAuthUsageCache(deps.AvailabilityCachePath).ClaudeVersion
	if cachedVersion == "" {
		cachedVersion = loadClaudeOAuthUsageCache(deps.UsageCachePath).ClaudeVersion
	}
	result, err := deps.Claim(claudeResetClaimRequest{
		Token:          token,
		Version:        deps.ClaudeVersion(cachedVersion),
		OrganizationID: organization,
		GrantID:        grant.ID,
		RequestID:      requestID,
	})
	if err != nil {
		// Keep "attempted" so a retry reuses the request id: the server may
		// have applied the reset even though no answer arrived.
		state.Outcome = "error"
		state.Message = "Couldn't confirm the reset went through · retry in a moment; if it keeps failing, check Settings → Usage on claude.ai and forget the attempt"
		_ = saveClaudeResetState(deps.StatePath, *state)
		return true, false, errors.New(safeClaudeResetError(err))
	}
	state.Outcome = result.Result
	state.Reason = result.Reason
	state.ResetsLeft = result.ResetsLeft
	refresh := false
	var actionErr error
	switch result.Result {
	case "reset":
		state.State = "used"
		state.Message = "Limits reset" + claudeResetClearedSuffix(result.Cleared) + claudeResetsLeftSuffix(result.ResetsLeft)
		refresh = true
	case "already_used":
		state.State = "used"
		state.Message = "That reset was already used · nothing changed just now" + claudeResetsLeftSuffix(result.ResetsLeft)
		refresh = true
	case "not_limited":
		state.State = "idle"
		state.Message = "Reset kept · it can only be used at a limit · nothing was used"
	case "cooldown":
		state.State = "idle"
		state.Message = "Another reset was just started on your account · nothing was used · try again in a minute"
	case "ineligible":
		state.State = "idle"
		state.Message = claudeResetIneligibleMessage(result.Reason) + " · nothing was used"
		actionErr = errors.New(state.Message)
	case "rate_limited":
		state.State = "failed"
		state.Message = "Claude is rate limiting reset requests · nothing was used · try again in a few minutes"
		actionErr = errors.New(state.Message)
	case "auth_error":
		state.State = "failed"
		state.Message = "Couldn't reset with this login · nothing was used · run any Claude Code session to refresh sign-in"
		actionErr = errors.New(state.Message)
	default:
		state.State = "failed"
		state.Message = "Couldn't reset your limits · nothing was used · try again in a moment"
		actionErr = errors.New(state.Message)
	}
	// Only reset and already_used say what became of the earlier request. Any
	// other answer to a retry (cooldown, ineligible, 429, ...) may describe the
	// account after that request was applied, so the attempt stays unconfirmed
	// and keeps its request id. forget exists for a grant that is truly gone.
	if pending && state.State != "used" {
		state.State = "attempted"
		state.Message = "Couldn't confirm the earlier reset went through · Claude answered: " + result.Result +
			" · check Settings → Usage on claude.ai, then retry or run `dankaiusage claude-reset forget`"
		actionErr = errors.New(state.Message)
	}
	if err := saveClaudeResetState(deps.StatePath, *state); err != nil {
		if actionErr == nil {
			actionErr = errors.New("reset outcome could not be saved; check usage before trying again")
		}
	}
	return true, refresh, actionErr
}

func selectClaudeResetGrant(availability claudeResetAvailability, grantID string, now time.Time) (claudeResetGrant, bool) {
	spendable := spendableClaudeResetGrants(availability, now)
	if grantID != "" {
		for _, grant := range spendable {
			if grant.ID == grantID {
				return grant, true
			}
		}
		return claudeResetGrant{}, false
	}
	for _, grant := range spendable {
		if grant.ID == availability.NextGrantID {
			return grant, true
		}
	}
	if len(spendable) == 1 {
		return spendable[0], true
	}
	return claudeResetGrant{}, false
}

func spendableClaudeResetGrants(availability claudeResetAvailability, now time.Time) []claudeResetGrant {
	var out []claudeResetGrant
	for _, grant := range availability.Grants {
		if grant.ResetsLeft <= 0 || grant.Paused {
			continue
		}
		if ends, err := time.Parse(time.RFC3339, grant.EndsAt); grant.EndsAt != "" && (err != nil || !ends.After(now)) {
			continue
		}
		if starts, err := time.Parse(time.RFC3339, grant.StartsAt); grant.StartsAt != "" && err == nil && starts.After(now) {
			continue
		}
		out = append(out, grant)
	}
	return out
}

// claudeAvailableResets lists spendable grants as provider resets, so the
// widget renders them like Codex credits. Nothing is listed while the account
// or calling surface is ineligible; the reason is reported in meta instead.
func claudeAvailableResets(availability claudeResetAvailability, now time.Time) []UsageReset {
	if !availability.Eligible {
		return nil
	}
	grants := spendableClaudeResetGrants(availability, now)
	resets := make([]UsageReset, 0, len(grants))
	for _, grant := range grants {
		resets = append(resets, UsageReset{
			ID:          grant.ID,
			Title:       firstNonEmpty(grant.Label, "Limit reset"),
			Description: claudeResetGrantDescription(grant),
			ResetType:   claudeResetType,
			ExpiresAt:   grant.EndsAt,
		})
	}
	return resets
}

func claudeResetGrantDescription(grant claudeResetGrant) string {
	parts := []string{fmt.Sprintf("%d left", grant.ResetsLeft)}
	if cleared := claudeResetClearedSuffix(grant.Clears); cleared != "" {
		parts = append(parts, strings.TrimPrefix(cleared, " · "))
	}
	if grant.UseRequiresLimit {
		parts = append(parts, "use at a limit")
	} else {
		parts = append(parts, "usable any time")
	}
	return strings.Join(parts, " · ")
}

func claudeResetMeta(availability claudeResetAvailability, now time.Time) map[string]any {
	if !availability.Reported {
		return nil
	}
	grants := spendableClaudeResetGrants(availability, now)
	count := 0
	for _, grant := range grants {
		count += grant.ResetsLeft
	}
	meta := map[string]any{
		"eligible": availability.Eligible,
		"atLimit":  availability.AtLimit,
	}
	if availability.IneligibleReason != "" {
		meta["ineligibleReason"] = availability.IneligibleReason
	}
	for _, grant := range grants {
		if grant.ID == availability.NextGrantID {
			meta["nextGrantId"] = availability.NextGrantID
		}
	}
	if availability.CooldownUntil != "" {
		meta["cooldownUntil"] = availability.CooldownUntil
	}
	if len(availability.Exhausted) > 0 {
		meta["exhausted"] = availability.Exhausted
	}
	publicGrants := make([]claudeResetGrant, 0, len(grants))
	publicGrants = append(publicGrants, grants...)
	meta["grants"] = publicGrants
	out := map[string]any{"claudeReset": meta}
	if availability.Eligible {
		out["availableResetCount"] = count
	}
	return out
}

// claudeResetsFromUsageCache lists grants from the cached usage body under the
// same staleness rule as the quota windows: a body kept through failed
// refreshes is trusted only within max(stale TTL, two intervals).
// claudeResetsForSummary refreshes the reset availability cache when its own
// cadence allows and lists the grants it holds. A failed check is reported in
// meta and never touches the quota cache.
func claudeResetsForSummary(now time.Time, interval time.Duration) ([]UsageReset, map[string]any) {
	path := claudeResetAvailabilityCachePath()
	refreshErr := refreshClaudeResetAvailability(path, now, interval)
	resets, meta := claudeResetsFromAvailabilityCache(path, now, interval)
	checkError := loadClaudeOAuthUsageCache(path).LastError
	if refreshErr != nil && checkError == "" {
		checkError = refreshErr.Error()
	}
	if checkError != "" {
		if meta == nil {
			meta = map[string]any{}
		}
		meta["claudeResetCheckError"] = checkError
	}
	return resets, meta
}

func claudeResetsFromAvailabilityCache(path string, now time.Time, interval time.Duration) ([]UsageReset, map[string]any) {
	organization, _ := claudeOrganizationID()
	availability, ok := claudeResetAvailabilityForUse(path, now, interval, organization, false)
	if !ok || !availability.Reported {
		return nil, nil
	}
	return claudeAvailableResets(availability, now), claudeResetMeta(availability, now)
}

// refreshClaudeResetAvailability fetches the cedar_ember block under Claude
// Code's client identity at most once per cadence. Failures back off for at
// least an hour and keep the last body for the stale window.
func refreshClaudeResetAvailability(path string, now time.Time, interval time.Duration) error {
	cadence := claudeResetAvailabilityCadence(interval)
	return withUsageRefreshLock(path, func() error {
		cache, err := loadClaudeOAuthUsageCacheStrict(path)
		if err != nil {
			return err
		}
		fetchedAt, err := parseOptionalRefreshTime(cache.FetchedAt)
		if err != nil {
			return err
		}
		nextAttemptAt, err := parseOptionalRefreshTime(cache.NextAttemptAt)
		if err != nil {
			return err
		}
		if !fetchedAt.IsZero() && fetchedAt.Add(cadence).After(nextAttemptAt) {
			nextAttemptAt = fetchedAt.Add(cadence)
		}
		if now.Before(nextAttemptAt) {
			return nil
		}
		// Reserve before the request so a concurrent summary cannot repeat it.
		cache.NextAttemptAt = now.Add(cadence).UTC().Format(time.RFC3339Nano)
		if err := saveClaudeOAuthUsageCache(path, cache); err != nil {
			return errors.New("could not reserve Claude reset check")
		}
		fail := func(message string, backoff time.Duration, status int) error {
			cache.LastError = message
			cache.DiagnosticCategory, cache.DiagnosticHTTPStatus = classifyDiagnosticError("claude", errors.New(message))
			if status != 0 {
				cache.DiagnosticHTTPStatus = status
			}
			wait := max(cadence, backoff, claudeResetAvailabilityBackoff)
			cache.DiagnosticCooldownSeconds = int64(wait / time.Second)
			cache.NextAttemptAt = now.Add(wait).UTC().Format(time.RFC3339Nano)
			if err := saveClaudeOAuthUsageCache(path, cache); err != nil {
				return errors.New("could not save Claude reset check failure")
			}
			return nil
		}
		token, _, err := readClaudeOAuthToken(now)
		if err != nil {
			return fail(err.Error(), 0, 0)
		}
		// Read with the token, before the request, so an account switch during
		// the fetch cannot tag this body with the next sign-in (ADR-0022).
		organization, _ := claudeOrganizationID()
		cache.ClaudeVersion = claudeCodeVersionString(cache.ClaudeVersion)
		body, backoff, err := fetchClaudeOAuthJSON(claudeResetAvailabilityURL, token, claudeResetClientUserAgent(cache.ClaudeVersion))
		if err != nil {
			return fail(strings.Replace(err.Error(), "Claude usage API", "Claude reset check", 1), backoff, 0)
		}
		var root map[string]any
		if err := json.Unmarshal(body, &root); err != nil {
			return fail("Claude reset check returned an unsupported response", 0, 0)
		}
		cache.Body = body
		cache.OrganizationID = organization
		cache.FetchedAt = now.UTC().Format(time.RFC3339Nano)
		cache.NextAttemptAt = now.Add(cadence).UTC().Format(time.RFC3339Nano)
		cache.LastError = ""
		cache.DiagnosticCategory = ""
		cache.DiagnosticHTTPStatus = 0
		cache.DiagnosticCooldownSeconds = 0
		cache.Invalidated = false
		if err := saveClaudeOAuthUsageCache(path, cache); err != nil {
			return errors.New("could not save Claude reset check")
		}
		return nil
	})
}

// claudeResetAvailabilityForUse applies a stale-serve rule before a grant may
// be listed or redeemed: a body kept through failed checks is trusted only
// within max(stale TTL, two check cadences). A cache fetched under a different
// organization than the current sign-in is never offered, and a redemption
// additionally requires the cache to be bound to one at all.
func claudeResetAvailabilityForUse(path string, now time.Time, interval time.Duration, organization string, requireBinding bool) (claudeResetAvailability, bool) {
	cache := loadClaudeOAuthUsageCache(path)
	if cache.OrganizationID != "" && organization != "" && cache.OrganizationID != organization {
		return claudeResetAvailability{}, false
	}
	if requireBinding && (cache.OrganizationID == "" || cache.OrganizationID != organization) {
		return claudeResetAvailability{}, false
	}
	if cache.LastError != "" {
		fetchedAt, err := time.Parse(time.RFC3339Nano, cache.FetchedAt)
		if err != nil || now.Sub(fetchedAt) >= max(claudeResetAvailabilityStaleTTL, 2*claudeResetAvailabilityCadence(interval)) {
			return claudeResetAvailability{}, false
		}
	}
	availability, _ := claudeResetAvailabilityFromCache(path)
	return availability, true
}

func claudeResetAvailabilityFromCache(path string) (claudeResetAvailability, string) {
	cache := loadClaudeOAuthUsageCache(path)
	if len(cache.Body) == 0 {
		return claudeResetAvailability{}, ""
	}
	var root map[string]any
	if err := json.Unmarshal(cache.Body, &root); err != nil {
		return claudeResetAvailability{}, ""
	}
	return parseClaudeResetAvailability(root), cache.FetchedAt
}

func parseClaudeResetAvailability(root map[string]any) claudeResetAvailability {
	block, ok := root[claudeResetProgram].(map[string]any)
	if !ok {
		return claudeResetAvailability{}
	}
	availability := claudeResetAvailability{Reported: true}
	availability.Eligible, _ = block["eligible"].(bool)
	availability.IneligibleReason = stringValue(block["ineligible_reason"])
	availability.AtLimit, _ = block["at_limit"].(bool)
	availability.Exhausted = claudeResetLimitList(block["exhausted"])
	if items, ok := block["grants"].([]any); ok {
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if grant, ok := parseClaudeResetGrant(entry); ok {
				availability.Grants = append(availability.Grants, grant)
			}
		}
	}
	if next := stringValue(block["next_grant_id"]); next != "" {
		for _, grant := range availability.Grants {
			if grant.ID == next {
				availability.NextGrantID = next
			}
		}
	}
	availability.WeeklyResetsAt = stringValue(block["weekly_resets_at"])
	availability.CooldownUntil = stringValue(block["cooldown_until"])
	return availability
}

func parseClaudeResetGrant(entry map[string]any) (claudeResetGrant, bool) {
	grant := claudeResetGrant{ID: stringValue(entry["id"]), UseRequiresLimit: true}
	if !claudeResetGrantIDPattern.MatchString(grant.ID) {
		return claudeResetGrant{}, false
	}
	left, ok := firstFloat(entry, "resets_left")
	if !ok || left < 0 {
		return claudeResetGrant{}, false
	}
	grant.ResetsLeft = int(left)
	if total, ok := firstFloat(entry, "resets_total"); ok && total >= 0 {
		grant.ResetsTotal = int(total)
	}
	grant.Label = stringValue(entry["label"])
	grant.StartsAt = normalizeClaudeResetTime(stringValue(entry["starts_at"]))
	grant.EndsAt = normalizeClaudeResetTime(stringValue(entry["ends_at"]))
	grant.Clears = claudeResetLimitList(entry["clears"])
	grant.Blocking = claudeResetLimitList(entry["blocking"])
	if paused, ok := entry["paused"].(bool); ok {
		grant.Paused = paused
	}
	if usable, ok := entry["usable_now"].(bool); ok {
		grant.UsableNow = usable
	}
	if requires, ok := entry["use_requires_limit"].(bool); ok {
		grant.UseRequiresLimit = requires
	}
	return grant, true
}

func normalizeClaudeResetTime(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}

func claudeResetLimitList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range items {
		kind := stringValue(item)
		for _, known := range claudeResetLimitKinds {
			if kind == known {
				out = append(out, kind)
				break
			}
		}
	}
	return out
}

func claudeResetClearedSuffix(cleared []string) string {
	var names []string
	for _, kind := range cleared {
		switch kind {
		case "five_hour":
			names = append(names, "5-hour")
		case "seven_day":
			names = append(names, "weekly")
		case "seven_day_overage_included":
			continue
		default:
			names = append(names, humanizeScopedLimitLabel(strings.TrimPrefix(kind, "seven_day_")))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return " · clears " + strings.Join(names, " and ")
}

func claudeResetsLeftSuffix(left *int) string {
	if left == nil {
		return ""
	}
	return fmt.Sprintf(" · %d left", *left)
}

func claudeResetIneligibleMessage(reason string) string {
	switch reason {
	case "surface", "cli_version":
		return "Claude does not offer the reset to this client right now; use Settings → Usage on claude.ai"
	case "tier", "seat", "no_grant", "tenure", "config_off", "other_experiment":
		return "A limit reset isn't available for this account right now"
	case "not_limited":
		return "This reset can only be used at a limit"
	case "already_used":
		return "That reset was already used"
	case "expired":
		return "This reset isn't available any more"
	case "cooldown":
		return "Another reset was just started on your account"
	case "unavailable", "unknown", "":
		return "A limit reset isn't available right now"
	default:
		return "A limit reset isn't available right now (" + reason + ")"
	}
}

func publicClaudeResetStatus(state claudeResetState, availability claudeResetAvailability, fetchedAt string, checkError string) claudeResetStatus {
	status := claudeResetStatus{
		StateKnown:        true,
		Reported:          availability.Reported,
		Eligible:          availability.Eligible,
		IneligibleReason:  availability.IneligibleReason,
		AtLimit:           availability.AtLimit,
		Grants:            []claudeResetGrant{},
		NextGrantID:       availability.NextGrantID,
		CooldownUntil:     availability.CooldownUntil,
		UsageFetchedAt:    fetchedAt,
		AvailabilityError: checkError,
		State:             state.State,
		Message:           state.Message,
		GrantID:           state.GrantID,
		Outcome:           state.Outcome,
		Reason:            state.Reason,
		LastAttemptAt:     state.AttemptedAt,
		ResetsLeft:        state.ResetsLeft,
	}
	status.Grants = append(status.Grants, availability.Grants...)
	if status.State == "" {
		status.State = "idle"
	}
	if status.Message == "" {
		switch {
		case !availability.Reported && checkError != "":
			status.Message = "Claude could not report limit resets: " + checkError
		case !availability.Reported:
			status.Message = "Claude reset availability is unknown until the next reset check"
		case !availability.Eligible:
			status.Message = claudeResetIneligibleMessage(availability.IneligibleReason)
		case len(availability.Grants) == 0:
			status.Message = "No Claude limit reset is available"
		default:
			status.Message = "A Claude limit reset is available"
		}
	}
	return status
}

func defaultClaudeResetState() claudeResetState {
	return claudeResetState{Version: claudeResetStateVersion, State: "idle"}
}

func claudeResetStatePath() string {
	return filepath.Join(pluginStateDir(), "claude-reset.json")
}

func loadClaudeResetState(path string) (claudeResetState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultClaudeResetState(), nil
	}
	if err != nil {
		return claudeResetState{}, err
	}
	var state claudeResetState
	if err := json.Unmarshal(data, &state); err != nil {
		return claudeResetState{}, err
	}
	if state.Version != claudeResetStateVersion || state.State == "" {
		return claudeResetState{}, errors.New("unsupported Claude reset state")
	}
	return state, nil
}

func saveClaudeResetState(path string, state claudeResetState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicWriteUsageRefreshFile(path, data)
}

// expireClaudeUsageCache drops the pre-reset quota snapshot and allows the
// next summary to fetch immediately, so the refilled windows appear without
// waiting out the regular cooldown. FetchedAt is cleared too: the collector
// floors the next attempt at FetchedAt plus the interval, which would
// otherwise keep the cooldown alive with no body to serve.
func expireClaudeUsageCache(path string, now time.Time) error {
	return withUsageRefreshLock(path, func() error {
		cache, err := loadClaudeOAuthUsageCacheStrict(path)
		if err != nil {
			return err
		}
		next := now
		// A backoff the provider asked for after a failed fetch still stands;
		// only the ordinary interval cooldown is skipped.
		if cache.LastError != "" {
			if existing, err := parseOptionalRefreshTime(cache.NextAttemptAt); err == nil && existing.After(next) {
				next = existing
			}
		}
		cache.Body = nil
		cache.FetchedAt = ""
		cache.Invalidated = true
		cache.NextAttemptAt = next.UTC().Format(time.RFC3339Nano)
		return saveClaudeOAuthUsageCache(path, cache)
	})
}

// claudeOrganizationID reads the signed-in organization from Claude Code's
// configuration. It is an identifier, not a credential.
func claudeOrganizationID() (string, error) {
	candidates := []string{filepath.Join(claudeHome(), ".claude.json")}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".claude.json"))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var config struct {
			OAuthAccount struct {
				OrganizationUUID string `json:"organizationUuid"`
			} `json:"oauthAccount"`
		}
		if err := json.Unmarshal(data, &config); err != nil {
			continue
		}
		if id := config.OAuthAccount.OrganizationUUID; claudeOrganizationIDPattern.MatchString(id) {
			return id, nil
		}
	}
	return "", errors.New("Claude organization is unknown; run any Claude Code session once")
}

func claimClaudeReset(request claudeResetClaimRequest) (claudeResetClaimResult, error) {
	if !claudeResetGrantIDPattern.MatchString(request.GrantID) || !claudeResetRequestIDPattern.MatchString(request.RequestID) {
		return claudeResetClaimResult{}, errors.New("refusing to claim with a malformed grant or request id")
	}
	if !claudeOrganizationIDPattern.MatchString(request.OrganizationID) {
		return claudeResetClaimResult{}, errors.New("refusing to claim with a malformed organization id")
	}
	body, err := json.Marshal(map[string]string{
		"program":    claudeResetProgram,
		"grant_id":   request.GrantID,
		"request_id": request.RequestID,
	})
	if err != nil {
		return claudeResetClaimResult{}, err
	}
	endpoint := fmt.Sprintf(claudeResetClaimURL, url.PathEscape(request.OrganizationID))
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return claudeResetClaimResult{}, err
	}
	setClaudeOAuthHeaders(req, request.Token, claudeResetClientUserAgent(request.Version))
	client := &http.Client{Timeout: claudeResetClaimTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return claudeResetClaimResult{}, fmt.Errorf("Claude reset request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return claudeResetClaimResult{}, err
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return claudeResetClaimResult{Result: "rate_limited"}, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return claudeResetClaimResult{Result: "auth_error"}, nil
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return claudeResetClaimResult{}, fmt.Errorf("Claude reset endpoint returned HTTP %d", resp.StatusCode)
	}
	return parseClaudeResetClaimResponse(data)
}

func parseClaudeResetClaimResponse(data []byte) (claudeResetClaimResult, error) {
	var wire struct {
		Result         string   `json:"result"`
		Reason         string   `json:"reason"`
		ResetsLeft     *int     `json:"resets_left"`
		Cleared        []string `json:"cleared"`
		WeeklyResetsAt string   `json:"weekly_resets_at"`
		CooldownUntil  string   `json:"cooldown_until"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return claudeResetClaimResult{}, errors.New("Claude reset response could not be read")
	}
	result := claudeResetClaimResult{
		Result:         wire.Result,
		Reason:         wire.Reason,
		ResetsLeft:     wire.ResetsLeft,
		WeeklyResetsAt: wire.WeeklyResetsAt,
		CooldownUntil:  wire.CooldownUntil,
	}
	switch result.Result {
	case "reset", "already_used", "not_limited", "cooldown", "ineligible", "unavailable":
	default:
		// An outcome this helper does not know cannot be called "nothing was
		// used"; keep the attempt pending like an unreadable response.
		return claudeResetClaimResult{}, errors.New("Claude reset response had an unknown outcome")
	}
	for _, kind := range wire.Cleared {
		for _, known := range claudeResetLimitKinds {
			if kind == known {
				result.Cleared = append(result.Cleared, kind)
				break
			}
		}
	}
	return result, nil
}

// safeClaudeResetError keeps transport errors short and free of request
// details before they reach the widget. A url.Error repeats the request URL,
// which names the organization, so only its underlying cause is kept.
func safeClaudeResetError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	message := err.Error()
	if len(message) > 160 {
		message = message[:160]
	}
	return message
}

// withStateFileLock serializes helper-owned state files across processes
// with an exclusive flock on a sidecar lock file.
func withStateFileLock(path string, timeout time.Duration, label string, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create %s directory", label)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("could not protect %s directory", label)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("could not open %s lock", label)
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return fmt.Errorf("could not protect %s lock", label)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("could not lock %s", label)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for %s lock", label)
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}
