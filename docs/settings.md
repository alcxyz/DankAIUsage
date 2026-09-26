# Settings

Open **Settings → Plugins → AI Usage**. Left / Used and Simple / Advanced stay
in the dropdown; everything persistent about the bar lives here.

## Refresh and providers

- **Usage refresh interval:** three to sixty minutes, five-minute default,
  with a reset-to-default action. See
  [Refresh interval and cooldown](data-sources.md#refresh-interval-and-cooldown)
  for how the shared cooldown behaves.
- **Refresh when the dropdown opens:** off by default. Opening the dropdown
  runs the same refresh as its Refresh button, at most every 30 seconds. The
  shared cooldown still applies, so inside the refresh interval this reloads
  cached quotas and local token history without contacting providers.
- **Show Codex / Show Claude:** disable providers you do not use.
- **Token history period:** the configured range stays available in the
  dropdown's range selector when it differs from the presets.
- **Include cached tokens:** controls combined totals; the Input / Cached /
  Output split is always shown.

## Top bar layout and icons

Choose the plugin icon, provider logos, both, or neither. **Compact mode**
keeps one selected quota per enabled provider rather than hiding a provider.
When provider logos are off, provider names identify each group in both text
and quota-bar modes.

**Quota bars instead of text** (off by default) replaces the percentages in the
horizontal bar with small stacked bars per provider: five-hour on top, then
weekly and model-scoped limits. Bars follow Left / Used and turn warning or
error colored when a quota runs low, like the text. Claude follows the quota
choices below; Codex shows all its non-credit quotas. Credits stay in the
dropdown, even when the Codex or Claude credit switch is enabled. Those
switches retain their saved values for text mode.
Compact mode does not apply, and the vertical bar is unchanged. Options:

- **Quota bar width:** 16–120 px, default 40.
- **Quota bar label: left / right:** none, a quota tag (`5h`, `w`, or the
  model initial such as `f` for Fable weekly; longer only when two would
  clash), the reset countdown (`2h05`, `4d23h`; elapsed window time with
  Used), or the percentage.
- **Quota bar pace marker:** a tick at the even-pace point for the time passed
  in each window. With Left, fill short of the tick means you are using the
  quota faster than the window is passing; with Used, fill beyond it.

See [ADR-0021](adr/ADR-0021-top-bar-quota-bars.md).

## Codex credits in the top bar

**Codex credits** adds the prepaid credit balance next to the most constrained
Codex quota in text mode when the account reports one. Off by default. It does
not add credits to quota-bar mode.

## Claude quotas in the top bar

Select session, weekly, and credits independently. Each reported weekly limit
has its own switch: **Weekly (all models)** and model-specific limits such as
Fable. Show either, both, or neither. Choices are remembered by quota ID,
including when a limit temporarily disappears. Compact mode selects only among
enabled quotas. The weekly default applies to limits without an individual
choice and preserves existing preferences on upgrade. All available quotas
remain visible in the dropdown.
The Claude credits switch applies to text mode; quota-bar mode omits credits.

## Automation and opt-ins

- **Enable Claude prime:** off by default; starts session windows with a tiny
  request that consumes usage. See [Claude prime](data-sources.md#claude-prime).
- **Public reset announcements (Alpha):** off by default; reads a third-party
  public feed. See
  [Public reset announcements](reset-history.md#public-reset-announcements-alpha-optional).
- **Desktop notifications:** on by default; sends a `notify-send` notification
  for new reset history changes and new public reset announcements. See
  [Notifications](reset-history.md#notifications).

## Reset history filters

**Scheduled 5-hour resets**, **Scheduled weekly resets**, and **Other reset
events** control which categories the Advanced history list shows. See
[Filters](reset-history.md#filters).
