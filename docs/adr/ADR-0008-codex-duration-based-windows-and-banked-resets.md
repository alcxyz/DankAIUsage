# ADR-0008: Classify Codex windows by duration and expose banked resets

**Status:** Accepted, amended 2026-10-31 by [ADR-0027](ADR-0027-remove-five-hour-windows-prime-and-statusline.md) (week-length windows only)
**Date:** 2026-08-30
**Applies to:** `cmd/dankaiusage/main.go`, `DankAIUsageWidget.qml`
**History:** [archive/ADR-0008-codex-duration-based-windows-and-banked-resets-history.md](archive/ADR-0008-codex-duration-based-windows-and-banked-resets-history.md)

**Amended by:** [ADR-0010](ADR-0010-one-shot-codex-reset.md) permits an explicit,
default-off one-shot reset control through the supported app-server protocol.
[ADR-0022](ADR-0022-manual-claude-limit-reset.md) adds explicit, confirmed use
of Claude limit resets.

## Context

The Codex app-server originally returned a five-hour window in `primary` and a
weekly window in `secondary`. The collector encoded those positions as
session and weekly respectively.

The response then began returning the general Codex allowance as a
10,080-minute `primary` window with `secondary: null`. OpenAI has since
removed the five-hour window: on 2026-10-31 that weekly window was the only
general window returned
([ADR-0027](ADR-0027-remove-five-hour-windows-prime-and-statusline.md)). The
response can also return
separate model-scoped windows and `rateLimitResetCredits`. Treating field
position as window identity makes the weekly allowance appear as a session
limit and fabricates a second, empty weekly limit.

## Decision

- Treat `primary` and `secondary` as nullable.
- Classify each present window by `windowDurationMins`. Only week-length
  windows are supported: a week-length duration is weekly, a window without a
  usable duration is treated as weekly, and a window with any other known
  duration, such as a returning five-hour window, is ignored.
- Omit missing windows from the UI instead of displaying an inferred quota.
- Parse available, unexpired banked resets and show their title and expiry.
- Keep reset consumption outside the plugin. Users apply a banked reset from
  Codex **Settings → Usage**, avoiding accidental account-state changes.
- Keep model-scoped limits separate from the general Codex allowance.

## Consequences

The widget shows the weekly window of both weekly-only and legacy dual-window
responses; a short window is not shown until new code and a new decision
support it. Adding reset consumption later
would require an explicit, confirmed action and separate protocol support.
