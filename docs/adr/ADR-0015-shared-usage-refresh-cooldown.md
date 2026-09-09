# ADR-0015: Shared usage refresh cooldown

**Status:** Accepted
**Date:** 2026-09-09
**Applies to:** `cmd/dankaiusage`, widget and settings
**Amends:** ADR-0001, ADR-0007 and ADR-0010 refresh timing

## Context

The five-minute widget timer did not bound all network activity. Manual
refreshes, multiple helpers, the two-minute Claude cache and one-minute armed
reset checks could fetch usage more frequently. A slider-only restriction
would leave those other paths unprotected. Users also want less frequent
background work to reduce power use.

## Decision

- Expose a **Usage refresh interval** slider from three to sixty minutes,
  with a five-minute default, a visible default marker and a reset action.
  Preserve the existing seconds-based setting and clamp old values to the
  supported range.
- Enforce the interval in the helper, with shared, durable per-provider
  caching and locking. Manual refresh and concurrent processes cannot bypass
  the three-minute minimum. Provider error backoff can require a longer wait.
- Reuse cached observations during cooldown, retaining their original fetch
  time so the same snapshot does not advance reset history repeatedly. A fresh
  snapshot fetched by a reset check can still enter history when the summary
  first reads it. Do not equate local UI redraws or countdown updates with
  provider requests.
- Apply the selected interval to regular summary collection and automatic
  reset checks. Longer intervals also reduce normal local token-history scans;
  explicit local actions can still update local data.
- Never redeem a reset based on a stale cached usage observation. Respect the
  network cooldown when obtaining post-action quotas; a confirmed reset outcome
  does not itself establish new percentages.
- Do not change Claude prime's model, budget, opt-in requirement or failure
  reporting as part of this change. Its usage reads share the cooldown, while
  its deliberate model request remains a separate opt-in action.

Three minutes is a conservative product choice, **not** a provider-published
guarantee against bans or a determination that an authentication method is
permitted. This change does not resolve the separate policy and compatibility
risks of Claude's undocumented OAuth usage endpoint described in ADR-0001.

## Alternatives and consequences

A UI-only minimum is simpler but does not protect multiple widgets or direct
helper invocations. A daemon could centralize requests but adds a service and
lifecycle dependency. Per-provider on-disk coordination fits the existing
short-lived helper architecture.

Less frequent updates trade freshness for less background work. In particular,
long intervals, sleep or provider failures can delay or miss an earned reset's
expiry window. The one-shot reset control remains best effort, not a guarantee.
Manual refresh can therefore show cached quotas rather than contacting a
provider immediately.
