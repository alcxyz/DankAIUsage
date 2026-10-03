# Settings

Open **Settings → Plugins → AI Usage**. Left / Used and Simple / Advanced stay
in the dropdown; everything persistent about the bar lives here. The page is
organised into the groups below. Groups only change what is shown: hiding or
collapsing a group never changes a saved value.

## Display

- **Show Codex / Show Claude:** disable providers you do not use.
- **Show used allowance:** percentages and bar fill show what is used instead
  of what is left, in the bar and the dropdown. The dropdown's Left / Used
  switch changes the same setting.

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
  warning or error colored when a quota runs low, like the text. Claude
  follows the quota choices in the next group; Codex shows all its non-credit
  quotas. Credits stay in the dropdown, even when the Codex or Claude credit
  switch is enabled. Those switches retain their saved values for text mode.
  The vertical bar is unchanged.
- **Compact pill** (text mode only, so it is shown while quota bars are off)
  keeps one selected quota per enabled provider rather than hiding a provider.
  Its saved value is kept while quota bars are on.

**Quota bar options** appear while quota bars are on. Their saved values are
kept while bars are off.

- **Bar width:** 16–120 px, default 40.
- **Color bars by usage:** off by default. Fills go from green through
  yellow (50% used) and orange (75%) to dark red (100%) instead of the theme's
  severity colors. This only affects horizontal quota bars; dropdown quota
  colors stay unchanged, and errors still use the theme's error color. Fixed
  colors may have lower contrast with some light or custom themes.
- **Left label / Right label:** none, a quota tag (`5h`, `w`, or the model
  initial such as `f` for Fable weekly; longer only when two would clash), the
  reset countdown (`2h05`, `4d23h`; elapsed window time with Used), or the
  percentage.
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
  next to the provider's quotas in text mode when the account reports one.
  Both are off by default and do not add credits to quota-bar mode.

## Collection

- **Usage refresh interval:** three to sixty minutes, five-minute default,
  with a reset-to-default action. See
  [Refresh interval and cooldown](data-sources.md#refresh-interval-and-cooldown)
  for how the shared cooldown behaves.
- **Refresh when the dropdown opens:** off by default. Opening the dropdown
  runs the same refresh as its Refresh button, at most every 30 seconds. The
  shared cooldown still applies, so inside the refresh interval this reloads
  cached quotas and local token history without contacting providers.

## Automation and notifications

- **Codex earned reset (Auto-use one reset):** off by default; uses one
  eligible earned reset, then turns itself off. The armed state, its expiry,
  and a cancel action are always shown here and in the dropdown. See
  [ADR-0010](adr/ADR-0010-one-shot-codex-reset.md).
- **Enable Claude prime:** off by default; starts session windows with a tiny
  request that consumes usage. See [Claude prime](data-sources.md#claude-prime).
- **Public reset announcements (Alpha):** off by default; reads a third-party
  public feed. See
  [Public reset announcements](reset-history.md#public-reset-announcements-alpha-optional).
- **Desktop notifications:** on by default; sends a `notify-send` notification
  for new reset history changes and new public reset announcements. See
  [Notifications](reset-history.md#notifications).

## Token history and compatibility

Collapsed by default; expand the heading to show it.

- **Include cached tokens:** controls combined totals; the Input / Cached /
  Output split is always shown.
- **Legacy token history (days):** kept for older cached summaries. The
  configured range stays available in the dropdown's range selector when it
  differs from the presets.

## Reset history filters

**Scheduled 5-hour resets**, **Scheduled weekly resets**, and **Other reset
events** control which categories the Advanced history list shows. See
[Filters](reset-history.md#filters).
