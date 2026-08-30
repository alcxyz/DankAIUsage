# ADR-0006: Opt-in Claude prime with local timer fallback

**Status:** Superseded by [ADR-0001](ADR-0001-claude-limits-from-oauth-usage-api.md) (limit recovery) and [ADR-0007](ADR-0007-auto-prime-as-window-scheduler.md) (current role)
**Date:** 2026-06-22
**Applies to:** `cmd/dankaiusage/main.go` (claude-prime), `DankAIUsageWidget.qml` (auto-prime)

## Context

The statusline cache (ADR-0005) refreshes only when an interactive Claude Code
session answers a request. Between sessions — or on hosts that never run the
TUI — the cache goes stale and the widget shows no Claude limits. The idea:
deliberately spend one tiny Claude request to make Claude Code publish fresh
limits.

## Decision

Add `dankaiusage claude-prime`, strictly opt-in (`enableClaudePrime` setting,
default off) because it spends real subscription usage. It sends one minimal
`claude -p` request (safe mode, no session persistence, tools disabled, tiny
system prompt, low budget cap, `sonnet`) and waits for the statusline cache to
refresh. Because print mode turned out not to drive the statusline, a fallback
records a local five-hour session timer from the successful request, giving
the widget at least an accurate reset countdown. Auto-prime guards prevent
retry loops: no prime while a timer is active, and a failed automatic prime is
not retried until re-armed manually.

## Alternatives Considered

- **Automatic priming without opt-in:** silently spends the user's money;
  rejected.
- **Driving a headless TUI via pty to force a statusline pass:** fragile and
  heavier than a print-mode call.
- **Accepting stale/absent limits:** the status quo this tried to fix.

## Consequences

- At best a session-reset timer, never real percentages, at the cost of a
  paid request and considerable guard complexity (five follow-up fixes in two
  days).
- Superseded by ADR-0001 for its original purpose: the usage API returns real
  percentages without spending tokens. The feature itself lives on with a
  different job — deliberately starting session windows early — recorded in
  ADR-0007, which also replaced the guard chain described here.
