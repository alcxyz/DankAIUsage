# Settings

Open **Settings → Plugins → AI Usage**. Left / Used, Simple / Advanced, and the
heatmap switch stay in the dropdown; everything persistent about the bar lives here. The page is
organised into the groups below. Groups only change what is shown: hiding or
collapsing a group never changes a saved value.

## Display

- **Show Codex / Show Claude:** disable providers you do not use.
- **Provider order:** whether Codex or Claude comes first in the top bar, the
  provider cards, and the per-provider token lines.
- **Show used allowance:** percentages and bar fill show what is used instead
  of what is left, in the bar and the dropdown. The dropdown's Left / Used
  switch changes the same setting.
- **Heatmap bar colors** (off by default) shades quota-bar fills in the top bar
  and the dropdown from cool to hot instead of the theme's severity colors.
  The dropdown's flame button changes the same setting. Percentages, provider
  summaries, and the vertical pill keep the severity colors, and errors still
  use the theme's error color. While it is on, **Heatmap options** offer:
  - **Colors:** green through yellow and orange to red (default), or the
    theme's accent, warning, and error colors. Fixed colors may have lower
    contrast with some light or custom themes.
  - **Heat follows:** **Usage** (default) colors by percent used: yellow at
    50%, orange at 75%, red at 100%. **Pace** colors by how fast a quota is
    used for the time passed in its window: even pace is the middle color and
    twice even pace or more is red, so heavy use late in a window stays calm
    while a fast start shows hot. The first tenth of a window counts as a
    tenth, an empty quota is always red, and quotas without a window, such as
    credits, follow usage.

  See [ADR-0026](adr/ADR-0026-shared-quota-bar-heatmap.md).
- **Include cached tokens:** controls combined totals; the Input / Cached /
  Output split is always shown.

## Top bar

Layout of the horizontal pill. The vertical pill always shows the most
constrained quota.

- **Provider logos:** when off, provider names identify each group in both
  text and quota-bar modes.
- **Brand-colored logos** (off by default) draws the Claude logo orange and the
  OpenAI logo white (black on light themes) instead of the theme accent, in
  both the bar and dropdown. Unavailable providers still use the theme's error
  color.
- **Plugin icon:** the generic monitoring icon at the start of the pill.
- **Quota bars instead of text** (off by default) replaces the percentages in
  the horizontal bar with small stacked bars per provider: five-hour on top,
  then weekly and model-scoped limits. Bars follow Left / Used and turn
  warning or error colored when a quota runs low, like the text, unless
  **Heatmap bar colors** is on. Claude
  follows the quota choices in the next group; Codex shows all its non-credit
  quotas. The Codex and Claude credit switches add a `cr` credit bar; see
  [Credit bars](dropdown.md#credit-bars). The vertical bar is unchanged.
- **Compact pill** (text mode only, so it is shown while quota bars are off)
  keeps one selected quota per enabled provider rather than hiding a provider.
  Its saved value is kept while quota bars are on.

**Quota bar options** appear while quota bars are on. Their saved values are
kept while bars are off.

- **Bar width:** 16–120 px, default 40.
- **Left label / Right label:** none, a quota tag (`5h`, `w`, or the model
  initial such as `f` for Fable weekly; longer only when two would clash), the
  reset countdown (`2h05`, `4d23h`; elapsed window time with Used), or the
  percentage. Credit bars have no reset, so the countdown slot shows their
  amount left instead (`850`, `1.2k`; the amount used with Used).
- **Pace marker:** a tick at the even-pace point for the time passed in each
  window. With Left, fill short of the tick means you are using the quota
  faster than the window is passing; with Used, fill beyond it.

See [ADR-0021](adr/ADR-0021-top-bar-quota-bars.md).

## Quotas in the top bar

Which limits each provider contributes to the pill. All available quotas
remain visible in the dropdown.

- **Claude 5-hour session**, **New Claude weekly limits by default**, and one
  switch per **reported Claude weekly limit**: **Weekly (all models)** and
  model-specific limits such as Fable. Show either, both, or neither. Choices
  are remembered by quota ID, including when a limit temporarily disappears.
  Compact mode selects only among enabled quotas. The weekly default applies
  to limits without an individual choice and preserves existing preferences
  on upgrade.
- **Claude extra-use credits** and **Codex credits** add the credit balance
  next to the provider's quotas in text mode, or a credit bar in quota-bar
  mode, when the account reports one. Both are off by default.

## Collection

- **Usage refresh interval:** three to sixty minutes, five-minute default,
  with a reset-to-default action. It paces quota polling, local history
  scans, and the check of an armed Codex reset, so long intervals can miss a
  brief expiry window. Claude limit resets are checked separately, at least
  30 minutes apart and never closer than two intervals, because Claude
  rate-limits that check ([ADR-0023](adr/ADR-0023-separate-claude-reset-availability-check.md)).
  See [Refresh interval and cooldown](data-sources.md#refresh-interval-and-cooldown)
  for how the shared cooldown behaves.
- **Refresh when the dropdown opens:** off by default. Opening the dropdown
  runs the same refresh cycle as its Refresh button, at most every 30
  seconds. Quotas are fetched only once the refresh interval has passed;
  like any cycle it may run a Claude reset check that is due or the check of
  an armed Codex reset.

## Codex earned reset

- **Auto-use one reset:** off by default; uses one eligible earned reset at
  99% general Codex usage or shortly before it expires, then turns itself
  off. DMS must be running. The armed state, its expiry, and a cancel action
  are always shown here and in the dropdown. See
  [ADR-0010](adr/ADR-0010-one-shot-codex-reset.md).

## Automation and notifications

Collapsed by default; expand the heading to show it.

- **Enable Claude prime:** off by default; starts session windows with a tiny
  request that consumes usage. See [Claude prime](data-sources.md#claude-prime).
- **Public reset announcements (Alpha):** off by default; reads a third-party
  public feed. See
  [Public reset announcements](reset-history.md#public-reset-announcements-alpha-optional).
- **Desktop notifications:** on by default; sends a `notify-send` notification
  for new reset history changes and new public reset announcements. See
  [Notifications](reset-history.md#notifications).

The legacy token history period (`periodDays`) no longer has a control; a
previously saved value still appears in the dropdown's range selector when it
differs from the presets.

## Reset history filters

**Scheduled 5-hour resets**, **Scheduled weekly resets**, and **Other reset
events** control which categories the Advanced history list shows. See
[Filters](reset-history.md#filters).
