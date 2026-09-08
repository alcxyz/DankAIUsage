package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	codexResetThreshold       = 99.0
	codexResetSafetyWindow    = 10 * time.Minute
	codexResetCommandTimeout  = 10 * time.Second
	codexResetLockTimeout     = 2 * time.Second
	codexResetStateVersion    = 1
	codexResetOutcomeUnknown  = "Reset attempt outcome is unknown; arm again only to make a new explicit attempt"
	codexResetProtocolFailure = "Codex app-server reset protocol is unavailable"
)

var errCodexResetStateAmbiguous = errors.New("Codex reset state durability is unknown")

type codexResetState struct {
	Version        int    `json:"version"`
	Armed          bool   `json:"armed"`
	CreditID       string `json:"creditId,omitempty"`
	Title          string `json:"title,omitempty"`
	ResetType      string `json:"resetType,omitempty"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	State          string `json:"state"`
	Message        string `json:"message"`
	Outcome        string `json:"outcome,omitempty"`
	LastAttemptAt  string `json:"lastAttemptAt,omitempty"`
	Refreshed      bool   `json:"refreshed,omitempty"`
}

type codexResetStatus struct {
	OK            bool   `json:"ok"`
	StateKnown    bool   `json:"stateKnown"`
	Armed         bool   `json:"armed"`
	CanArm        bool   `json:"canArm"`
	State         string `json:"state"`
	Message       string `json:"message"`
	CreditID      string `json:"creditId,omitempty"`
	Title         string `json:"title,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	LastAttemptAt string `json:"lastAttemptAt,omitempty"`
	Refreshed     bool   `json:"refreshed,omitempty"`
	Error         string `json:"error,omitempty"`
}

type codexRateLimitsResult struct {
	RateLimits            codexRateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID   map[string]codexRateLimitSnapshot `json:"rateLimitsByLimitId"`
	RateLimitResetCredits codexRateLimitResetCredits        `json:"rateLimitResetCredits"`
}

type codexResetClient interface {
	ReadRateLimits() (codexRateLimitsResult, error)
	ConsumeReset(creditID, idempotencyKey string) (string, error)
	Close()
}

type codexResetDeps struct {
	Now        func() time.Time
	StatePath  string
	OpenClient func(context.Context) (codexResetClient, error)
	Timeout    time.Duration
}

func defaultCodexResetDeps() codexResetDeps {
	return codexResetDeps{
		Now:        time.Now,
		StatePath:  codexResetStatePath(),
		OpenClient: openCodexResetClient,
		Timeout:    codexResetCommandTimeout,
	}
}

func runCodexResetCommand(args []string) {
	fs := flag.NewFlagSet("codex-reset", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		writeCodexResetStatus(codexResetStatus{State: "error", Message: "Invalid codex-reset arguments", Error: "invalid arguments"}, false, *pretty)
		return
	}
	if fs.NArg() > 0 {
		writeCodexResetStatus(codexResetStatus{State: "error", Message: "Invalid codex-reset arguments", Error: "invalid arguments"}, false, *pretty)
		return
	}

	status, err := runCodexResetAction(action, defaultCodexResetDeps())
	writeCodexResetStatus(status, err == nil, *pretty)
}

func writeCodexResetStatus(status codexResetStatus, success, pretty bool) {
	status.OK = success
	var data []byte
	var err error
	if pretty {
		data, err = json.MarshalIndent(status, "", "  ")
	} else {
		data, err = json.Marshal(status)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode codex reset status")
		os.Exit(1)
	}
	fmt.Println(string(data))
	if !success {
		os.Exit(1)
	}
}

func runCodexResetAction(action string, deps codexResetDeps) (codexResetStatus, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.StatePath == "" {
		deps.StatePath = codexResetStatePath()
	}
	if deps.OpenClient == nil {
		deps.OpenClient = openCodexResetClient
	}
	if deps.Timeout <= 0 {
		deps.Timeout = codexResetCommandTimeout
	}

	var state codexResetState
	var actionErr error
	stateKnown := false
	err := withCodexResetLock(deps.StatePath, func() error {
		// Disarm is the recovery operation as well as the ordinary off toggle.
		// It intentionally replaces even malformed or unsupported saved state.
		if action == "disarm" {
			candidate := defaultCodexResetState()
			if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
				return codexResetSaveError("could not save Codex reset state", err)
			}
			state = candidate
			stateKnown = true
			return nil
		}
		loaded, err := loadCodexResetState(deps.StatePath)
		if err != nil {
			return errors.New("saved Codex reset state is unreadable")
		}
		state = loaded
		stateKnown = true
		switch action {
		case "status":
			return nil
		case "arm":
			actionErr = armCodexReset(&state, deps)
			return nil
		case "check":
			if !state.Armed {
				return nil
			}
			actionErr = checkCodexReset(&state, deps)
			return nil
		default:
			actionErr = fmt.Errorf("unknown codex-reset action %q", action)
			return nil
		}
	})
	if err != nil {
		status := publicCodexResetStatus(state)
		status.StateKnown = stateKnown
		if !stateKnown {
			status.CanArm = false
			status.State = "error"
			status.Message = "Could not inspect saved Codex reset state; armed state is unknown"
		}
		status.Error = err.Error()
		return status, err
	}
	status := publicCodexResetStatus(state)
	if actionErr != nil {
		status.Error = actionErr.Error()
		if errors.Is(actionErr, errCodexResetStateAmbiguous) {
			status.StateKnown = false
			status.CanArm = false
			status.State = "error"
			status.Message = "Could not confirm durable Codex reset state; armed state is unknown"
		}
	}
	return status, actionErr
}

func armCodexReset(state *codexResetState, deps codexResetDeps) error {
	if state.Armed {
		return errors.New("automatic Codex reset is already armed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), deps.Timeout)
	defer cancel()
	client, err := deps.OpenClient(ctx)
	if err != nil {
		return errors.New(codexResetProtocolFailure)
	}
	defer client.Close()
	limits, err := client.ReadRateLimits()
	if err != nil {
		return errors.New(safeCodexResetError(err))
	}
	now := deps.Now()
	credit, ok := selectCodexResetCredit(limits.RateLimitResetCredits, now)
	if !ok {
		candidate := defaultCodexResetState()
		candidate.Message = "No eligible earned Codex reset credit is available"
		if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
			return codexResetSaveError("could not save Codex reset state", err)
		}
		*state = candidate
		return errors.New("no eligible earned reset credit")
	}
	key, err := newUUID()
	if err != nil {
		return errors.New("could not create reset attempt identifier")
	}
	candidate := codexResetState{
		Version:        codexResetStateVersion,
		Armed:          true,
		CreditID:       credit.ID,
		Title:          firstNonEmpty(credit.Title, "Usage reset"),
		ResetType:      credit.ResetType,
		ExpiresAt:      time.Unix(credit.ExpiresAt, 0).UTC().Format(time.RFC3339),
		IdempotencyKey: key,
		State:          "armed",
		Message:        "Automatic Codex reset is armed",
	}
	if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
		return codexResetSaveError("could not save Codex reset state", err)
	}
	*state = candidate
	return nil
}

func checkCodexReset(state *codexResetState, deps codexResetDeps) error {
	now := deps.Now()
	expiresAt, err := time.Parse(time.RFC3339, state.ExpiresAt)
	if err != nil || !expiresAt.After(now) {
		candidate := *state
		candidate.Armed = false
		candidate.State = "off"
		candidate.Message = "The armed Codex reset credit expired without being used"
		if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
			return codexResetSaveError("could not save Codex reset state", err)
		}
		*state = candidate
		return errors.New("armed reset credit expired")
	}

	ctx, cancel := context.WithTimeout(context.Background(), deps.Timeout)
	defer cancel()
	client, err := deps.OpenClient(ctx)
	if err != nil {
		candidate := *state
		candidate.State = "error"
		candidate.Message = "Could not refresh Codex limits; the reset remains armed"
		if saveCodexResetState(deps.StatePath, candidate) == nil {
			*state = candidate
		}
		return errors.New(codexResetProtocolFailure)
	}
	defer client.Close()
	limits, err := client.ReadRateLimits()
	if err != nil {
		candidate := *state
		candidate.State = "error"
		candidate.Message = "Could not refresh Codex limits; the reset remains armed"
		if saveCodexResetState(deps.StatePath, candidate) == nil {
			*state = candidate
		}
		return errors.New(safeCodexResetError(err))
	}
	// The app-server read can take long enough to cross a reset or credit
	// expiry boundary. All eligibility decisions use a clock sample taken
	// after that fresh response arrives.
	now = deps.Now()
	credit, ok := matchingCodexResetCredit(limits.RateLimitResetCredits, *state, now)
	if !ok {
		candidate := *state
		candidate.State = "waiting"
		candidate.Message = "The armed reset credit is not currently available; no other credit will be substituted"
		if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
			return codexResetSaveError("could not save Codex reset state", err)
		}
		*state = candidate
		return nil
	}
	trigger, reason := shouldConsumeCodexReset(limits, credit, now)
	if !trigger {
		candidate := *state
		candidate.State = "waiting"
		candidate.Message = reason
		if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
			return codexResetSaveError("could not save Codex reset state", err)
		}
		*state = candidate
		return nil
	}

	// Persist the one-shot transition before sending the mutating RPC. A crash,
	// timeout, or protocol error after this point can never cause an automatic
	// retry from another widget or helper process.
	candidate := *state
	candidate.Armed = false
	candidate.State = "attempted"
	candidate.Message = codexResetOutcomeUnknown
	candidate.Outcome = ""
	candidate.LastAttemptAt = now.UTC().Format(time.RFC3339)
	candidate.Refreshed = false
	if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
		return codexResetSaveError("could not persist one-shot reset state", err)
	}
	*state = candidate

	outcome, consumeErr := client.ConsumeReset(state.CreditID, state.IdempotencyKey)
	if consumeErr != nil {
		candidate := *state
		candidate.State = "error"
		candidate.Message = codexResetOutcomeUnknown
		if saveCodexResetState(deps.StatePath, candidate) == nil {
			*state = candidate
		}
		return errors.New(safeCodexResetError(consumeErr))
	}
	candidate = *state
	candidate.Outcome = outcome
	switch outcome {
	case "reset":
		candidate.State = "completed"
		candidate.Message = "Codex limits were reset; automatic reset is off"
	case "alreadyRedeemed":
		candidate.State = "completed"
		candidate.Message = "This reset attempt was already redeemed; automatic reset is off"
	case "nothingToReset":
		candidate.State = "completed"
		candidate.Message = "Codex reported nothing eligible to reset; automatic reset is off"
	case "noCredit":
		candidate.State = "completed"
		candidate.Message = "Codex reported no earned reset credit; automatic reset is off"
	default:
		candidate.State = "error"
		candidate.Message = codexResetOutcomeUnknown
		candidate.Outcome = ""
		if saveCodexResetState(deps.StatePath, candidate) == nil {
			*state = candidate
		}
		return errors.New(codexResetProtocolFailure)
	}
	if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
		return codexResetSaveError("could not save reset outcome", err)
	}
	*state = candidate

	if _, err := client.ReadRateLimits(); err != nil {
		candidate = *state
		candidate.Message += "; refreshed limits are unavailable"
		if saveCodexResetState(deps.StatePath, candidate) == nil {
			*state = candidate
		}
		return errors.New("reset outcome received, but refreshed Codex limits are unavailable")
	}
	candidate = *state
	candidate.Refreshed = true
	if err := saveCodexResetState(deps.StatePath, candidate); err != nil {
		return codexResetSaveError("could not save refreshed reset status", err)
	}
	*state = candidate
	if outcome == "nothingToReset" || outcome == "noCredit" {
		return errors.New("Codex did not apply the earned reset")
	}
	return nil
}

func selectCodexResetCredit(credits codexRateLimitResetCredits, now time.Time) (codexRateLimitResetCredit, bool) {
	if credits.AvailableCount <= 0 {
		return codexRateLimitResetCredit{}, false
	}
	var selected codexRateLimitResetCredit
	for _, credit := range credits.Credits {
		if credit.ID == "" || credit.ResetType != "codexRateLimits" || credit.Status != "available" || credit.ExpiresAt <= 0 {
			continue
		}
		expiresAt := time.Unix(credit.ExpiresAt, 0)
		if !expiresAt.After(now) {
			continue
		}
		if selected.ID == "" || credit.ExpiresAt < selected.ExpiresAt || (credit.ExpiresAt == selected.ExpiresAt && credit.ID < selected.ID) {
			selected = credit
		}
	}
	return selected, selected.ID != ""
}

func matchingCodexResetCredit(credits codexRateLimitResetCredits, state codexResetState, now time.Time) (codexRateLimitResetCredit, bool) {
	if credits.AvailableCount <= 0 {
		return codexRateLimitResetCredit{}, false
	}
	wantExpiry, err := time.Parse(time.RFC3339, state.ExpiresAt)
	if err != nil || !wantExpiry.After(now) {
		return codexRateLimitResetCredit{}, false
	}
	for _, credit := range credits.Credits {
		if credit.ID != state.CreditID || credit.ResetType != state.ResetType || credit.ResetType != "codexRateLimits" || credit.Status != "available" {
			continue
		}
		expiresAt := time.Unix(credit.ExpiresAt, 0).UTC()
		if credit.ExpiresAt > 0 && expiresAt.Equal(wantExpiry.UTC()) && expiresAt.After(now) {
			return credit, true
		}
	}
	return codexRateLimitResetCredit{}, false
}

func shouldConsumeCodexReset(limits codexRateLimitsResult, credit codexRateLimitResetCredit, now time.Time) (bool, string) {
	snapshot, ok := generalCodexRateLimitSnapshot(limits)
	if !ok {
		return false, "Waiting for the general Codex allowance"
	}
	windows := []*codexRateLimitWindow{snapshot.Primary, snapshot.Secondary}
	hasLiveUsage := false
	for _, window := range windows {
		if window != nil && window.UsedPercent > 0 && window.ResetsAt != nil && *window.ResetsAt > 0 && time.Unix(*window.ResetsAt, 0).After(now) {
			hasLiveUsage = true
		}
	}
	creditExpiresAt := time.Unix(credit.ExpiresAt, 0)
	if creditExpiresAt.After(now) && !creditExpiresAt.After(now.Add(codexResetSafetyWindow)) && hasLiveUsage {
		return true, "Earned reset credit is near expiry"
	}
	for _, window := range windows {
		if window == nil || window.UsedPercent < codexResetThreshold || window.ResetsAt == nil || *window.ResetsAt <= 0 {
			continue
		}
		if time.Unix(*window.ResetsAt, 0).After(now.Add(codexResetSafetyWindow)) {
			return true, "General Codex allowance reached the reset threshold"
		}
	}
	return false, "Waiting for 99% general Codex usage or the credit-expiry safety window"
}

func generalCodexRateLimitSnapshot(limits codexRateLimitsResult) (codexRateLimitSnapshot, bool) {
	if snapshot, ok := limits.RateLimitsByLimitID["codex"]; ok && codexSnapshotHasWindow(snapshot) {
		return snapshot, true
	}
	for _, snapshot := range limits.RateLimitsByLimitID {
		if snapshot.LimitID == "codex" && codexSnapshotHasWindow(snapshot) {
			return snapshot, true
		}
	}
	snapshot := limits.RateLimits
	id := strings.ToLower(snapshot.LimitID)
	name := strings.ToLower(snapshot.LimitName)
	if codexSnapshotHasWindow(snapshot) && (id == "" || id == "codex") && !strings.Contains(id, "spark") && !strings.Contains(name, "spark") {
		return snapshot, true
	}
	return codexRateLimitSnapshot{}, false
}

func codexSnapshotHasWindow(snapshot codexRateLimitSnapshot) bool {
	return snapshot.Primary != nil || snapshot.Secondary != nil
}

func defaultCodexResetState() codexResetState {
	return codexResetState{
		Version: codexResetStateVersion,
		State:   "off",
		Message: "Automatic Codex reset is off",
	}
}

func publicCodexResetStatus(state codexResetState) codexResetStatus {
	if state.State == "" {
		state = defaultCodexResetState()
	}
	return codexResetStatus{
		StateKnown:    true,
		Armed:         state.Armed,
		CanArm:        !state.Armed,
		State:         state.State,
		Message:       state.Message,
		CreditID:      state.CreditID,
		Title:         state.Title,
		ExpiresAt:     state.ExpiresAt,
		Outcome:       state.Outcome,
		LastAttemptAt: state.LastAttemptAt,
		Refreshed:     state.Refreshed,
	}
}

func codexResetStatePath() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "dankaiusage", "codex-reset.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dankaiusage", "codex-reset.json")
}

func withCodexResetLock(path string, fn func() error) error {
	return withCodexResetLockTimeout(path, codexResetLockTimeout, fn)
}

func withCodexResetLockTimeout(path string, timeout time.Duration, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("could not create Codex reset state directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return errors.New("could not protect Codex reset state directory")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.New("could not open Codex reset state lock")
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return errors.New("could not protect Codex reset state lock")
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return errors.New("could not lock Codex reset state")
		}
		if !time.Now().Before(deadline) {
			return errors.New("timed out waiting for Codex reset state lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func loadCodexResetState(path string) (codexResetState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultCodexResetState(), nil
	}
	if err != nil {
		return codexResetState{}, err
	}
	var state codexResetState
	if err := json.Unmarshal(data, &state); err != nil {
		return codexResetState{}, err
	}
	if state.Version != codexResetStateVersion || state.State == "" {
		return codexResetState{}, errors.New("unsupported Codex reset state")
	}
	if state.Armed && (state.CreditID == "" || state.IdempotencyKey == "" || state.ExpiresAt == "" || state.ResetType != "codexRateLimits") {
		return codexResetState{}, errors.New("incomplete Codex reset state")
	}
	return state, nil
}

func saveCodexResetState(path string, state codexResetState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".codex-reset-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("%w: open state directory", errCodexResetStateAmbiguous)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("%w: sync state directory", errCodexResetStateAmbiguous)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("%w: close state directory", errCodexResetStateAmbiguous)
	}
	return nil
}

func codexResetSaveError(message string, err error) error {
	if errors.Is(err, errCodexResetStateAmbiguous) {
		return fmt.Errorf("%s: %w", message, errCodexResetStateAmbiguous)
	}
	return errors.New(message)
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

type codexAppServerClient struct {
	ctx     context.Context
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	nextID  int
}

func openCodexResetClient(ctx context.Context) (codexResetClient, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return nil, err
	}
	return newCodexAppServerClient(ctx, path, "app-server", "--listen", "stdio://", "--analytics-default-enabled")
}

func newCodexAppServerClient(parent context.Context, command string, args ...string) (*codexAppServerClient, error) {
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	client := &codexAppServerClient{
		ctx:     ctx,
		cancel:  cancel,
		cmd:     cmd,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		nextID:  1,
	}
	client.scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var initialized map[string]any
	if err := client.call("initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "dankaiusage", "title": "DankAIUsage", "version": version},
		"capabilities": nil,
	}, &initialized); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (client *codexAppServerClient) ReadRateLimits() (codexRateLimitsResult, error) {
	var result codexRateLimitsResult
	err := client.call("account/rateLimits/read", nil, &result)
	return result, err
}

func (client *codexAppServerClient) ConsumeReset(creditID, idempotencyKey string) (string, error) {
	var result struct {
		Outcome string `json:"outcome"`
	}
	err := client.call("account/rateLimitResetCredit/consume", map[string]string{
		"creditId":       creditID,
		"idempotencyKey": idempotencyKey,
	}, &result)
	if err != nil {
		return "", err
	}
	return result.Outcome, nil
}

func (client *codexAppServerClient) call(method string, params any, result any) error {
	client.nextID++
	id := client.nextID
	request := struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{ID: id, Method: method, Params: params}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := client.stdin.Write(append(data, '\n')); err != nil {
		return err
	}
	for client.scanner.Scan() {
		var envelope struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(client.scanner.Bytes(), &envelope); err != nil || envelope.ID != id {
			continue
		}
		if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
			var rpcErr struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(envelope.Error, &rpcErr)
			return codexRPCError{Code: rpcErr.Code}
		}
		if len(envelope.Result) == 0 {
			return errors.New("missing app-server result")
		}
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return errors.New("invalid app-server result")
		}
		return nil
	}
	if client.ctx.Err() != nil {
		return client.ctx.Err()
	}
	if err := client.scanner.Err(); err != nil {
		return err
	}
	return errors.New("app-server closed without a response")
}

func (client *codexAppServerClient) Close() {
	_ = client.stdin.Close()
	client.cancel()
	_ = client.cmd.Wait()
}

type codexRPCError struct {
	Code int
}

func (err codexRPCError) Error() string {
	return fmt.Sprintf("Codex app-server RPC error %d", err.Code)
}

func safeCodexResetError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "Codex app-server reset request timed out"
	}
	var rpcErr codexRPCError
	if errors.As(err, &rpcErr) {
		return codexResetProtocolFailure
	}
	return codexResetProtocolFailure
}
