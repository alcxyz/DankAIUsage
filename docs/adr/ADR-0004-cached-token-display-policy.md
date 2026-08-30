# ADR-0004: Cached-token display policy and per-provider total semantics

**Status:** Accepted
**Date:** 2026-06-03 (display default tightened 2026-06-24)
**Applies to:** `cmd/dankaiusage/main.go` (eventTotal), `DankAIUsageWidget.qml` (displayTotal)

## Context

Claude Code reports prompt-cache reads as part of per-request usage, and cache
reads dominate raw volume: a trivial request in a long session can carry
millions of cached tokens. Codex reports cached tokens as a subset rather than
an additive component. Summing everything uniformly produces Claude totals
that dwarf Codex totals and say nothing about actual work performed.

## Decision

Two layers:

- **Collection keeps provider-native semantics** (`eventTotal`): Claude raw
  totals are `input + output + cached` (what Claude Code reports as the event);
  Codex totals are `input + output`. The `cached` component is always carried
  separately in `PeriodTotals`.
- **Display subtracts cached tokens by default** (`displayTotal` in the
  widget): shown totals are `total - cached`, approximating "fresh work"
  tokens. An "Include cached tokens" setting restores raw totals for users
  inspecting cache overhead.

## Alternatives Considered

- **Raw sums everywhere:** cached noise makes the numbers useless as an
  effort signal and inconsistent across providers.
- **Dropping cached tokens at collection time:** loses the data needed for the
  opt-in raw view and for diagnosing cache-heavy sessions.
- **Normalizing Codex to include cached:** would fabricate a number Codex
  itself does not report additively.

## Consequences

- Displayed totals intentionally do not match ccusage or raw transcript sums;
  the toggle exists for reconciliation.
- Displayed "input" is derived (`displayTotal - output`), not the raw input
  field.
- Per-provider semantics live in one function (`eventTotal`) with a test
  pinning the difference (`TestEventTotalProviderSemantics`).
