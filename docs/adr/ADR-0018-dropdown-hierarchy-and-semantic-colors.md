# ADR-0018: Dropdown information hierarchy and semantic status colors

**Status:** Accepted
**Date:** 2026-09-12
**Applies to:** `DankAIUsageWidget.qml`

## Context

The dropdown grew feature by feature. Secondary sections (bar controls, reset
history, diagnostics, public announcements) appeared in different places with
different styling, the "Advanced" and "Left" chips did not make the inactive
choice visible, and every provider note was rendered in one amber block, so a
signed-out account looked the same as a routine local-history caveat. Quota
rows spent a separate line on the reset time, and the Advanced view could
exceed screen height with no way to scroll. The 2026-09-07 UI review recorded
the amber-note and fixed-green findings; the hard-coded colors also ignored
the DMS theme.

## Decision

- Order the dropdown top to bottom as: header with freshness status and
  refresh, view controls, attention items (errors, the "What changed?" prompt,
  upcoming verified public resets), the Advanced overview, provider cards, and
  finally an Advanced group of collapsible disclosure rows for reset history,
  public reset announcements, and diagnostics. Persistent bar layout and quota
  selection controls live only in Settings → Plugins, including per-model
  Claude weekly choices; view switches remain available in the dropdown.
- Disclosure badges count unread displayed history events and public
  announcements. Mark displayed items read while their section and the popout
  are open, including items arriving during that view. Persist bounded read
  identities across restarts; hidden or filtered-out items remain unread.
  Announcement revisions count as new, while edits to a history explanation
  do not. Reading never removes content or changes public notification receipts.
  Read state lives in shared plugin state; every widget instance reloads it on
  the plugin-state change signal so multi-bar setups agree on badge counts.
- New displayed history events and announcements may raise a desktop
  notification (opt-out setting). Notify each item at most once across
  restarts and instances, never for items already read, never on first
  tracking, never for history older than a day, and never from a stale feed.
- Use two-state segmented switches for Simple / Advanced and Left / Used. Both
  labels stay visible; clicking the inactive segment switches. Each switch still
  has a single persisted toggle action, so the ADR-0013 contracts are unchanged.
- Render each quota as label, inline countdown or detail, severity-colored value,
  allowance bar, and (Advanced) the ADR-0013 window-time bar beneath it. The exact
  reset time stays on hover.
- Keep one shared token-history range selector above the provider cards in
  Advanced. Provider token rows display the selected range and their own results
  without duplicating controls or implying independent ranges.
- Classify provider notices as info, warning, or error and render each on its own
  line with an icon. Info uses the neutral variant text color; warnings and errors
  use the theme's `warning` and `error` colors. An unavailable provider with no
  quota data shows a sign-in hint instead of a generic "unavailable" line.
- Derive all status colors from the DMS theme: `primary` for healthy, `warning`
  at or below 25% remaining, `error` at or below 10% remaining or on failure.
  Provider header summaries and top-bar percentages use the same severity
  scale; there is no fixed per-provider color.
- Cap the popout at a maximum height and scroll its content instead of growing
  without bound.
- Show the most constrained allowance in the top bar's vertical pill instead of
  a token total, so both pill orientations report quota state.

## Alternatives

Independent per-section visibility settings would add configuration for a
presentation-only concern (rejected in ADR-0013). Keeping single-label chips
avoids two extra labels but leaves the inactive state implicit. Per-provider
brand colors are recognisable but conflict with severity coloring and with
user themes.

## Consequences

Screenshots in `docs/` predate this layout and need recapturing before the
next listing update. Tests continue to pin the persisted actions and
visibility gates rather than the visual layout. New notices must choose a
level; unknown levels fall back to info styling.
