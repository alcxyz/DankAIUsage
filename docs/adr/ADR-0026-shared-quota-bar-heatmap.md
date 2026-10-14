# ADR-0026: Shared quota-bar heatmap with theme and pace options

**Status:** Accepted
**Date:** 2026-10-14
**Applies to:** `DankAIUsageWidget.qml`, settings
**Amends:** ADR-0021's usage-based quota-bar colors and its rule that the
dropdown's quota colors are unchanged.

## Context

ADR-0021 added an opt-in green to dark red gradient over percent used for
top-bar quota bars. It blended in RGB, so its middle tones turned muddy, and
its dark red end was hard to see on a dark bar track exactly when it mattered.
It colored by amount used only: 60% used with minutes left in a window looked
like 60% used minutes after it opened. The fixed colors could clash with the
user's theme, and the dropdown's allowance bars could not use it at all.

## Decision

- The existing **Color bars by usage** setting (`barUsageColors`, key kept so
  saved choices carry over) becomes the shared **Heatmap bar colors** switch.
  It colors both the top-bar quota bars and the dropdown's allowance bars, and
  the dropdown's view controls toggle it, persisting like Left / Used. It moves
  from the quota-bar options to the Display settings group.
- Gradients blend in OKLab. The fixed palette ends in a brighter red.
- **Colors** (`heatmapPalette`): `fixed` green, yellow, orange, red (default),
  or `theme`, which blends the theme's `primary`, `warning`, and `error`.
- **Heat follows** (`heatmapBasis`): `usage`, percent used (default), or
  `pace`, usage rate against window time elapsed: even pace is the middle of the
  scale and twice even pace or more is the hottest. Elapsed time is floored at a
  tenth of the window so early use does not swing to red. An exhausted quota is
  always the hottest, and quotas without a known window fall back to usage.
- Only bar fills change. Percentages, provider summaries, Advanced rings, and
  the vertical pill keep the ADR-0018 severity colors, and errors still use the
  theme's error color.

## Alternatives

A separate dropdown-only switch would leave two color choices for the same
quotas; a single shared one was preferred first and can be split later.
Coloring by the absolute time remaining was rejected because it ignores how
much quota is left. Deriving heat from projected end-of-window usage directly
would color exact even pace as the hottest value, which reads as an alarm for
normal use.

## Consequences

Users who enabled the old gradient keep it on, now in the dropdown too and with
the new blend and red. Pace heat changes as time passes without new usage. The
theme palette depends on the theme's warning and error being distinguishable
from its accent.
