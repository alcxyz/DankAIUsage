# ADR-0011: Bounded local quota and reset history

**Status:** Accepted
**Date:** 2026-09-08
**Applies to:** `cmd/dankaiusage`, `DankAIUsageWidget.qml`

## Context

The provider APIs expose current allowance windows, not a complete account
reset audit trail. A low percentage alone cannot tell a user whether a window
rolled over, an earned reset was redeemed, or the provider replenished usage.
The user wants evidence for those distinctions, including resets made outside
the plugin, without adding a monitoring service or another dependency.

## Decision

- Compare fresh, authoritative subscription observations during normal helper
  collection. The first observation establishes a baseline, not a reset event.
- Retain at most 200 events for 30 days in a private local JSON state file,
  with bounded locking and atomic writes. Do not store credentials, account
  identifiers, prompts, raw provider responses, or opaque reset-credit IDs.
- Record observation intervals, quota identity, before/after usage, reset
  timestamps, and available earned-reset counts where known. Do not claim an
  observation timestamp is the exact time of the underlying account change.
- Classify scheduled window changes as inferred. Flag an early allowance
  increase as **Unexpected replenishment**, with a possible provider reset as
  an explanation, not an established cause. A changed account, plan, corrected
  measurement, or external manual action can produce similar observations.
- Correlate an early general Codex refill with a decrease in available earned
  resets as **Likely reset redeemed** only when the recorded expiry evidence
  does not explain the decrease. A count decrease alone is not redemption
  proof; unchanged counts do not rule it out if another credit was granted.
- Record explicit plugin reset outcomes separately. Confirm only what the
  consume response establishes. An attempt with an uncertain outcome is not
  a confirmed reset. Logging failure must not weaken the one-shot safety
  transition or cause an account request to be retried.
- Ignore unknown/stale/fallback-inferred readings for change detection. Missing
  buckets do not mean that usage reset. Idle sliding reset timestamps must not
  produce repetitive reset events.
- Display the latest eight events in a collapsible history section, following
  provider visibility and the Left/Used presentation preference. Keep history
  out of DMS's persistent summary cache so there is one retained history store.

## Alternatives and consequences

A provider-side audit API would establish stronger attribution, but the current
interfaces do not provide that evidence. A background collector or database
would add maintenance and lifecycle costs; normal polling and a bounded file
are sufficient for this feature.

History starts with installation and observations. Sleeping, stopping DMS,
authentication failures, caching, or infrequent polling can hide intermediate
events. This is a local observation log, not a billing or compliance ledger.
