# ADR-0028: Prime only an idle, reported five-hour window

**Status:** Accepted
**Date:** 2026-11-02
**Applies to:** `cmd/dankaiusage` (`claude-prime`), `DankAIUsageWidget.qml` (auto-prime), settings
**Builds on:** ADR-0001, ADR-0015, ADR-0027
**Replaces:** the retired [ADR-0006](archive/ADR-0006-opt-in-claude-prime.md)
and [ADR-0007](archive/ADR-0007-auto-prime-as-window-scheduler.md)

## Context

For a user who reliably uses up each five-hour window, a window that is not
running is capacity lost: starting the next one as soon as the previous one
ends, rather than on the next real request, starts its countdown earlier. A
tiny prime request does that (ADR-0007).

ADR-0007's guard chain fell back to a local five-hour timer whenever account
data could not show an active window. When an account lost its five-hour
window, the usage response never showed one running, so prime fired about
every five hours, spending usage with nothing to start. ADR-0027 removed prime
in 2.0.0 for that reason. Five-hour windows are back where providers report
them (ADR-0027), so prime is useful again on those plans, but it must not be
able to repeat that failure.

## Decision

- `dankaiusage claude-prime` and auto-prime return, opt-in through
  **Claude prime** (`enableClaudePrime`, default off), because the request
  spends a little real usage.
- The helper decides only from the latest usage response the quota poll saved
  (`claude-oauth-usage.json`). It makes no usage request of its own and has no
  local-timer fallback. It sends the prime request only when all hold:
  1. the response is readable, not invalidated, and its last refresh did not
     fail;
  2. it was fetched within twice the refresh interval, and at least within
     ten minutes;
  3. it carries the organization of the current Claude sign-in;
  4. it is newer than the last prime, and the last prime is at least 15
     minutes old;
  5. it reports a five-hour window (`five_hour` is present), and that window
     is not running (no reset time, or one already past; a reset even seconds
     ahead counts as running);
  6. after a prime, a window ran since: a usage response newer than the prime,
     for the sign-in that was primed, showed the window running (each summary
     records this in the state file),
     or the idle window's last reset is later than the prime. Otherwise prime
     waits five hours from the last prime, so a prime that starts nothing (for
     example a request billed elsewhere) is not repeated every 15 minutes.
  Otherwise it skips and says why: unknown, failed or stale usage, another
  sign-in, waiting for the result of the last prime, the last prime not shown
  to start a window, no five-hour window, or window already running.
- The decision and the prime time are recorded under a lock in
  `claude-prime-state.json` before the request runs, so concurrent callers,
  a crash or a failed request cannot send a second one before a newer usage
  response. A `claude-prime.json` timer left by 1.x is ignored.
- The request is the 1.x one: `claude -p` with tools disabled, no session
  persistence, a fixed one-word prompt, a budget cap of 0.001 USD
  and the `sonnet` model.
- The widget asks the helper at most once per refresh interval, only after a
  live summary (never a cached one) whose Claude provider reports an idle
  five-hour window with current, error-free usage. While prime is on, the
  Claude card shows its status in both modes, including "idle: this account
  has no five-hour window"; the helper's last answer appears under
  **Advanced → Diagnostics**. Advanced offers a manual prime button while the
  window is idle; the helper applies the same checks.

## Alternatives Considered

- **ADR-0007's guard chain with a local timer:** caused the 2.0.0 failure
  mode on accounts without a window; rejected.
- **Let prime fetch usage itself before deciding:** a second request path
  against a rate-limited endpoint (ADR-0015, ADR-0023) for little gain, since
  the widget primes right after a poll; rejected.
- **Leave prime removed:** loses the head start on plans that still have the
  window; rejected now that the window is back.

## Consequences

- An account without a five-hour window, or with unknown, stale or failing
  usage, is never primed; tests cover two days of checks on such an account.
- Cost stays bounded: about one tiny request per five-hour window at most,
  none while the window runs, and one per five hours when primes start
  nothing.
- A prime shows in the widget after the next scheduled usage poll, not
  immediately.
- Priming happens only while DankMaterialShell runs; nothing schedules windows
  when the machine or shell is down.
