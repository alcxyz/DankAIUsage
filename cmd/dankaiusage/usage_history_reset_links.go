package main

import (
	"math"
	"strings"
	"time"
)

// Link observations, not causes: the original facts and explanations remain
// intact. Recomputing also gives retained history the same presentation.
func linkPluginResetObservations(events []UsageHistoryEvent) {
	for i := range events {
		events[i].PluginResetAt = ""
	}
	type link struct{ refill, credit, action int }
	var links []link
	actionUses := make(map[int]int)
	for i, refill := range events {
		if refill.Provider != "codex" || refill.Source != "quota_observation" ||
			(refill.Kind != "allowance_increased_unknown" && refill.Kind != "reset_redeemed_inferred") ||
			!strings.HasPrefix(refill.Bucket, "general-") || !historyHasRefill(refill) {
			continue
		}
		previous, previousErr := time.Parse(time.RFC3339Nano, refill.PreviousObservedAt)
		observed, observedErr := time.Parse(time.RFC3339Nano, refill.ObservedAt)
		sampled := observed
		if refill.SampledAt != "" {
			var err error
			sampled, err = time.Parse(time.RFC3339Nano, refill.SampledAt)
			if err != nil {
				continue
			}
		}
		beforeReset, beforeErr := time.Parse(time.RFC3339Nano, refill.Before.ResetAt)
		afterReset, afterErr := time.Parse(time.RFC3339Nano, refill.After.ResetAt)
		if previousErr != nil || observedErr != nil || beforeErr != nil || afterErr != nil ||
			!sampled.After(previous) || sampled.After(observed) || observed.Sub(previous) > time.Hour ||
			!beforeReset.After(observed) || !afterReset.After(observed) {
			continue
		}
		// More than one successful action in the observation interval is ambiguous.
		actionIndex := -1
		for j, action := range events {
			if action.Provider != "codex" || action.Source != "plugin_reset" ||
				(action.Kind != "plugin_reset_reset" && action.Kind != "plugin_reset_already_redeemed") ||
				action.Confidence != "confirmed" {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, action.ObservedAt)
			if err != nil || !at.After(previous) || at.After(observed) {
				continue
			}
			if actionIndex != -1 {
				actionIndex = -2
				break
			}
			actionIndex = j
		}
		if actionIndex < 0 {
			continue
		}
		action := events[actionIndex]
		actionAt, _ := time.Parse(time.RFC3339Nano, action.ObservedAt)
		if action.Kind != "plugin_reset_reset" || actionAt.After(sampled) || action.Bucket != refill.Bucket || action.Before == nil ||
			!historyValidUsed(action.Before.UsedPercent) || *action.Before.UsedPercent < *refill.Before.UsedPercent-0.001 {
			continue
		}
		actionReset, err := time.Parse(time.RFC3339Nano, action.Before.ResetAt)
		if err != nil || actionReset.Sub(beforeReset) < -usageHistoryTimingTolerance ||
			actionReset.Sub(beforeReset) > usageHistoryTimingTolerance {
			continue
		}
		creditIndex := -1
		for j, credit := range events {
			if credit.Provider != "codex" || credit.Source != "quota_observation" || credit.Kind != "credits_changed" ||
				credit.ObservedAt != refill.ObservedAt || credit.PreviousObservedAt != refill.PreviousObservedAt ||
				credit.SampledAt != refill.SampledAt || credit.Before == nil || credit.After == nil ||
				credit.Before.AvailableCredits == nil || credit.After.AvailableCredits == nil ||
				*credit.After.AvailableCredits < 0 || *credit.Before.AvailableCredits-*credit.After.AvailableCredits != 1 {
				continue
			}
			if creditIndex != -1 {
				creditIndex = -2
				break
			}
			creditIndex = j
		}
		if creditIndex < 0 {
			continue
		}
		links = append(links, link{i, creditIndex, actionIndex})
		actionUses[actionIndex]++
	}
	for _, candidate := range links {
		if actionUses[candidate.action] != 1 {
			continue
		}
		at := events[candidate.action].ObservedAt
		events[candidate.refill].PluginResetAt = at
		events[candidate.credit].PluginResetAt = at
	}
}

func historyValidUsed(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 100
}

func historyHasRefill(event UsageHistoryEvent) bool {
	return event.Before != nil && event.After != nil && historyValidUsed(event.Before.UsedPercent) &&
		historyValidUsed(event.After.UsedPercent) && *event.After.UsedPercent < *event.Before.UsedPercent-0.001
}
