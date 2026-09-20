# ADR-0019: Link delayed observations to a confirmed plugin reset

**Status:** Accepted
**Date:** 2026-09-19
**Applies to:** history helper and widget
**Amends:** ADR-0011 and ADR-0014 presentation; preserves ADR-0015 refresh policy

## Context

Auto-use records a confirmed reset before the shared cooldown permits another
quota read. The next observation can therefore show an "Unexpected
replenishment" and a credit decrease separately from the successful action.
Those labels obscure the available evidence and can prompt an unnecessary
explanation.

## Decision

- Derive a `pluginResetAt` association when a Codex general-quota refill and
  exactly one credit decrease share an observation interval containing one
  confirmed successful plugin reset. Require the same quota bucket and prior
  window, consistent pre-action usage, and a still-future natural reset.
  Limit the interval to one hour; ambiguous actions, multiple matching refill
  groups, missing evidence and unrelated buckets remain unlinked.
- Retain the provider sample time separately from the observation-record time
  for new events. A sample taken before the action cannot describe its effects.
  Older events use their recorded observation time, so the association is
  context, not proof that every simultaneous change has the same cause.
- Recompute associations for retained history, preserving original kinds,
  confidence, timestamps, values, group identifiers and user explanations.
  This is additive presentation metadata, not a state-version migration or
  a new explanation written on behalf of the user.
- Present the action and its linked later observations in one history card,
  with both timestamps and the measured changes. Label the refill as observed
  after the plugin reset. Keep unrelated events and explanation targets intact.
- Quiet linked observation rows without exposing older unanswered prompts.
  Explain/Edit remains available for the original observation group.
- Do not change redemption, cooldowns, notification receipts or unread event
  identities. No live reset is necessary to test this behavior.

## Alternatives

Fetching immediately would violate the shared cooldown. Rewriting the later
observation as another confirmed reset would overstate its evidence and lose
the original record. Grouping everything within a loose time window could
hide unrelated changes. A bounded evidence-based association retains the
facts while making the relationship visible.
