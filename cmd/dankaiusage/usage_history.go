package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	usageHistoryStateVersion = 1
	usageHistoryMaxEvents    = 200
	usageHistoryMaxAge       = 30 * 24 * time.Hour
	usageHistoryLockTimeout  = 250 * time.Millisecond
)

type UsageHistoryValue struct {
	UsedPercent      *float64 `json:"usedPercent,omitempty"`
	ResetAt          string   `json:"resetAt,omitempty"`
	AvailableCredits *int     `json:"availableCredits,omitempty"`
}

type UsageHistoryEvent struct {
	ObservedAt         string             `json:"observedAt"`
	PreviousObservedAt string             `json:"previousObservedAt,omitempty"`
	Provider           string             `json:"provider"`
	Bucket             string             `json:"bucket,omitempty"`
	Label              string             `json:"label"`
	Kind               string             `json:"kind"`
	Source             string             `json:"source"`
	Confidence         string             `json:"confidence"`
	Before             *UsageHistoryValue `json:"before,omitempty"`
	After              *UsageHistoryValue `json:"after,omitempty"`
	Message            string             `json:"message"`
}

type usageHistoryObservation struct {
	ObservedAt  string  `json:"observedAt"`
	OrderAt     string  `json:"orderAt,omitempty"`
	Provider    string  `json:"provider"`
	Bucket      string  `json:"bucket"`
	Label       string  `json:"label"`
	Source      string  `json:"source"`
	UsedPercent float64 `json:"usedPercent"`
	ResetAt     string  `json:"resetAt,omitempty"`
}

type usageHistoryCreditObservation struct {
	ObservedAt       string   `json:"observedAt"`
	OrderAt          string   `json:"orderAt,omitempty"`
	AvailableCredits int      `json:"availableCredits"`
	ExpiresAt        []string `json:"expiresAt,omitempty"`
}

type usageHistoryState struct {
	Version      int                                      `json:"version"`
	Observations map[string]usageHistoryObservation       `json:"observations"`
	Credits      map[string]usageHistoryCreditObservation `json:"credits"`
	Events       []UsageHistoryEvent                      `json:"events"`
}

type codexResetHistoryRecord struct {
	ObservedAt       time.Time
	BeforeObservedAt time.Time
	Outcome          string
	Before           codexRateLimitsResult
	After            *codexRateLimitsResult
}

func usageHistoryPath() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "dankaiusage", "usage-history.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dankaiusage", "usage-history.json")
}

func defaultUsageHistoryState() usageHistoryState {
	return usageHistoryState{
		Version:      usageHistoryStateVersion,
		Observations: map[string]usageHistoryObservation{},
		Credits:      map[string]usageHistoryCreditObservation{},
		Events:       []UsageHistoryEvent{},
	}
}

func runUsageHistoryCommand(args []string) {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	result := struct {
		History      []UsageHistoryEvent `json:"history"`
		HistoryError string              `json:"historyError,omitempty"`
	}{History: []UsageHistoryEvent{}}
	failed := false
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		result.HistoryError = "invalid history arguments"
		failed = true
	} else if events, err := readUsageHistory(usageHistoryPath()); err != nil {
		result.HistoryError = formatUsageHistoryError(err)
		failed = true
	} else {
		result.History = events
	}
	var data []byte
	if *pretty {
		data, _ = json.MarshalIndent(result, "", "  ")
	} else {
		data, _ = json.Marshal(result)
	}
	fmt.Println(string(data))
	if failed {
		os.Exit(1)
	}
}

func observeUsageHistory(path string, now time.Time, providers []ProviderUsage) ([]UsageHistoryEvent, error) {
	var events []UsageHistoryEvent
	err := withUsageHistoryLock(path, func() error {
		state, err := loadUsageHistoryState(path)
		if err != nil {
			return err
		}
		pruneUsageHistory(&state, now)
		for _, provider := range providers {
			observeProviderHistory(&state, provider, now)
		}
		pruneUsageHistory(&state, now)
		if err := saveUsageHistoryState(path, state); err != nil {
			return errors.New("could not save usage history")
		}
		events = append([]UsageHistoryEvent{}, state.Events...)
		return nil
	})
	return events, err
}

func readUsageHistory(path string) ([]UsageHistoryEvent, error) {
	var events []UsageHistoryEvent
	err := withUsageHistoryLock(path, func() error {
		state, err := loadUsageHistoryState(path)
		if err != nil {
			return err
		}
		beforeEvents := len(state.Events)
		beforeObservations := len(state.Observations)
		beforeCredits := len(state.Credits)
		pruneUsageHistory(&state, time.Now())
		if len(state.Events) != beforeEvents || len(state.Observations) != beforeObservations || len(state.Credits) != beforeCredits {
			if err := saveUsageHistoryState(path, state); err != nil {
				return errors.New("could not save usage history")
			}
		}
		events = append([]UsageHistoryEvent{}, state.Events...)
		return nil
	})
	return events, err
}

func observeProviderHistory(state *usageHistoryState, provider ProviderUsage, now time.Time) {
	if !providerHistoryIsFresh(provider) {
		return
	}
	observedAt := now
	if !provider.historyObservedAt.IsZero() {
		observedAt = provider.historyObservedAt
	}

	var currentCredits usageHistoryCreditObservation
	var creditCountKnown bool
	redemptionLikely := false
	redemptionBefore := 0
	redemptionAfter := 0
	if provider.ID == "codex" {
		currentCredits, creditCountKnown = providerCreditObservation(provider, observedAt, now)
		if previous, ok := state.Credits[provider.ID]; ok && creditCountKnown {
			redemptionLikely = creditDropSupportsRedemption(previous, currentCredits, now) &&
				!hasConfirmedPluginResetBetween(state.Events, historyCreditOrderAt(previous), now)
			redemptionBefore = previous.AvailableCredits
			redemptionAfter = currentCredits.AvailableCredits
		}
	}

	redemptionEmitted := false
	for _, bucket := range provider.QuotaBuckets {
		if !historyEligibleBucket(provider, bucket) {
			continue
		}
		if observeQuotaBucket(state, provider.ID, bucket, observedAt, now, redemptionLikely, redemptionBefore, redemptionAfter) {
			redemptionEmitted = true
		}
	}
	if provider.ID == "codex" {
		if !creditCountKnown {
			return
		}
		observeCreditCount(state, provider.ID, currentCredits, !redemptionEmitted)
	}
}

func providerHistoryIsFresh(provider ProviderUsage) bool {
	if provider.ID == "" || !provider.Available || provider.Meta == nil || provider.Meta["limitError"] != nil {
		return false
	}
	if stale, _ := provider.Meta["usageDataStale"].(bool); stale {
		return false
	}
	if provider.ID == "claude" {
		return stringValue(provider.Meta["source"]) == claudeOAuthUsageSource
	}
	return provider.ID == "codex"
}

func historyEligibleBucket(provider ProviderUsage, bucket QuotaBucket) bool {
	if bucket.ID == "" || bucket.Kind == "credits" || !bucket.Allowance.Known || bucket.Allowance.Unit != "percent" {
		return false
	}
	switch provider.ID {
	case "codex":
		return bucket.Allowance.Source == "codex app-server"
	case "claude":
		return bucket.Allowance.Source == claudeOAuthUsageSource
	default:
		return false
	}
}

func observeQuotaBucket(state *usageHistoryState, provider string, bucket QuotaBucket, observedAt, validationNow time.Time, redemptionLikely bool, redemptionBefore, redemptionAfter int) bool {
	key := historyObservationKey(provider, bucket.ID)
	current := usageHistoryObservation{
		ObservedAt:  historyTimestamp(validationNow),
		OrderAt:     historyTimestamp(observedAt),
		Provider:    provider,
		Bucket:      bucket.ID,
		Label:       bucket.Label,
		Source:      bucket.Allowance.Source,
		UsedPercent: clampPercent(bucket.Allowance.PercentUsed),
		ResetAt:     canonicalHistoryTime(bucket.Allowance.ResetAt),
	}
	currentReset, resetErr := time.Parse(time.RFC3339, current.ResetAt)
	if resetErr != nil || !currentReset.After(validationNow) {
		return false
	}
	previous, ok := state.Observations[key]
	if !ok {
		state.Observations[key] = current
		return false
	}
	if previous.Source != current.Source {
		state.Observations[key] = current
		return false
	}
	if redemptionLikely && historyCreditOrderAt(state.Credits[provider]) != historyObservationOrderAt(previous) {
		redemptionLikely = false
	}
	previousAt, err := time.Parse(time.RFC3339, historyObservationOrderAt(previous))
	if err != nil || !observedAt.After(previousAt) {
		return false
	}

	usedDropped := current.UsedPercent < previous.UsedPercent-0.001
	resetChanged := current.ResetAt != "" && previous.ResetAt != "" && current.ResetAt != previous.ResetAt
	if resetChanged && previous.UsedPercent == 0 && current.UsedPercent == 0 {
		state.Observations[key] = current
		return false
	}
	previousReset, previousResetErr := time.Parse(time.RFC3339, previous.ResetAt)
	previousResetDue := previousResetErr == nil && !validationNow.Before(previousReset)
	redemptionEmitted := false
	if resetChanged && previousResetDue {
		state.Events = append(state.Events, quotaHistoryEvent(previous, current,
			"scheduled_window", "inferred", "Allowance was observed after its expected window reset."))
	} else if usedDropped {
		kind := "allowance_increased_unknown"
		confidence := "inferred"
		message := "Possible provider reset; manual reset or account change cannot be ruled out."
		if previousResetDue {
			kind = "scheduled_window"
			message = "Allowance was observed after its expected window reset."
		} else if provider == "codex" && strings.HasPrefix(bucket.ID, "general-") && redemptionLikely {
			kind = "reset_redeemed_inferred"
			message = "Likely earned reset redeemed; not a confirmed plugin action."
			redemptionEmitted = true
		}
		event := quotaHistoryEvent(previous, current, kind, confidence, message)
		if redemptionEmitted {
			event.Before.AvailableCredits = &redemptionBefore
			event.After.AvailableCredits = &redemptionAfter
		}
		state.Events = append(state.Events, event)
	} else if resetChanged {
		state.Events = append(state.Events, quotaHistoryEvent(previous, current,
			"window_changed_unknown", "observed", "Quota window timing changed; the cause is unknown."))
	}
	state.Observations[key] = current
	return redemptionEmitted
}

func quotaHistoryEvent(before, after usageHistoryObservation, kind, confidence, message string) UsageHistoryEvent {
	beforeUsed := before.UsedPercent
	afterUsed := after.UsedPercent
	return UsageHistoryEvent{
		ObservedAt:         after.ObservedAt,
		PreviousObservedAt: before.ObservedAt,
		Provider:           after.Provider,
		Bucket:             after.Bucket,
		Label:              firstNonEmpty(after.Label, before.Label, after.Bucket),
		Kind:               kind,
		Source:             "quota_observation",
		Confidence:         confidence,
		Before:             &UsageHistoryValue{UsedPercent: &beforeUsed, ResetAt: before.ResetAt},
		After:              &UsageHistoryValue{UsedPercent: &afterUsed, ResetAt: after.ResetAt},
		Message:            message,
	}
}

func observeCreditCount(state *usageHistoryState, provider string, current usageHistoryCreditObservation, emitChange bool) {
	previous, ok := state.Credits[provider]
	if !ok {
		state.Credits[provider] = current
		return
	}
	previousAt, err := time.Parse(time.RFC3339, historyCreditOrderAt(previous))
	currentAt, currentErr := time.Parse(time.RFC3339, historyCreditOrderAt(current))
	if err != nil || currentErr != nil || !currentAt.After(previousAt) {
		return
	}
	if emitChange && previous.AvailableCredits != current.AvailableCredits {
		beforeCount := previous.AvailableCredits
		afterCount := current.AvailableCredits
		state.Events = append(state.Events, UsageHistoryEvent{
			ObservedAt:         current.ObservedAt,
			PreviousObservedAt: previous.ObservedAt,
			Provider:           provider,
			Bucket:             "earned-resets",
			Label:              "Earned resets",
			Kind:               "credits_changed",
			Source:             "quota_observation",
			Confidence:         "observed",
			Before:             &UsageHistoryValue{AvailableCredits: &beforeCount},
			After:              &UsageHistoryValue{AvailableCredits: &afterCount},
			Message:            "Available earned reset count changed; this does not confirm an award or use.",
		})
	}
	state.Credits[provider] = current
}

func historyAvailableResetCount(meta map[string]any) (int, bool) {
	if meta == nil {
		return 0, false
	}
	switch value := meta["availableResetCount"].(type) {
	case int:
		return value, value >= 0
	case int64:
		return int(value), value >= 0
	case float64:
		return int(value), value >= 0 && value == float64(int(value))
	default:
		return 0, false
	}
}

func providerCreditObservation(provider ProviderUsage, orderAt, observedAt time.Time) (usageHistoryCreditObservation, bool) {
	count, ok := historyAvailableResetCount(provider.Meta)
	if !ok {
		return usageHistoryCreditObservation{}, false
	}
	observation := usageHistoryCreditObservation{
		ObservedAt:       historyTimestamp(observedAt),
		OrderAt:          historyTimestamp(orderAt),
		AvailableCredits: count,
	}
	if len(provider.Resets) != count {
		return observation, true
	}
	for _, reset := range provider.Resets {
		if reset.ResetType != "codexRateLimits" {
			return observation, true
		}
		expiresAt := canonicalHistoryTime(reset.ExpiresAt)
		if expiresAt == "" {
			return observation, true
		}
		observation.ExpiresAt = append(observation.ExpiresAt, expiresAt)
	}
	sort.Strings(observation.ExpiresAt)
	return observation, true
}

func historyObservationOrderAt(observation usageHistoryObservation) string {
	return firstNonEmpty(observation.OrderAt, observation.ObservedAt)
}

func historyCreditOrderAt(observation usageHistoryCreditObservation) string {
	return firstNonEmpty(observation.OrderAt, observation.ObservedAt)
}

func creditDropSupportsRedemption(previous, current usageHistoryCreditObservation, now time.Time) bool {
	if current.AvailableCredits >= previous.AvailableCredits || previous.AvailableCredits <= 0 || len(previous.ExpiresAt) != previous.AvailableCredits {
		return false
	}
	for _, value := range previous.ExpiresAt {
		expiresAt, err := time.Parse(time.RFC3339, value)
		if err != nil || !expiresAt.After(now) {
			return false
		}
	}
	return true
}

func hasConfirmedPluginResetBetween(events []UsageHistoryEvent, previousObservedAt string, now time.Time) bool {
	previous, err := time.Parse(time.RFC3339, previousObservedAt)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.Provider != "codex" || event.Source != "plugin_reset" || event.Confidence != "confirmed" ||
			(event.Kind != "plugin_reset_reset" && event.Kind != "plugin_reset_already_redeemed") {
			continue
		}
		observedAt, err := time.Parse(time.RFC3339, event.ObservedAt)
		if err == nil && observedAt.After(previous) && !observedAt.After(now) {
			return true
		}
	}
	return false
}

func recordCodexResetHistory(path string, record codexResetHistoryRecord) error {
	return withUsageHistoryLock(path, func() error {
		state, err := loadUsageHistoryState(path)
		if err != nil {
			return err
		}
		event := codexResetHistoryEvent(record)
		state.Events = append(state.Events, event)
		if record.After != nil && (record.Outcome == "reset" || record.Outcome == "alreadyRedeemed") {
			updateCodexObservationsFromLimits(&state, *record.After, record.ObservedAt)
		}
		pruneUsageHistory(&state, record.ObservedAt)
		if err := saveUsageHistoryState(path, state); err != nil {
			return errors.New("could not save usage history")
		}
		return nil
	})
}

func codexResetHistoryEvent(record codexResetHistoryRecord) UsageHistoryEvent {
	beforeBucket, beforeOK := generalCodexHistoryBucket(record.Before, record.ObservedAt)
	var afterBucket QuotaBucket
	afterOK := false
	if record.After != nil {
		afterBucket, afterOK = matchingCodexHistoryBucket(*record.After, beforeBucket, record.ObservedAt)
	}
	event := UsageHistoryEvent{
		ObservedAt:         historyTimestamp(record.ObservedAt),
		PreviousObservedAt: historyTimestamp(record.BeforeObservedAt),
		Provider:           "codex",
		Label:              "Codex allowance",
		Source:             "plugin_reset",
	}
	if beforeOK {
		event.Bucket = beforeBucket.ID
		event.Label = beforeBucket.Label
		event.Before = historyValueForBucket(beforeBucket)
	}
	if afterOK {
		event.After = historyValueForBucket(afterBucket)
	}
	switch record.Outcome {
	case "reset":
		event.Kind = "plugin_reset_reset"
		event.Confidence = "confirmed"
		event.Message = "The plugin received a confirmed reset outcome; other simultaneous quota changes may have separate causes."
	case "alreadyRedeemed":
		event.Kind = "plugin_reset_already_redeemed"
		event.Confidence = "confirmed"
		event.Message = "Codex confirmed that this reset was already redeemed; other quota changes may have separate causes."
	case "nothingToReset":
		event.Kind = "plugin_reset_nothing_to_reset"
		event.Confidence = "confirmed"
		event.Message = "Codex confirmed that no eligible allowance was reset."
	case "noCredit":
		event.Kind = "plugin_reset_no_credit"
		event.Confidence = "confirmed"
		event.Message = "Codex confirmed that no earned reset credit was available."
	default:
		event.Kind = "plugin_reset_attempt_unknown"
		event.Confidence = "observed"
		event.Message = "The plugin attempted a reset, but the outcome is unknown."
	}
	return event
}

func generalCodexHistoryBucket(limits codexRateLimitsResult, now time.Time) (QuotaBucket, bool) {
	snapshot, ok := generalCodexRateLimitSnapshot(limits)
	if !ok {
		return QuotaBucket{}, false
	}
	session, weekly := codexSnapshotWindowAllowances(snapshot, now)
	buckets := makeQuotaBuckets(session, weekly, nil, nil)
	if len(buckets) == 0 {
		return QuotaBucket{}, false
	}
	selected := buckets[0]
	for _, bucket := range buckets[1:] {
		if bucket.Allowance.PercentUsed > selected.Allowance.PercentUsed {
			selected = bucket
		}
	}
	return selected, true
}

func matchingCodexHistoryBucket(limits codexRateLimitsResult, before QuotaBucket, now time.Time) (QuotaBucket, bool) {
	snapshot, ok := generalCodexRateLimitSnapshot(limits)
	if !ok {
		return QuotaBucket{}, false
	}
	session, weekly := codexSnapshotWindowAllowances(snapshot, now)
	for _, bucket := range makeQuotaBuckets(session, weekly, nil, nil) {
		if before.ID == "" || bucket.ID == before.ID {
			return bucket, true
		}
	}
	return QuotaBucket{}, false
}

func updateCodexObservationsFromLimits(state *usageHistoryState, limits codexRateLimitsResult, now time.Time) {
	snapshot, ok := generalCodexRateLimitSnapshot(limits)
	if !ok {
		return
	}
	session, weekly := codexSnapshotWindowAllowances(snapshot, now)
	for _, bucket := range makeQuotaBuckets(session, weekly, nil, nil) {
		resetAt, err := time.Parse(time.RFC3339, bucket.Allowance.ResetAt)
		if err != nil || !resetAt.After(now) {
			continue
		}
		key := historyObservationKey("codex", bucket.ID)
		if previous, ok := state.Observations[key]; ok {
			previousAt, err := time.Parse(time.RFC3339, historyObservationOrderAt(previous))
			if err == nil && !now.After(previousAt) {
				continue
			}
		}
		state.Observations[key] = usageHistoryObservation{
			ObservedAt:  historyTimestamp(now),
			OrderAt:     historyTimestamp(now),
			Provider:    "codex",
			Bucket:      bucket.ID,
			Label:       bucket.Label,
			Source:      bucket.Allowance.Source,
			UsedPercent: bucket.Allowance.PercentUsed,
			ResetAt:     canonicalHistoryTime(bucket.Allowance.ResetAt),
		}
	}
	if count := limits.RateLimitResetCredits.AvailableCount; limits.RateLimitResetCredits.AvailableCountKnown && count >= 0 {
		if previous, ok := state.Credits["codex"]; ok {
			previousAt, err := time.Parse(time.RFC3339, historyCreditOrderAt(previous))
			if err == nil && !now.After(previousAt) {
				return
			}
		}
		state.Credits["codex"] = usageHistoryCreditObservation{ObservedAt: historyTimestamp(now), OrderAt: historyTimestamp(now), AvailableCredits: count}
	}
}

func historyValueForBucket(bucket QuotaBucket) *UsageHistoryValue {
	used := clampPercent(bucket.Allowance.PercentUsed)
	return &UsageHistoryValue{UsedPercent: &used, ResetAt: canonicalHistoryTime(bucket.Allowance.ResetAt)}
}

func historyObservationKey(provider, bucket string) string {
	return provider + "/" + bucket
}

func canonicalHistoryTime(value string) string {
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}

func historyTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func pruneUsageHistory(state *usageHistoryState, now time.Time) {
	cutoff := now.Add(-usageHistoryMaxAge)
	kept := state.Events[:0]
	for _, event := range state.Events {
		observedAt, err := time.Parse(time.RFC3339, event.ObservedAt)
		if err == nil && !observedAt.Before(cutoff) {
			kept = append(kept, event)
		}
	}
	state.Events = kept
	sort.SliceStable(state.Events, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339, state.Events[i].ObservedAt)
		right, rightErr := time.Parse(time.RFC3339, state.Events[j].ObservedAt)
		if leftErr == nil && rightErr == nil {
			return left.Before(right)
		}
		return state.Events[i].ObservedAt < state.Events[j].ObservedAt
	})
	if len(state.Events) > usageHistoryMaxEvents {
		state.Events = append([]UsageHistoryEvent(nil), state.Events[len(state.Events)-usageHistoryMaxEvents:]...)
	}
	for key, observation := range state.Observations {
		observedAt, err := time.Parse(time.RFC3339, observation.ObservedAt)
		if err != nil || observedAt.Before(cutoff) {
			delete(state.Observations, key)
		}
	}
	for provider, observation := range state.Credits {
		observedAt, err := time.Parse(time.RFC3339, observation.ObservedAt)
		if err != nil || observedAt.Before(cutoff) {
			delete(state.Credits, provider)
		}
	}
}

func withUsageHistoryLock(path string, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("could not create usage history directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return errors.New("could not protect usage history directory")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.New("could not open usage history lock")
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return errors.New("could not protect usage history lock")
	}
	deadline := time.Now().Add(usageHistoryLockTimeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return errors.New("could not lock usage history")
		}
		if !time.Now().Before(deadline) {
			return errors.New("timed out waiting for usage history lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func loadUsageHistoryState(path string) (usageHistoryState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultUsageHistoryState(), nil
	}
	if err != nil {
		return usageHistoryState{}, errors.New("could not read usage history")
	}
	state := defaultUsageHistoryState()
	if err := json.Unmarshal(data, &state); err != nil {
		return usageHistoryState{}, errors.New("saved usage history is unreadable")
	}
	if state.Version != usageHistoryStateVersion {
		return usageHistoryState{}, errors.New("saved usage history version is unsupported")
	}
	if state.Observations == nil {
		state.Observations = map[string]usageHistoryObservation{}
	}
	if state.Credits == nil {
		state.Credits = map[string]usageHistoryCreditObservation{}
	}
	if state.Events == nil {
		state.Events = []UsageHistoryEvent{}
	}
	return state, nil
}

func saveUsageHistoryState(path string, state usageHistoryState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".usage-history-*.tmp")
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
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func formatUsageHistoryError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if strings.Contains(message, "usage history") {
		return message
	}
	return fmt.Sprintf("usage history unavailable: %s", message)
}
