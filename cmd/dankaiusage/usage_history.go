package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	usageHistoryLegacyVersion   = 1
	usageHistoryStateVersion    = 2
	usageHistoryMaxEvents       = 200
	usageHistoryMaxAge          = 30 * 24 * time.Hour
	usageHistoryLockTimeout     = 250 * time.Millisecond
	usageHistoryExplainMax      = 4 * 1024
	usageHistoryNoteMaxRunes    = 280
	usageHistoryTimingTolerance = 5 * time.Second
)

var usageHistoryExplanationReasons = []string{
	"subscription_change",
	"external_reset",
	"account_change",
	"provider_bonus",
	"unknown",
	"dismissed",
}

type UsageHistoryValue struct {
	UsedPercent      *float64 `json:"usedPercent,omitempty"`
	ResetAt          string   `json:"resetAt,omitempty"`
	AvailableCredits *int     `json:"availableCredits,omitempty"`
}

type UsageHistoryExplanation struct {
	Reason    string `json:"reason"`
	Note      string `json:"note,omitempty"`
	UpdatedAt string `json:"updatedAt"`
	Source    string `json:"source"`
}

type UsageHistoryEvent struct {
	ObservedAt         string                   `json:"observedAt"`
	PreviousObservedAt string                   `json:"previousObservedAt,omitempty"`
	Provider           string                   `json:"provider"`
	Bucket             string                   `json:"bucket,omitempty"`
	Label              string                   `json:"label"`
	Kind               string                   `json:"kind"`
	Source             string                   `json:"source"`
	Confidence         string                   `json:"confidence"`
	Before             *UsageHistoryValue       `json:"before,omitempty"`
	After              *UsageHistoryValue       `json:"after,omitempty"`
	Message            string                   `json:"message"`
	GroupID            string                   `json:"groupId,omitempty"`
	Explainable        bool                     `json:"explainable"`
	ExplanationChoices []string                 `json:"explanationChoices"`
	Explanation        *UsageHistoryExplanation `json:"explanation,omitempty"`
	ExpiryExplained    bool                     `json:"expiryExplained,omitempty"`
	TimingNoise        bool                     `json:"timingNoise,omitempty"`
}

type usageHistoryExplainRequest struct {
	GroupID string  `json:"groupId"`
	Reason  string  `json:"reason"`
	Note    *string `json:"note,omitempty"`
}

type usageHistoryResult struct {
	History      []UsageHistoryEvent `json:"history"`
	HistoryError string              `json:"historyError,omitempty"`
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
	return filepath.Join(pluginStateDir(), "usage-history.json")
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
	if len(args) > 0 && args[0] == "explain" {
		runUsageHistoryExplainCommand(args[1:])
		return
	}
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	result := usageHistoryResult{History: []UsageHistoryEvent{}}
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

func runUsageHistoryExplainCommand(args []string) {
	result := usageHistoryResult{History: []UsageHistoryEvent{}}
	failed := false
	if len(args) != 0 {
		result.HistoryError = "invalid history explanation arguments"
		failed = true
	} else if request, err := readUsageHistoryExplainRequest(os.Stdin); err != nil {
		result.HistoryError = "invalid history explanation request"
		failed = true
	} else if events, err := explainUsageHistory(usageHistoryPath(), time.Now(), request); err != nil {
		result.HistoryError = formatUsageHistoryError(err)
		failed = true
	} else {
		result.History = events
	}
	data, _ := json.Marshal(result)
	fmt.Println(string(data))
	if failed {
		os.Exit(1)
	}
}

func readUsageHistoryExplainRequest(input io.Reader) (usageHistoryExplainRequest, error) {
	reader := bufio.NewReaderSize(input, usageHistoryExplainMax+2)
	data, err := reader.ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return usageHistoryExplainRequest{}, errors.New("invalid explanation request")
	}
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
	}
	if len(data) == 0 || len(data) > usageHistoryExplainMax {
		return usageHistoryExplainRequest{}, errors.New("invalid explanation request")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var request usageHistoryExplainRequest
	if err := decoder.Decode(&request); err != nil {
		return usageHistoryExplainRequest{}, errors.New("invalid explanation request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return usageHistoryExplainRequest{}, errors.New("invalid explanation request")
	}
	request, err = normalizeUsageHistoryExplainRequest(request)
	if err != nil {
		return usageHistoryExplainRequest{}, err
	}
	return request, nil
}

func normalizeUsageHistoryExplainRequest(request usageHistoryExplainRequest) (usageHistoryExplainRequest, error) {
	if request.GroupID == "" || request.Reason == "" || !slices.Contains(usageHistoryExplanationReasons, request.Reason) {
		return usageHistoryExplainRequest{}, errors.New("invalid explanation request")
	}
	if request.Note == nil {
		return request, nil
	}
	note := strings.TrimSpace(*request.Note)
	if utf8.RuneCountInString(note) > usageHistoryNoteMaxRunes {
		return usageHistoryExplainRequest{}, errors.New("invalid explanation request")
	}
	if note == "" {
		request.Note = nil
	} else {
		request.Note = &note
	}
	return request, nil
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
		if err := decorateUsageHistoryEvents(state.Events); err != nil {
			return err
		}
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

func explainUsageHistory(path string, now time.Time, request usageHistoryExplainRequest) ([]UsageHistoryEvent, error) {
	request, err := normalizeUsageHistoryExplainRequest(request)
	if err != nil {
		return nil, err
	}
	var events []UsageHistoryEvent
	err = withUsageHistoryLock(path, func() error {
		state, err := loadUsageHistoryState(path)
		if err != nil {
			return err
		}
		pruneUsageHistory(&state, now)
		groupIndexes := make([]int, 0)
		allowed := false
		for index := range state.Events {
			event := &state.Events[index]
			if !event.Explainable || event.GroupID != request.GroupID {
				continue
			}
			groupIndexes = append(groupIndexes, index)
			if slices.Contains(event.ExplanationChoices, request.Reason) {
				allowed = true
			}
		}
		if len(groupIndexes) == 0 {
			return errors.New("usage history explanation group is unavailable")
		}
		if !allowed {
			return errors.New("usage history explanation reason is not supported")
		}
		explanation := &UsageHistoryExplanation{
			Reason:    request.Reason,
			UpdatedAt: historyTimestamp(now),
			Source:    "user",
		}
		if request.Note != nil {
			explanation.Note = *request.Note
		}
		for _, index := range groupIndexes {
			copy := *explanation
			state.Events[index].Explanation = &copy
		}
		if err := saveUsageHistoryState(path, state); err != nil {
			return errors.New("could not save usage history")
		}
		events = append([]UsageHistoryEvent{}, state.Events...)
		return nil
	})
	return events, err
}

func decorateUsageHistoryEvents(events []UsageHistoryEvent) error {
	groups := make(map[string][]int)
	for index := range events {
		event := &events[index]
		// Derived presentation metadata, not a rewrite of the original facts or
		// user explanation. Recompute it when loading older retained events too.
		event.TimingNoise = usageHistoryTimingNoise(*event)
		choices := usageHistoryChoices(*event)
		if len(choices) == 0 {
			if event.GroupID != "" || event.Explanation != nil {
				return errors.New("saved usage history is unreadable")
			}
			event.GroupID = ""
			event.Explainable = false
			event.ExplanationChoices = []string{}
			continue
		}
		if event.Provider == "" || event.ObservedAt == "" {
			return errors.New("saved usage history is unreadable")
		}
		groupID := usageHistoryGroupID(event.Provider, event.ObservedAt)
		if event.GroupID != "" && event.GroupID != groupID {
			return errors.New("saved usage history is unreadable")
		}
		event.GroupID = groupID
		event.Explainable = true
		event.ExplanationChoices = choices
		groups[groupID] = append(groups[groupID], index)
	}
	for _, indexes := range groups {
		var expected *UsageHistoryExplanation
		for _, index := range indexes {
			explanation := events[index].Explanation
			if explanation == nil {
				if expected != nil {
					return errors.New("saved usage history is unreadable")
				}
				continue
			}
			if !validUsageHistoryExplanation(*explanation) {
				return errors.New("saved usage history is unreadable")
			}
			if expected == nil {
				copy := *explanation
				expected = &copy
				continue
			}
			if *explanation != *expected {
				return errors.New("saved usage history is unreadable")
			}
		}
		if expected != nil {
			for _, index := range indexes {
				if events[index].Explanation == nil {
					return errors.New("saved usage history is unreadable")
				}
			}
		}
	}
	return nil
}

func usageHistoryGroupID(provider, observedAt string) string {
	digest := sha256.Sum256([]byte(provider + "\x00" + observedAt))
	return fmt.Sprintf("%x", digest)
}

func usageHistoryTimingNoise(event UsageHistoryEvent) bool {
	if event.Kind != "window_changed_unknown" || event.Before == nil || event.After == nil ||
		event.Before.UsedPercent == nil || event.After.UsedPercent == nil {
		return false
	}
	before, after := *event.Before.UsedPercent, *event.After.UsedPercent
	if math.IsNaN(before) || math.IsNaN(after) || math.IsInf(before, 0) || math.IsInf(after, 0) ||
		after < before-0.001 {
		return false
	}
	previousReset, beforeErr := time.Parse(time.RFC3339Nano, event.Before.ResetAt)
	currentReset, afterErr := time.Parse(time.RFC3339Nano, event.After.ResetAt)
	if beforeErr != nil || afterErr != nil {
		return false
	}
	delta := currentReset.Sub(previousReset)
	return delta != 0 && delta >= -usageHistoryTimingTolerance && delta <= usageHistoryTimingTolerance
}

func usageHistoryChoices(event UsageHistoryEvent) []string {
	if event.ExpiryExplained || event.Source == "plugin_reset" || strings.HasPrefix(event.Kind, "plugin_") {
		return nil
	}
	switch event.Kind {
	case "allowance_increased_unknown", "window_changed_unknown", "credits_changed", "reset_redeemed_inferred":
	default:
		return nil
	}
	refillOrSchedule := usageHistoryRefillOrSchedule(event)
	creditIncrease, creditDecrease := usageHistoryCreditDirection(event)
	choices := make([]string, 0, len(usageHistoryExplanationReasons))
	for _, reason := range usageHistoryExplanationReasons {
		switch reason {
		case "external_reset":
			if event.Provider != "codex" || (!refillOrSchedule && !creditDecrease) {
				continue
			}
		case "provider_bonus":
			if !refillOrSchedule && !creditIncrease {
				continue
			}
		}
		choices = append(choices, reason)
	}
	return choices
}

func usageHistoryRefillOrSchedule(event UsageHistoryEvent) bool {
	if event.Kind == "allowance_increased_unknown" || event.Kind == "window_changed_unknown" || event.Kind == "reset_redeemed_inferred" {
		return true
	}
	if event.Before == nil || event.After == nil {
		return false
	}
	if event.Before.UsedPercent != nil && event.After.UsedPercent != nil && *event.After.UsedPercent < *event.Before.UsedPercent {
		return true
	}
	return event.Before.ResetAt != "" && event.After.ResetAt != "" && event.Before.ResetAt != event.After.ResetAt
}

func usageHistoryCreditDirection(event UsageHistoryEvent) (increase, decrease bool) {
	if event.Before == nil || event.After == nil || event.Before.AvailableCredits == nil || event.After.AvailableCredits == nil {
		return false, false
	}
	return *event.After.AvailableCredits > *event.Before.AvailableCredits,
		*event.After.AvailableCredits < *event.Before.AvailableCredits
}

func validUsageHistoryExplanation(explanation UsageHistoryExplanation) bool {
	if explanation.Source != "user" || !slices.Contains(usageHistoryExplanationReasons, explanation.Reason) || strings.TrimSpace(explanation.Note) != explanation.Note || utf8.RuneCountInString(explanation.Note) > usageHistoryNoteMaxRunes {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339, explanation.UpdatedAt)
	return err == nil && !updatedAt.IsZero()
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
		observeCreditCount(state, provider.ID, currentCredits, now, !redemptionEmitted)
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
	previousReset, previousResetErr := time.Parse(time.RFC3339, previous.ResetAt)
	resetDelta := currentReset.Sub(previousReset)
	resetChanged := previousResetErr == nil && (resetDelta < -usageHistoryTimingTolerance || resetDelta > usageHistoryTimingTolerance)
	if resetChanged && previous.UsedPercent == 0 && current.UsedPercent == 0 {
		state.Observations[key] = current
		return false
	}
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

func observeCreditCount(state *usageHistoryState, provider string, current usageHistoryCreditObservation, validationNow time.Time, emitChange bool) {
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
			ExpiryExplained:    creditChangeIsExplainedByExpiry(previous, current, validationNow),
		})
	}
	state.Credits[provider] = current
}

func creditChangeIsExplainedByExpiry(previous, current usageHistoryCreditObservation, now time.Time) bool {
	if current.AvailableCredits >= previous.AvailableCredits ||
		len(previous.ExpiresAt) != previous.AvailableCredits || len(current.ExpiresAt) != current.AvailableCredits {
		return false
	}
	future := make([]string, 0, len(previous.ExpiresAt))
	expired := 0
	for _, value := range previous.ExpiresAt {
		expiresAt, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return false
		}
		if expiresAt.After(now) {
			future = append(future, value)
		} else {
			expired++
		}
	}
	sort.Strings(future)
	currentExpiries := append([]string(nil), current.ExpiresAt...)
	sort.Strings(currentExpiries)
	return expired == previous.AvailableCredits-current.AvailableCredits && slices.Equal(future, currentExpiries)
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
		if err := decorateUsageHistoryEvents(state.Events); err != nil {
			return err
		}
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
	var state usageHistoryState
	if err := json.Unmarshal(data, &state); err != nil {
		return usageHistoryState{}, errors.New("saved usage history is unreadable")
	}
	if state.Version != usageHistoryLegacyVersion && state.Version != usageHistoryStateVersion {
		return usageHistoryState{}, errors.New("saved usage history version is unsupported")
	}
	state.Version = usageHistoryStateVersion
	if state.Observations == nil {
		state.Observations = map[string]usageHistoryObservation{}
	}
	if state.Credits == nil {
		state.Credits = map[string]usageHistoryCreditObservation{}
	}
	if state.Events == nil {
		state.Events = []UsageHistoryEvent{}
	}
	if err := decorateUsageHistoryEvents(state.Events); err != nil {
		return usageHistoryState{}, err
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
