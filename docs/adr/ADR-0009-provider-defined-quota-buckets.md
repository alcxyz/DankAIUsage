# ADR-0009: Render provider-defined quota buckets

**Status:** Accepted
**Date:** 2026-08-30
**Applies to:** `cmd/dankaiusage/main.go`, `DankAIUsageWidget.qml`

## Context

A fixed session/weekly UI no longer represents either provider completely.
Codex may return only a general weekly limit plus separate model-scoped
windows. Claude can return five-hour, weekly, model-scoped, and monetary
extra-usage limits. Its usage response currently exposes the monetary cap in
both `spend` and `extra_usage` forms.

## Decision

- Emit an ordered `quotaBuckets` list for every provider. Each bucket carries
  an identifier, label, kind, allowance percentages, and optional formatted
  value/detail labels.
- Retain `sessionLeft`, `weeklyLeft`, and `extraLimits` in the helper output for
  compatibility and Claude prime scheduling, but do not use them to construct
  the widget layout.
- Flatten scoped windows into independent quota buckets.
- Parse Claude's `spend` object as the authoritative extra-usage representation
  and fall back to `extra_usage`. Monetary amounts remain integer minor units;
  currency and exponent metadata determine their display.
- Render every bucket with the same label/value/detail/progress-bar component.
- Use the lowest remaining bucket for provider summaries, the compact pill,
  and the overall "Most constrained" summary.
- Allow a persisted Left / Used display choice directly in the dropdown.
  All quota percentages and progress bars, including credits, follow that
  choice. Remaining is the default; warning severity and most-constrained
  ordering always use remaining allowance. Credit details retain the provider's
  formatted monetary values. Quick bar controls use the same saved preferences
  as the full settings menu.

## Consequences

New provider limits can be added without another fixed-column UI redesign.
The old weekly-focus setting is removed because there is no longer one global
pair of windows to select between. Credit usage is visible but remains
read-only; purchasing or changing extra-usage settings stays in the provider's
own account UI.
