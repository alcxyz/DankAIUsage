**History of** [ADR-0027](../ADR-0027-remove-five-hour-windows-prime-and-statusline.md): text replaced on 2026-11-01.

# ADR-0027: Remove five-hour windows, Claude prime and the statusline fallback

**Status:** Accepted
**Date:** 2026-10-31
**Applies to:** `cmd/dankaiusage`, `DankAIUsageWidget.qml`, settings
**Retires:** [ADR-0007](ADR-0007-auto-prime-as-window-scheduler.md);
finalises the already superseded
[ADR-0005](ADR-0005-claude-limits-via-statusline-capture.md) and
[ADR-0006](ADR-0006-opt-in-claude-prime.md).
**Amends:** ADR-0001, ADR-0003, ADR-0008, ADR-0009, ADR-0011, ADR-0012,
ADR-0013, ADR-0015, ADR-0021 and ADR-0022 where they described five-hour
windows, prime or the statusline fallback.

## Context

Anthropic and OpenAI removed their five-hour usage windows. Observed on
2026-10-31: Claude's OAuth usage response has `five_hour: null`, and its
`limits` array holds only `weekly_all` and `weekly_scoped` entries; the Codex
app-server returns only a 10,080-minute `primary` window with
`secondary: null`.

Several features existed only for the five-hour window. Claude prime
(ADR-0007) scheduled it: with no window to start, it kept firing about every
five hours through its local-timer fallback, spending usage for nothing. The
top bar's session toggle and short-window tags, session buckets and totals,
the five-hour token range and the scheduled five-hour reset-history filter all
described a window that no longer exists.

The Claude statusline capture (ADR-0005) has been a fallback since ADR-0001
made the OAuth usage endpoint the primary source. It only works while Claude
Code runs as an interactive terminal TUI.

## Decision

- Strip all five-hour and session-window support rather than keeping it
  generic: no `five_hour` parsing, no session buckets, no `sessionLeft` or
  session totals in the helper output, no top-bar session toggle and no
  short-window tags.
- Codex supports only week-length windows. A window without a usable duration
  is treated as weekly; a window with another known duration is ignored.
- If a provider reintroduces a short window, it is ignored until new code and
  a new decision support it.
- Remove Claude prime and auto-prime (`claude-prime`, `enableClaudePrime`).
- Remove the Claude statusline fallback (`claude-statusline` and its cache).
  When the OAuth usage fetch fails, Claude limits are unavailable and the
  error is shown. `dankaiusage claude-statusline` stays as a silent no-op
  that drains its input and prints nothing, so a Claude Code
  `statusLine.command` still pointing at it shows a blank status line rather
  than an error. The removed `claude-prime` command is rejected.
- Token-history ranges are 7d, 30d, 90d and Tracked. A saved 5h choice falls
  back to 7d.
- Reset history drops the **Scheduled 5-hour resets** filter. Stored legacy
  scheduled five-hour events stay hidden; unexpected changes on legacy
  five-hour buckets stay visible with their stored label. The history file is
  not rewritten.
- Claude limit resets (ADR-0022, ADR-0023, ADR-0024) and Codex resets
  (ADR-0010) stay; their grants now clear the weekly window.

## Alternatives Considered

- **Keep generic duration-based buckets**, so a returning short window would
  render automatically: rejected by the owner as dead code for a window that
  no longer exists.
- **Keep the statusline fallback:** rejected. It is TUI-only, has not been the
  primary source since ADR-0001, and removing it shrinks the surface.

## Consequences

- Prime no longer spends usage, and the helper, widget and settings lose the
  session, prime and statusline code paths.
- A returning five-hour window from either provider is not shown until the
  plugin is changed and a new decision is recorded.
- Hosts that relied on the statusline fallback (TUI-only setups, macOS
  installs with credentials in the Keychain) show no Claude limits, only the
  usage-fetch error.
- Existing reset history stays readable without migration; legacy five-hour
  entries follow the rules above.
- ADR-0007 is retired, and ADR-0005 and ADR-0006 move to the archive with it.
