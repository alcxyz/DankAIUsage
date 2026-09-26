# ADR-0021: Optional quota bars in the horizontal top bar

**Status:** Proposed
**Date:** 2026-09-23
**Applies to:** `DankAIUsageWidget.qml`, settings
**Builds on:** ADR-0009, ADR-0013 and ADR-0018

## Context

The horizontal pill shows quotas as text. With Claude's five-hour, weekly and
model-scoped weekly limits plus Codex, that text gets long, and a percentage
alone does not show whether usage is ahead of the time left in its window.
The dropdown already has allowance bars and, in Advanced, window-time bars.

## Decision

- Add an off-by-default **Quota bars instead of text** setting for the
  horizontal pill. Each visible provider shows its logo (or its name when
  logos are disabled) and one
  thin bar per quota, stacked in helper order: short window, weekly, then
  model-scoped limits. Row pitch derives from the bar thickness, so up to four
  rows fit.
- Bars follow the existing quota selection: Claude honours its per-quota
  top-bar choices; Codex shows all non-credit quotas. Credits stay in the
  dropdown because they have no window, even when their top-bar switches are
  enabled. These switches retain their saved values and apply to text mode;
  settings describe this distinction. Compact pill does not apply, and the
  vertical pill is unchanged.
- Fill follows Left / Used and uses the ADR-0018 severity colors (`primary`,
  `warning`, `error`), like the text pill.
- Optional short labels on either side of each bar: a quota tag, the reset
  countdown (elapsed window time with Used), or the percentage. Tags derive
  from the window length (`5h`, `w`) and, for model-scoped limits, the model
  initial (`f`, `f5h`), growing only when two tags would clash. Label columns
  are sized from the theme font so they neither jitter nor elide.
- An optional pace tick marks the ADR-0013 window-time position on each bar.
  It uses theme colors with contrasting edges so it stays visible on any fill.
- Width is configurable (16–120 px, default 40). All of this is presentation
  only: no extra provider requests and no change to collection.

## Consequences

The default top bar is unchanged. Bar mode trades exact numbers for density
unless the percent label is enabled. Tags are abbreviations; the dropdown and
hover details remain the place for full labels. New quota kinds without a
window render as `?` tags until their window length is reported.
