# ADR-0008: Classify Codex windows by duration and expose banked resets

**Status:** Accepted
**Date:** 2026-08-30
**Applies to:** `cmd/dankaiusage/main.go`, `DankAIUsageWidget.qml`

## Context

The Codex app-server originally returned a five-hour window in `primary` and a
weekly window in `secondary`. The collector encoded those positions as
session and weekly respectively.

The current response may instead return the general Codex allowance as a
10,080-minute `primary` window with `secondary: null`. It can also return
separate model-scoped windows and `rateLimitResetCredits`. Treating field
position as window identity makes the weekly allowance appear as a session
limit and fabricates a second, empty weekly limit.

## Decision

- Treat `primary` and `secondary` as nullable.
- Classify each present window by `windowDurationMins`: short durations are the
  short window and week-length durations are weekly. Retain positional fallback
  only when the server omits a usable duration.
- Omit missing windows from the UI instead of displaying an inferred quota.
- Parse available, unexpired banked resets and show their title and expiry.
- Keep reset consumption outside the plugin. Users apply a banked reset from
  Codex **Settings → Usage**, avoiding accidental account-state changes.
- Keep model-scoped limits separate from the general Codex allowance.

## Consequences

The widget matches both weekly-only and legacy dual-window responses. Claude's
five-hour and weekly windows remain supported. Adding reset consumption later
would require an explicit, confirmed action and separate protocol support.
