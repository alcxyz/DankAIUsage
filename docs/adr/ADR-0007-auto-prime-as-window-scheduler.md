# ADR-0007: Auto-prime as session-window scheduler

**Status:** Accepted
**Date:** 2026-07-03
**Applies to:** `cmd/dankaiusage/main.go` (claude-prime guards), `DankAIUsageWidget.qml` (auto-prime)

Usage fetch timing, including the post-prime refresh, is amended by
[ADR-0015](ADR-0015-shared-usage-refresh-cooldown.md).

## Context

[ADR-0001](ADR-0001-claude-limits-from-oauth-usage-api.md) removed prime's
original purpose (recovering limit visibility), and
[ADR-0006](ADR-0006-opt-in-claude-prime.md) concluded there was no reason to
enable it anymore. That conclusion assumed prime existed only to *observe*
windows.

There is a second use: for a user who reliably exhausts every 5-hour window,
a window that is not running is capacity wasted. Starting the next window as
soon as the previous one closes — rather than lazily on the next real
request — makes the countdown start earlier and effectively yields more
usable capacity per day. The tiny prime request is always amortized because
the window it starts is always fully used.

## Decision

Keep `claude-prime` and run it with auto-prime enabled, repurposed as a
window scheduler. The guard chain is reworked to be driven by account data
rather than a blind local timer:

1. **Skip if the account window is active** per the cached usage-API data.
   A cached reset time is trustworthy regardless of cache age — a window
   cannot end before its own reset — so this needs no extra fetch.
2. **15-minute floor between real prime requests** (`claudePrimeMinInterval`),
   protecting against a burn loop if the usage API briefly lags in reporting
   a freshly started window.
3. **Live usage check** before priming; if it reports an active window, skip.
4. **Local 5-hour timer** remains the guard only when account data is
   unavailable (credentials missing, API backoff).
5. After a successful prime, the usage cache is refreshed immediately so the
   widget and any subsequent prime attempt see the new window without
   waiting out the cache TTL.

This eliminates the dead zone where the old local timer outlived the account
window (the API rounds resets down to 10-minute marks), which used to block
the next prime for up to ~10 minutes.

## Alternatives Considered

- **Leave prime disabled (ADR-0006 conclusion):** windows start lazily on
  first real use; the head-start capacity is lost. Rejected by actual usage
  pattern.
- **A systemd timer priming outside the widget:** would also work when the
  shell is down, but duplicates scheduling logic that the widget's refresh
  loop already provides, and adds a second component to keep consistent.
- **Keeping the blind 5-hour local timer as primary guard:** simpler, but
  causes the recurring dead zone and skips report no useful limit data.

## Consequences

- Cost is bounded and negligible: at most one 1-turn request
  (~$0.0005-equivalent, measured) per 5-hour window, ~5/day when idle.
- A new window starts within one widget refresh (~5 minutes, default) of the
  previous window closing.
- Skips now return real percentages from the usage cache instead of an
  empty timer allowance.
- Priming only happens while DankMaterialShell is running; nothing schedules
  windows when the machine or shell is down.
- If the usage API is unavailable, behavior degrades to the conservative
  ADR-0006 timer guard rather than priming blind.
