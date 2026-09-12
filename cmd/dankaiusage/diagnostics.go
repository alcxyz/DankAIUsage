package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

const (
	diagnosticStoreVersion = 1
	diagnosticMaxEvents    = 100
	diagnosticMaxBytes     = 64 << 10
	diagnosticRetention    = 7 * 24 * time.Hour
	diagnosticLockTimeout  = 500 * time.Millisecond
	diagnosticMaxCooldown  = 7 * 24 * time.Hour
)

var revision string

type diagnosticEvent struct {
	Time            string `json:"time"`
	Provider        string `json:"provider"`
	Category        string `json:"category"`
	HTTPStatus      int    `json:"httpStatus,omitempty"`
	CooldownSeconds int64  `json:"cooldownSeconds,omitempty"`
	Version         string `json:"version,omitempty"`
	Revision        string `json:"revision,omitempty"`
}

type diagnosticStore struct {
	Version int               `json:"version"`
	Events  []diagnosticEvent `json:"events"`
}

type diagnosticReport struct {
	Report    string `json:"report"`
	Available bool   `json:"available"`
}

var diagnosticCategories = map[string]string{
	"authentication":       "Authentication failed",
	"backoff":              "Refresh backoff started",
	"helper_failed":        "Widget helper process failed",
	"invalid_response":     "Provider response was invalid",
	"local_state":          "Local refresh state failed",
	"network":              "Provider could not be reached",
	"provider_unavailable": "Provider usage is unavailable",
	"rate_limited":         "Provider rate limit was reached",
	"recovered":            "Usage refresh recovered",
	"request_rejected":     "Provider rejected the usage request",
}

func pluginStateDir() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" && filepath.IsAbs(value) {
		return filepath.Join(value, "dankaiusage")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dankaiusage")
}

func diagnosticsPath() string { return filepath.Join(pluginStateDir(), "diagnostics.json") }

func runDiagnosticsCommand(args []string) {
	if len(args) == 1 && args[0] == "helper-failed" {
		if err := recordDiagnostic(diagnosticEvent{Provider: "helper", Category: "helper_failed"}); err != nil {
			writeDiagnosticJSON(diagnosticReport{Report: "Diagnostics are unavailable.", Available: false})
			return
		}
		writeDiagnosticJSON(diagnosticReport{Report: "Widget helper failure recorded.", Available: true})
		return
	}
	if len(args) != 0 {
		writeDiagnosticJSON(diagnosticReport{Report: "Diagnostics are unavailable.", Available: false})
		return
	}
	report, err := renderDiagnostics(diagnosticsPath(), time.Now())
	if err != nil {
		writeDiagnosticJSON(diagnosticReport{Report: "Diagnostics are unavailable.", Available: false})
		return
	}
	writeDiagnosticJSON(diagnosticReport{Report: report, Available: true})
}

func writeDiagnosticJSON(value diagnosticReport) {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Println(`{"report":"Diagnostics are unavailable.","available":false}`)
		return
	}
	fmt.Println(string(data))
}

func recordDiagnostic(event diagnosticEvent) error {
	event.Time = time.Now().UTC().Format(time.RFC3339)
	event.Version, event.Revision = diagnosticBuildIdentity()
	return appendDiagnosticEvent(diagnosticsPath(), time.Now(), event)
}

func appendDiagnosticEvent(path string, now time.Time, event diagnosticEvent) error {
	validated, ok := validateDiagnosticEvent(event, now)
	if !ok || !filepath.IsAbs(path) {
		return errors.New("invalid diagnostic event")
	}
	return withDiagnosticLock(path, func() error {
		store, err := loadDiagnosticStore(path, now, false)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if store.Version == 0 {
			store.Version = diagnosticStoreVersion
		}
		if diagnosticTransitionAlreadyRecorded(store.Events, validated) {
			return nil
		}
		store.Events = append(store.Events, validated)
		if len(store.Events) > diagnosticMaxEvents {
			store.Events = store.Events[len(store.Events)-diagnosticMaxEvents:]
		}
		data, err := json.Marshal(store)
		if err != nil || len(data) > diagnosticMaxBytes {
			return errors.New("diagnostic store is too large")
		}
		return atomicWriteDiagnosticFile(path, data)
	})
}

func loadDiagnosticStore(path string, now time.Time, checkPermissions bool) (diagnosticStore, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return diagnosticStore{Version: diagnosticStoreVersion, Events: []diagnosticEvent{}}, os.ErrNotExist
	}
	if err != nil || !info.Mode().IsRegular() || (checkPermissions && info.Mode().Perm()&0o077 != 0) {
		return diagnosticStore{}, errors.New("diagnostic store is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return diagnosticStore{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, diagnosticMaxBytes+1))
	if err != nil || len(data) > diagnosticMaxBytes {
		return diagnosticStore{}, errors.New("diagnostic store is unavailable")
	}
	var raw struct {
		Version int               `json:"version"`
		Events  []json.RawMessage `json:"events"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raw); err != nil || raw.Version != diagnosticStoreVersion || len(raw.Events) > diagnosticMaxEvents*2 {
		return diagnosticStore{}, errors.New("diagnostic store is unavailable")
	}
	store := diagnosticStore{Version: diagnosticStoreVersion, Events: make([]diagnosticEvent, 0, len(raw.Events))}
	for _, item := range raw.Events {
		var event diagnosticEvent
		if json.Unmarshal(item, &event) == nil {
			if event, ok := validateDiagnosticEvent(event, now); ok {
				store.Events = append(store.Events, event)
			}
		}
	}
	if len(store.Events) > diagnosticMaxEvents {
		store.Events = store.Events[len(store.Events)-diagnosticMaxEvents:]
	}
	return store, nil
}

func validateDiagnosticEvent(event diagnosticEvent, now time.Time) (diagnosticEvent, bool) {
	at, err := time.Parse(time.RFC3339, event.Time)
	if err != nil || at.Before(now.Add(-diagnosticRetention)) || at.After(now.Add(time.Minute)) {
		return diagnosticEvent{}, false
	}
	if event.Provider != "codex" && event.Provider != "claude" && event.Provider != "helper" {
		return diagnosticEvent{}, false
	}
	if _, ok := diagnosticCategories[event.Category]; !ok {
		return diagnosticEvent{}, false
	}
	if event.HTTPStatus != 0 && (event.HTTPStatus < 100 || event.HTTPStatus > 599) {
		return diagnosticEvent{}, false
	}
	if event.CooldownSeconds < 0 || event.CooldownSeconds > int64(diagnosticMaxCooldown/time.Second) {
		return diagnosticEvent{}, false
	}
	if event.Version != "" && !validBuildVersion(event.Version) {
		return diagnosticEvent{}, false
	}
	if event.Revision != "" && !validBuildRevision(event.Revision) {
		return diagnosticEvent{}, false
	}
	event.Time = at.UTC().Format(time.RFC3339)
	return event, true
}

func sameDiagnosticTransition(left, right diagnosticEvent) bool {
	return left.Provider == right.Provider && left.Category == right.Category && left.HTTPStatus == right.HTTPStatus && left.CooldownSeconds == right.CooldownSeconds && left.Version == right.Version && left.Revision == right.Revision
}

func diagnosticTransitionAlreadyRecorded(events []diagnosticEvent, event diagnosticEvent) bool {
	for i := len(events) - 1; i >= 0; i-- {
		previous := events[i]
		if previous.Provider != event.Provider {
			continue
		}
		if event.Category == "backoff" {
			return sameDiagnosticTransition(previous, event)
		}
		if previous.Category == "backoff" {
			continue
		}
		return sameDiagnosticTransition(previous, event)
	}
	return false
}

func emitUsageRefreshDiagnostics(provider string, info usageRefreshInfo, operationErr, actionErr error, recovered bool) {
	if operationErr != nil {
		_ = recordDiagnostic(diagnosticEvent{Provider: provider, Category: "local_state"})
		return
	}
	if recovered {
		_ = recordDiagnostic(diagnosticEvent{Provider: provider, Category: "recovered"})
		return
	}
	if info.DiagnosticCategory == "" && actionErr != nil && !errors.Is(actionErr, errUsageRefreshCoolingDown) {
		info.DiagnosticCategory, info.DiagnosticHTTPStatus = classifyDiagnosticError(provider, actionErr)
	}
	if _, ok := diagnosticCategories[info.DiagnosticCategory]; !ok || info.DiagnosticCategory == "recovered" || info.DiagnosticCategory == "backoff" || info.DiagnosticCategory == "helper_failed" {
		if actionErr == nil && diagnosticProviderFailureActive(provider) {
			_ = recordDiagnostic(diagnosticEvent{Provider: provider, Category: "recovered"})
		}
		return
	}
	_ = recordDiagnostic(diagnosticEvent{Provider: provider, Category: info.DiagnosticCategory, HTTPStatus: info.DiagnosticHTTPStatus, CooldownSeconds: info.DiagnosticCooldownSeconds})
	if info.DiagnosticCooldownSeconds > 0 {
		_ = recordDiagnostic(diagnosticEvent{Provider: provider, Category: "backoff", HTTPStatus: info.DiagnosticHTTPStatus, CooldownSeconds: info.DiagnosticCooldownSeconds})
	}
}

func diagnosticProviderFailureActive(provider string) bool {
	store, err := loadDiagnosticStore(diagnosticsPath(), time.Now(), true)
	if err != nil {
		return false
	}
	for i := len(store.Events) - 1; i >= 0; i-- {
		if store.Events[i].Provider != provider || store.Events[i].Category == "backoff" {
			continue
		}
		return store.Events[i].Category != "recovered"
	}
	return false
}

func classifyDiagnosticError(provider string, err error) (string, int) {
	if errors.Is(err, errUsageRefreshState) {
		return "local_state", 0
	}
	message := err.Error()
	if provider == "claude" {
		switch {
		case strings.Contains(message, "auth failed (HTTP 401)"):
			return "authentication", 401
		case strings.Contains(message, "auth failed (HTTP 403)"):
			return "authentication", 403
		case strings.Contains(message, "credentials"), strings.Contains(message, "OAuth token"):
			return "authentication", 0
		case strings.Contains(message, "rate limited (HTTP 429)"):
			return "rate_limited", 429
		case strings.Contains(message, "unreachable"):
			return "network", 0
		case strings.Contains(message, "unsupported response"), strings.Contains(message, "returned no session"):
			return "invalid_response", 0
		case strings.HasPrefix(message, "Claude usage API returned HTTP "):
			status := 0
			_, _ = fmt.Sscanf(message, "Claude usage API returned HTTP %d", &status)
			if status >= 100 && status <= 599 {
				return "provider_unavailable", status
			}
		}
	}
	if strings.Contains(message, "response could not be read") {
		return "invalid_response", 0
	}
	if strings.Contains(message, "request was rejected") {
		return "request_rejected", 0
	}
	return "provider_unavailable", 0
}

func renderDiagnostics(path string, now time.Time) (string, error) {
	currentVersion, currentRevision := diagnosticBuildIdentity()
	header := "DankAIUsage diagnostics"
	if currentVersion != "" {
		header += " — build " + currentVersion
		if currentRevision != "" {
			header += " (" + currentRevision + ")"
		}
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return header + "\nNo diagnostics have been recorded.", nil
	} else if err != nil {
		return "", err
	}
	store, err := loadAndPruneDiagnosticStore(path, now)
	if err != nil {
		return "", err
	}
	if len(store.Events) == 0 {
		return header + "\nNo diagnostics have been recorded.", nil
	}
	var out strings.Builder
	out.WriteString(header + "\nRecent events:\n")
	for _, event := range store.Events {
		provider := map[string]string{"codex": "Codex", "claude": "Claude", "helper": "Helper"}[event.Provider]
		fmt.Fprintf(&out, "%s — %s: %s", event.Time, provider, diagnosticCategories[event.Category])
		if event.HTTPStatus != 0 {
			fmt.Fprintf(&out, " (HTTP %d)", event.HTTPStatus)
		}
		if event.CooldownSeconds != 0 {
			fmt.Fprintf(&out, " (cooldown %ds)", event.CooldownSeconds)
		}
		if event.Version != "" {
			out.WriteString(" [build " + event.Version)
			if event.Revision != "" {
				out.WriteString(" " + event.Revision)
			}
			out.WriteByte(']')
		}
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

func loadAndPruneDiagnosticStore(path string, now time.Time) (diagnosticStore, error) {
	var store diagnosticStore
	err := withDiagnosticLock(path, func() error {
		loaded, err := loadDiagnosticStore(path, now, true)
		if err != nil {
			return err
		}
		data, err := json.Marshal(loaded)
		if err != nil || len(data) > diagnosticMaxBytes {
			return errors.New("diagnostic store is unavailable")
		}
		if err := atomicWriteDiagnosticFile(path, data); err != nil {
			return err
		}
		store = loaded
		return nil
	})
	return store, err
}

func diagnosticBuildIdentity() (string, string) {
	v := version
	if !validBuildVersion(v) {
		v = ""
	}
	r := revision
	if r == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					r = setting.Value
				}
				if setting.Key == "vcs.modified" && setting.Value == "true" && r != "" {
					r += "-dirty"
				}
			}
		}
	}
	if !validBuildRevision(r) {
		r = ""
	}
	return v, r
}

func validBuildVersion(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	if value == "dev" {
		return true
	}
	base := strings.TrimSuffix(strings.TrimSuffix(value, "-dev"), "-dirty")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func validBuildRevision(value string) bool {
	if strings.HasPrefix(value, "source-") {
		return len(value) == 71 && isLowerHex(value[7:])
	}
	base := strings.TrimSuffix(value, "-dirty")
	return len(base) >= 7 && len(base) <= 64 && isHex(base)
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func isHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func withDiagnosticLock(path string, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("diagnostic state directory is unavailable")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return err
	}
	deadline := time.Now().Add(diagnosticLockTimeout)
	for {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		} else if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errors.New("diagnostic lock timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func atomicWriteDiagnosticFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".diagnostics-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck
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
	return os.Chmod(path, 0o600)
}
