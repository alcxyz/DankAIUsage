# Settings

Open **Settings → Plugins → AI Usage**. Left / Used and Simple / Advanced stay
in the dropdown; everything persistent about the bar lives here.

## Refresh and providers

- **Usage refresh interval:** three to sixty minutes, five-minute default,
  with a reset-to-default action. See
  [Refresh interval and cooldown](data-sources.md#refresh-interval-and-cooldown)
  for how the shared cooldown behaves.
- **Show Codex / Show Claude:** disable providers you do not use.
- **Token history period:** the configured range stays available in the
  dropdown's range selector when it differs from the presets.
- **Include cached tokens:** controls combined totals; the Input / Cached /
  Output split is always shown.

## Top bar layout and icons

Choose the plugin icon, provider logos, both, or neither. **Compact mode**
keeps one selected quota per enabled provider rather than hiding a provider.

## Claude quotas in the top bar

Select session, weekly, and credits independently. Each reported weekly limit
has its own switch: **Weekly (all models)** and model-specific limits such as
Fable. Show either, both, or neither. Choices are remembered by quota ID,
including when a limit temporarily disappears. Compact mode selects only among
enabled quotas. The weekly default applies to limits without an individual
choice and preserves existing preferences on upgrade. All available quotas
remain visible in the dropdown.

## Automation and opt-ins

- **Enable Claude prime:** off by default; starts session windows with a tiny
  request that consumes usage. See [Claude prime](data-sources.md#claude-prime).
- **Public reset announcements (Alpha):** off by default; reads a third-party
  public feed. See
  [Public reset announcements](reset-history.md#public-reset-announcements-alpha-optional).

## Reset history filters

**Scheduled 5-hour resets**, **Scheduled weekly resets**, and **Other reset
events** control which categories the Advanced history list shows. See
[Filters](reset-history.md#filters).
