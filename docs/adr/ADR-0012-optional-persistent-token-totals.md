# ADR-0012: Optional persistent local token totals

**Status:** Accepted
**Date:** 2026-09-08
**Applies to:** token collection, local state, dropdown range controls

## Context

Provider quotas describe current account allowance. Local transcripts provide
token history, but removing them also removes the history available to a fresh
scan. The maintainer wants an optional longer-term total that survives transcript
cleanup without presenting incomplete local observations as account-wide or
all-time usage.

## Decision

- Offer explicit rolling ranges: five hours, seven days, thirty days, and
  ninety days. These are not quota windows or active-conversation totals.
  Retain the configured history period for compatibility.
- Add a separate **Tracked total** view. Tracking is off by default and is
  controlled by the helper, not by independent widget-local counters.
- First enable seeds the total from retained local transcripts without a
  ninety-day cutoff. Record when tracking started and explain that the seed
  can include older events. Unavailable or incomplete source history must be
  disclosed; it cannot be reconstructed or silently called complete.
- While enabled, collect newly observed usage into durable totals using
  stable hashed event identities. Refreshes, restarts, copied transcripts,
  and repeated cumulative snapshots must not count an event again. Token
  display keeps ADR-0004's provider-specific cached-token semantics.
- Pause preserves totals and checkpoints. Resume does not reseed retained
  history or backfill the explicitly paused interval. Clear is a separate,
  confirmed action that removes tracked totals/checkpoints and disables
  tracking; it does not remove provider transcripts or quota reset history.
- Keep state local and private, with atomic replacement and bounded locking.
  Reject corrupt or unsupported state rather than resetting and reseeding it
  implicitly. A generation check prevents an older scan committing after a
  pause, clear, or new tracking period.
- Persist token counters and dates in hashed duplicate-detection checkpoints;
  expose aggregated provider totals to the widget and command output.
  Never persist prompts, transcript contents, raw session identifiers, paths,
  account identifiers, or credentials in tracking state. Do not duplicate
  tracked state in the DMS summary cache.
- No new database or service. Enabled tracking may scan more retained files
  than the rolling views. Totals are updated incrementally after deduplication;
  this first implementation favors correctness over a file-offset cache.
- Codex can emit distinct snapshots at the same timestamp without event IDs.
  Identify these by their hashed source snapshot, not their derived delta or
  position in a file. After seeding, unfamiliar snapshots at or before a
  session's previously observed timestamp are conservatively skipped and
  disclosed as partial coverage: a late arrival cannot reliably be distinguished
  from a rewritten checkpoint. All distinct snapshots in a new timestamp group
  are accepted together.

## Alternatives and consequences

- Call the result all-time usage: rejected because deleted, missing, remote,
  and pre-tracking history cannot be recovered reliably.
- Sum each rolling total into a saved counter: rejected because overlapping
  windows double-count usage and deletions can reduce the apparent baseline.
- Retain transcripts forever: stores far more sensitive data than needed and
  changes the provider CLI's lifecycle policy.
- Add a database or background collector now: unnecessary operational overhead
  for the initial opt-in feature. Revisit only with measured evidence.

Checkpoints grow with observed usage; they have no time-based retention cutoff
that would allow old events to be counted again. Clearing tracking removes this
state. If transcripts disappear before the helper observes their usage, that
usage is lost. Provider-reported quotas, rolling local history, and tracked
totals remain deliberately separate views with different scopes.
