package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	usageRefreshMinInterval     = 3 * time.Minute
	usageRefreshDefaultInterval = 5 * time.Minute
	usageRefreshMaxInterval     = time.Hour
	usageRefreshLockTimeout     = 10 * time.Second
	usageRefreshCacheVersion    = 1
)

var errUsageRefreshCoolingDown = errors.New("usage refresh is cooling down")
var errUsageRefreshState = errors.New("usage refresh state is invalid")

type usageRefreshInfo struct {
	FetchedAt                 time.Time
	NextAttemptAt             time.Time
	Cached                    bool
	Stale                     bool
	Pending                   bool
	LastError                 string
	DiagnosticCategory        string
	DiagnosticHTTPStatus      int
	DiagnosticCooldownSeconds int64
}

type codexUsageCache struct {
	Version                   int                    `json:"version"`
	FetchedAt                 string                 `json:"fetchedAt,omitempty"`
	NextAttemptAt             string                 `json:"nextAttemptAt,omitempty"`
	LastError                 string                 `json:"lastError,omitempty"`
	DiagnosticCategory        string                 `json:"diagnosticCategory,omitempty"`
	DiagnosticHTTPStatus      int                    `json:"diagnosticHttpStatus,omitempty"`
	DiagnosticCooldownSeconds int64                  `json:"diagnosticCooldownSeconds,omitempty"`
	Invalidated               bool                   `json:"invalidated,omitempty"`
	Result                    *codexRateLimitsResult `json:"result,omitempty"`
}

func normalizeUsageRefreshInterval(interval time.Duration) time.Duration {
	if interval == 0 {
		return usageRefreshDefaultInterval
	}
	if interval < usageRefreshMinInterval {
		return usageRefreshMinInterval
	}
	if interval > usageRefreshMaxInterval {
		return usageRefreshMaxInterval
	}
	return interval
}

func usageRefreshIntervalSeconds(seconds int) time.Duration {
	if seconds == 0 {
		return usageRefreshDefaultInterval
	}
	if seconds < int(usageRefreshMinInterval/time.Second) {
		return usageRefreshMinInterval
	}
	if seconds > int(usageRefreshMaxInterval/time.Second) {
		return usageRefreshMaxInterval
	}
	return time.Duration(seconds) * time.Second
}

func codexUsageCachePath() string {
	return filepath.Join(pluginStateDir(), "codex-usage.json")
}

func collectCachedCodexRateLimits(path string, now time.Time, interval time.Duration, requireFresh bool, fetch func() (codexRateLimitsResult, error)) (codexRateLimitsResult, usageRefreshInfo, error) {
	interval = normalizeUsageRefreshInterval(interval)
	var result codexRateLimitsResult
	var info usageRefreshInfo
	var actionErr error
	var recovered bool
	err := withUsageRefreshLock(path, func() error {
		cache, err := loadCodexUsageCache(path)
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
		configuredNext := time.Time{}
		if !fetchedAt.IsZero() {
			configuredNext = fetchedAt.Add(interval)
		}
		if configuredNext.After(nextAttemptAt) {
			nextAttemptAt = configuredNext
		}
		info.FetchedAt = fetchedAt
		info.NextAttemptAt = nextAttemptAt
		info.LastError = cache.LastError
		info.DiagnosticCategory = cache.DiagnosticCategory
		info.DiagnosticHTTPStatus = cache.DiagnosticHTTPStatus
		info.DiagnosticCooldownSeconds = cache.DiagnosticCooldownSeconds
		if info.DiagnosticCategory == "" && cache.LastError != "" {
			info.DiagnosticCategory = "provider_unavailable"
		}
		if fetchedAt.After(now) || nextAttemptAt.After(now.Add(usageRefreshMaxInterval)) {
			return fmt.Errorf("%w: future timestamp", errUsageRefreshState)
		}
		cacheUsable := cache.Result != nil && !cache.Invalidated
		staleTTL := max(30*time.Minute, 2*interval)
		staleOK := cacheUsable && !fetchedAt.IsZero() && now.Sub(fetchedAt) >= 0 && now.Sub(fetchedAt) < staleTTL

		coolingDown := (!fetchedAt.IsZero() && now.Before(fetchedAt.Add(interval))) || (!nextAttemptAt.IsZero() && now.Before(nextAttemptAt))
		if coolingDown {
			if cacheUsable && (cache.LastError == "" || staleOK) && !requireFresh {
				result = *cache.Result
				info.Cached = true
				info.Stale = cache.LastError != ""
				return nil
			}
			info.Pending = cache.Invalidated
			actionErr = errUsageRefreshCoolingDown
			return nil
		}

		// Persist the reservation before contacting the provider. This makes the
		// cooldown durable even if the request succeeds but the final cache write
		// fails, and the provider is not called when the reservation cannot be
		// saved.
		cache.Version = usageRefreshCacheVersion
		cache.NextAttemptAt = now.Add(interval).UTC().Format(time.RFC3339Nano)
		if err := saveCodexUsageCache(path, cache); err != nil {
			return errors.New("could not reserve Codex usage refresh")
		}

		fresh, fetchErr := fetch()
		if fetchErr != nil {
			cache.LastError = "Codex usage refresh failed"
			cache.DiagnosticCategory, cache.DiagnosticHTTPStatus = classifyDiagnosticError("codex", fetchErr)
			cache.DiagnosticCooldownSeconds = int64(interval / time.Second)
			info.DiagnosticCategory = cache.DiagnosticCategory
			info.DiagnosticHTTPStatus = cache.DiagnosticHTTPStatus
			info.DiagnosticCooldownSeconds = cache.DiagnosticCooldownSeconds
			if err := saveCodexUsageCache(path, cache); err != nil {
				return errors.New("could not save Codex usage refresh failure")
			}
			if staleOK && !requireFresh {
				result = *cache.Result
				info.FetchedAt = fetchedAt
				info.NextAttemptAt = now.Add(interval)
				info.Cached = true
				info.Stale = true
				info.LastError = cache.LastError
				return nil
			}
			actionErr = fetchErr
			return nil
		}

		recovered = cache.LastError != "" || cache.DiagnosticCategory != ""
		cache.Result = &fresh
		cache.FetchedAt = now.UTC().Format(time.RFC3339Nano)
		cache.NextAttemptAt = now.Add(interval).UTC().Format(time.RFC3339Nano)
		cache.LastError = ""
		cache.DiagnosticCategory = ""
		cache.DiagnosticHTTPStatus = 0
		cache.DiagnosticCooldownSeconds = 0
		cache.Invalidated = false
		if err := saveCodexUsageCache(path, cache); err != nil {
			return errors.New("could not save refreshed Codex usage")
		}
		result = fresh
		info.FetchedAt = now
		info.NextAttemptAt = now.Add(interval)
		info.LastError = ""
		return nil
	})
	if err != nil {
		emitUsageRefreshDiagnostics("codex", info, err, nil, false)
		return codexRateLimitsResult{}, usageRefreshInfo{}, err
	}
	emitUsageRefreshDiagnostics("codex", info, nil, actionErr, recovered)
	return result, info, actionErr
}

func invalidateCodexUsageCache(path string, now time.Time, interval time.Duration) error {
	interval = normalizeUsageRefreshInterval(interval)
	return withUsageRefreshLock(path, func() error {
		cache, err := loadCodexUsageCache(path)
		if err != nil {
			return err
		}
		cache.Version = usageRefreshCacheVersion
		cache.Result = nil
		cache.Invalidated = true
		cache.LastError = ""
		fetchedAt, err := parseOptionalRefreshTime(cache.FetchedAt)
		if err != nil {
			return err
		}
		existingNext, err := parseOptionalRefreshTime(cache.NextAttemptAt)
		if err != nil {
			return err
		}
		next := now.Add(interval)
		if !fetchedAt.IsZero() && fetchedAt.Add(interval).After(now) {
			next = fetchedAt.Add(interval)
		}
		if existingNext.After(next) {
			next = existingNext
		}
		cache.NextAttemptAt = next.UTC().Format(time.RFC3339Nano)
		return saveCodexUsageCache(path, cache)
	})
}

func parseOptionalRefreshTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid timestamp", errUsageRefreshState)
	}
	return parsed, nil
}

func usageRefreshMeta(info usageRefreshInfo) map[string]any {
	meta := map[string]any{}
	mergeUsageRefreshMeta(meta, info)
	return meta
}

func mergeUsageRefreshMeta(meta map[string]any, info usageRefreshInfo) {
	if info.Cached {
		meta["usageCached"] = true
	}
	if info.Stale {
		meta["usageStale"] = true
		meta["usageDataStale"] = true
	}
	if info.Pending {
		meta["usageRefreshPending"] = true
	}
	if !info.FetchedAt.IsZero() {
		meta["usageFetchedAt"] = info.FetchedAt.UTC().Format(time.RFC3339)
	}
	if !info.NextAttemptAt.IsZero() {
		meta["usageNextRefreshAt"] = info.NextAttemptAt.UTC().Format(time.RFC3339)
	}
	if info.LastError != "" {
		meta["usageRefreshError"] = info.LastError
	}
}

func loadCodexUsageCache(path string) (codexUsageCache, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return codexUsageCache{Version: usageRefreshCacheVersion}, nil
	}
	if err != nil {
		return codexUsageCache{}, err
	}
	var cache codexUsageCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return codexUsageCache{}, fmt.Errorf("%w: Codex cache unreadable", errUsageRefreshState)
	}
	if cache.Version != usageRefreshCacheVersion {
		return codexUsageCache{}, fmt.Errorf("%w: Codex cache version unsupported", errUsageRefreshState)
	}
	return cache, nil
}

func saveCodexUsageCache(path string, cache codexUsageCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return atomicWriteUsageRefreshFile(path, data)
}

func withUsageRefreshLock(path string, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("could not create usage refresh state directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return errors.New("could not protect usage refresh state directory")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.New("could not open usage refresh lock")
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return errors.New("could not protect usage refresh lock")
	}
	deadline := time.Now().Add(usageRefreshLockTimeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return errors.New("could not lock usage refresh state")
		}
		if !time.Now().Before(deadline) {
			return errors.New("timed out waiting for usage refresh lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func atomicWriteUsageRefreshFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".usage-refresh-*.tmp")
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
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync usage refresh state directory: %w", err)
	}
	return nil
}
