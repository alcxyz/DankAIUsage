# ADR-0002: Display authoritative subscription-window percentages, not token-derived estimates

**Status:** Accepted
**Date:** 2026-05-28
**Applies to:** `cmd/dankaiusage/main.go`, `DankAIUsageWidget.qml`

## Context

The first version of the widget led with token totals. Subscription plans for
both Codex and Claude are gated by rolling usage windows (a ~5-hour session
window and a weekly window), and neither vendor publishes a token budget for
those windows, so raw token counts answer "how much did I use" but not "how
much do I have left" — the question a bar widget exists to answer.

## Decision

Lead with the remaining percentage of each provider's session and weekly
window, taken from an authoritative provider source (Codex app-server, Claude
statusline at the time; the Anthropic usage API since
[ADR-0001](ADR-0001-claude-limits-from-oauth-usage-api.md)). Token totals are
demoted to a secondary history detail. The pill shows `session/weekly`
percentages per provider.

## Alternatives Considered

- **Estimate limits from token counts against assumed plan budgets**
  (ccusage-style): plan budgets are unpublished, vary by plan and model mix,
  and drift when vendors retune limits; estimates fail exactly when they
  matter (near the cap).
- **Show cost in USD:** meaningless for flat-rate subscriptions.
- **Tokens only (status quo):** answers the wrong question.

## Consequences

- The widget depends on provider-specific limit interfaces, each with its own
  failure modes and follow-up decisions (ADR-0001, ADR-0005, ADR-0006).
- When no limit source is available the display degrades to `--` rather than
  showing a fabricated estimate; this is deliberate.
- `makeAllowance` retains a token-unit allowance path for hypothetical
  providers that do publish token budgets.
