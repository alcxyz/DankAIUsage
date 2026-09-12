package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "dankaiusage-test-state-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_STATE_HOME", root)
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func TestDiagnosticStoreBoundsPermissionsAndDedupe(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "nested", "diagnostics.json")
	for i := 0; i < diagnosticMaxEvents+20; i++ {
		event := diagnosticEvent{
			Time:            now.Add(-time.Duration(diagnosticMaxEvents+20-i) * time.Second).Format(time.RFC3339),
			Provider:        "codex",
			Category:        "provider_unavailable",
			CooldownSeconds: int64(i + 1),
			Version:         "0.8.0",
			Revision:        "abcdef0",
		}
		if err := appendDiagnosticEvent(path, now, event); err != nil {
			t.Fatal(err)
		}
	}
	store, err := loadDiagnosticStore(path, now, true)
	if err != nil || len(store.Events) != diagnosticMaxEvents {
		t.Fatalf("events=%d error=%v", len(store.Events), err)
	}
	last := store.Events[len(store.Events)-1]
	if err := appendDiagnosticEvent(path, now, last); err != nil {
		t.Fatal(err)
	}
	store, _ = loadDiagnosticStore(path, now, true)
	if len(store.Events) != diagnosticMaxEvents {
		t.Fatal("duplicate transition was retained")
	}
	for _, check := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Dir(path), 0o700},
		{path, 0o600},
		{path + ".lock", 0o600},
	} {
		info, statErr := os.Stat(check.path)
		if statErr != nil || info.Mode().Perm() != check.mode {
			t.Fatalf("%s mode=%v error=%v", check.path, info.Mode().Perm(), statErr)
		}
	}
}

func TestDiagnosticStorePrunesExpiredOnWrite(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "diagnostics.json")
	data := fmt.Sprintf(`{"version":1,"events":[{"time":%q,"provider":"codex","category":"network"}]}`, now.Add(-diagnosticRetention-time.Second).Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendDiagnosticEvent(path, now, diagnosticEvent{Time: now.Format(time.RFC3339), Provider: "claude", Category: "recovered"}); err != nil {
		t.Fatal(err)
	}
	store, err := loadDiagnosticStore(path, now, true)
	if err != nil || len(store.Events) != 1 || store.Events[0].Provider != "claude" {
		t.Fatalf("store=%+v error=%v", store, err)
	}
}

func TestDiagnosticReportPrunesExpiredOnDisk(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "diagnostics.json")
	expired := now.Add(-diagnosticRetention - time.Second).Format(time.RFC3339)
	data := fmt.Sprintf(`{"version":1,"events":[{"time":%q,"provider":"codex","category":"network"}]}`, expired)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := renderDiagnostics(path, now)
	if err != nil || !strings.Contains(report, "No diagnostics") {
		t.Fatalf("report=%q error=%v", report, err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(onDisk), expired) {
		t.Fatalf("expired event remained on disk: %s error=%v", onDisk, err)
	}
}

func TestDiagnosticReportRejectsPoisonedFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "diagnostics.json")
	poison := "TOKEN=/home/alice/account@example.test"
	data := fmt.Sprintf(`{"version":1,"events":[`+
		`{"time":%q,"provider":"claude","category":"authentication","httpStatus":401,"version":"0.8.0","revision":"abcdef0","note":%q},`+
		`{"time":%q,"provider":%q,"category":"network"},`+
		`{"time":%q,"provider":"codex","category":%q}`+
		`]}`, now.Format(time.RFC3339), poison, now.Format(time.RFC3339), poison, now.Format(time.RFC3339), poison)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := renderDiagnostics(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, poison) || strings.Contains(report, "/home/") || strings.Count(report, "Authentication failed") != 1 {
		t.Fatalf("unsafe report: %q", report)
	}
}

func TestDiagnosticStoreRejectsOversizeAndUnsafePermissions(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"oversize", make([]byte, diagnosticMaxBytes+1), 0o600},
		{"public", []byte(`{"version":1,"events":[]}`), 0o644},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "diagnostics.json")
			if err := os.WriteFile(path, test.data, test.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := renderDiagnostics(path, now); err == nil {
				t.Fatal("unsafe store was accepted")
			}
		})
	}
}

func TestDiagnosticStatePathUsesOnlyAbsoluteXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "relative-state")
	want := filepath.Join(home, ".local", "state", "dankaiusage", "diagnostics.json")
	if got := diagnosticsPath(); got != want {
		t.Fatalf("relative XDG path=%q want=%q", got, want)
	}
	absolute := t.TempDir()
	t.Setenv("XDG_STATE_HOME", absolute)
	if got := diagnosticsPath(); got != filepath.Join(absolute, "dankaiusage", "diagnostics.json") {
		t.Fatalf("absolute XDG path=%q", got)
	}
}

func TestDiagnosticRecoveryAndBackoffTransitionsDedupe(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "diagnostics.json")
	events := []diagnosticEvent{
		{Time: now.Format(time.RFC3339), Provider: "claude", Category: "rate_limited", HTTPStatus: 429, CooldownSeconds: 900},
		{Time: now.Format(time.RFC3339), Provider: "claude", Category: "backoff", HTTPStatus: 429, CooldownSeconds: 900},
		{Time: now.Format(time.RFC3339), Provider: "claude", Category: "rate_limited", HTTPStatus: 429, CooldownSeconds: 900},
		{Time: now.Format(time.RFC3339), Provider: "claude", Category: "backoff", HTTPStatus: 429, CooldownSeconds: 900},
		{Time: now.Format(time.RFC3339), Provider: "codex", Category: "network"},
		{Time: now.Format(time.RFC3339), Provider: "claude", Category: "recovered"},
		{Time: now.Format(time.RFC3339), Provider: "claude", Category: "recovered"},
	}
	for _, event := range events {
		if err := appendDiagnosticEvent(path, now, event); err != nil {
			t.Fatal(err)
		}
	}
	store, err := loadDiagnosticStore(path, now, true)
	if err != nil || len(store.Events) != 4 {
		t.Fatalf("events=%+v error=%v", store.Events, err)
	}
}

func TestDiagnosticBuildIdentityValidation(t *testing.T) {
	for _, valid := range []string{"dev", "0.8.0", "10.20.300", "1.2.3-dev", "1.2.3-dirty"} {
		if !validBuildVersion(valid) {
			t.Errorf("valid version rejected: %q", valid)
		}
	}
	for _, invalid := range []string{"sk-tokensecret", "1.2", "01.2.3", "1.2.3/secret", "v1.2.3"} {
		if validBuildVersion(invalid) {
			t.Errorf("unsafe version accepted: %q", invalid)
		}
	}
	if !validBuildRevision("source-"+strings.Repeat("a", 64)) || !validBuildRevision("abcdef0-dirty") || validBuildRevision("source-"+strings.Repeat("A", 64)) || validBuildRevision("token-secret") {
		t.Fatal("revision validation mismatch")
	}
}

func TestCodexRefreshDiagnosticsFailureCacheAndRecovery(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	now := time.Now().UTC().Truncate(time.Second)
	refreshPath := filepath.Join(state, "dankaiusage", "codex-usage.json")
	malicious := errors.New("TOKEN=/home/alice/account@example.test")
	if _, _, err := collectCachedCodexRateLimits(refreshPath, now, 3*time.Minute, false, func() (codexRateLimitsResult, error) {
		return codexRateLimitsResult{}, malicious
	}); !errors.Is(err, malicious) {
		t.Fatalf("failure=%v", err)
	}
	if _, _, err := collectCachedCodexRateLimits(refreshPath, now.Add(time.Minute), 3*time.Minute, false, func() (codexRateLimitsResult, error) {
		return usageRefreshTestResult("unexpected"), nil
	}); !errors.Is(err, errUsageRefreshCoolingDown) {
		t.Fatalf("cached failure=%v", err)
	}
	if _, _, err := collectCachedCodexRateLimits(refreshPath, now.Add(3*time.Minute), 3*time.Minute, false, func() (codexRateLimitsResult, error) {
		return usageRefreshTestResult("recovered"), nil
	}); err != nil {
		t.Fatal(err)
	}
	store, err := loadDiagnosticStore(diagnosticsPath(), time.Now(), true)
	if err != nil || len(store.Events) != 3 {
		t.Fatalf("events=%+v error=%v", store.Events, err)
	}
	if store.Events[0].Category != "provider_unavailable" || store.Events[1].Category != "backoff" || store.Events[2].Category != "recovered" {
		t.Fatalf("transitions=%+v", store.Events)
	}
	report, err := renderDiagnostics(diagnosticsPath(), time.Now())
	if err != nil || strings.Contains(report, malicious.Error()) || strings.Contains(report, "/home/") {
		t.Fatalf("unsafe report=%q error=%v", report, err)
	}
}

func TestDiagnosticLockRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "state")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	err := appendDiagnosticEvent(filepath.Join(link, "diagnostics.json"), now, diagnosticEvent{Time: now.UTC().Format(time.RFC3339), Provider: "helper", Category: "helper_failed"})
	if err == nil {
		t.Fatal("symlinked diagnostic directory was accepted")
	}
	info, statErr := os.Stat(target)
	if statErr != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target was modified: mode=%v error=%v", info.Mode().Perm(), statErr)
	}
}
