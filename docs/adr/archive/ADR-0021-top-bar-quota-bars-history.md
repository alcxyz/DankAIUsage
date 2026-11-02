**History of** [ADR-0021](../ADR-0021-top-bar-quota-bars.md): text replaced on 2026-10-31 and 2026-11-02.

# ADR-0021: Optional quota bars in the horizontal top bar

**Status:** Accepted
**Date:** 2026-09-23
**Applies to:** `DankAIUsageWidget.qml`, settings
**Builds on:** ADR-0009, ADR-0013 and ADR-0018
**Amends:** ADR-0018 with the opt-in color exceptions below.
**Amended by:** ADR-0025 (credit switches add a credit bar in quota-bar mode)
and ADR-0026 (the usage gradient becomes a shared heatmap for the top bar and
dropdown, with theme and pace options).

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

## Opt-in color exceptions

ADR-0018 derives all status colors from the theme and rejects per-provider
brand colors. Two off-by-default settings deliberately step outside that for
users who prefer them: **Brand-colored logos** (Claude `#D97757`, OpenAI
white on dark themes, black on light) and **Color quota bars by usage** (a fixed
green, yellow, orange, dark red gradient over percent used). Defaults keep
ADR-0018 behaviour, errors still use the theme's error color, and the dropdown's
quota colors are unchanged. The fixed gradient can clash with light or custom
themes; that is the user's trade-off when enabling them.

## Consequences

The default top bar is unchanged. Bar mode trades exact numbers for density
unless the percent label is enabled. Tags are abbreviations; the dropdown and
hover details remain the place for full labels. New quota kinds without a
window render as `?` tags until their window length is reported.

## Text replaced on 2026-11-02

Five-hour windows were restored where providers still report them ([ADR-0027](../ADR-0027-remove-five-hour-windows-prime-and-statusline.md)), which replaced:

**Status:** Accepted, amended 2026-10-31 by [ADR-0027](../ADR-0027-remove-five-hour-windows-prime-and-statusline.md) (no short window or short-window tags)
**Date:** 2026-09-23

The horizontal pill shows quotas as text. With Claude's weekly and
model-scoped weekly limits plus Codex (and, when this was decided, Claude's
five-hour window), that text gets long, and a percentage

  thin bar per quota, stacked in helper order: weekly, then model-scoped
  limits. Row pitch derives from the bar thickness, so up to four

  countdown (elapsed window time with Used), or the percentage. General
  quotas are tagged `w`, since weekly is the only general window (ADR-0027),
  and model-scoped limits take the model initial (`f`), growing only when two
  tags would clash. There are no short-window tags. Label columns

hover details remain the place for full labels.
