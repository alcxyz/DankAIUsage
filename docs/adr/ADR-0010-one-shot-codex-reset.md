# ADR-0010: Opt-in, one-shot Codex reset scheduling

**Status:** Accepted
**Date:** 2026-09-08
**Applies to:** `cmd/dankaiusage`, `DankAIUsageWidget.qml`
**Amends:** ADR-0008's read-only reset decision

Check scheduling and post-consumption usage refresh timing are amended by
[ADR-0015](ADR-0015-shared-usage-refresh-cooldown.md).

## Context

Earned Codex resets expire. Users want to use an available reset late enough to
retain their existing allowance, without missing its expiry. The Codex app
server now documents `account/rateLimitResetCredit/consume`, including a credit
identifier, idempotency key, and explicit outcomes.

## Decision

- Provide an explicit **Auto-use one reset** control in the Codex dropdown.
  It is off by default. Arming selects the earliest-expiring available
  `codexRateLimits` credit whose identifier and future expiry are known.
- Keep durable one-shot state in the helper, not a second DMS preference.
  Serialize access across processes, store state with restrictive permissions,
  and pin the selected credit and a UUID idempotency key to the logical attempt.
- Re-read provider limits before acting. Trigger at 99% general Codex usage,
  unless a known natural reset is within ten minutes; alternatively, try within
  ten minutes of the credit's expiry if there is general usage in a window
  whose natural reset time is known and still in the future. Unknown or stale
  reset timestamps are insufficient for an automatic account mutation.
  Spark-only usage never triggers consumption. These are conservative local
  heuristics, not provider guarantees of eligibility or optimal timing.
- Persist the control as off **before** attempting consumption. A crash,
  timeout, or failure cannot lead to an unattended second attempt. Display an
  uncertain result explicitly and require the user to re-arm for another
  attempt. Never silently choose a replacement credit.
- Treat `reset` and `alreadyRedeemed` as success, then fetch provider limits
  again. Do not infer fresh percentages from a successful consume response.
- The widget checks once per minute while loaded. Hiding Codex pauses automatic
  checks. Sleep, shutdown, unavailable authentication, or connectivity can
  prevent timely use; this is not a background service or an expiry guarantee.
- Test consumption only with fake servers and temporary state. Installing or
  upgrading the plugin never arms the control or redeems a live reset.

## Alternatives

Keeping resets manual avoids account writes but does not address expiring
credits. An always-on recurring reset policy could consume multiple credits
without renewed intent. A background daemon could run without DMS, but adds
lifecycle and deployment complexity. The explicit one-shot control keeps the
authorization narrow and uses the existing helper.

## References

- [Codex app-server: earned rate-limit resets](https://learn.chatgpt.com/docs/app-server#8-earned-rate-limit-resets-chatgpt)
- [Codex-Spark has separate usage limits](https://learn.chatgpt.com/docs/agent-configuration/speed#codex-spark)
