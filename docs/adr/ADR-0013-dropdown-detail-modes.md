# ADR-0013: Simple and Advanced dropdown modes

**Status:** Accepted
**Date:** 2026-09-08
**Applies to:** widget dropdown

## Context

Token history and automation controls are useful for detailed inspection but
make the default quota view busy. Users want an immediate way to choose detail
without changing collection or their topbar layout.

## Decision

- Provide a remembered Simple / Advanced switch directly in the dropdown.
- Simple shows provider quota bars, percentages, reset countdowns, and warnings.
  Advanced also shows the overall summary, token history and tracking controls,
  available reset details, reset history, automation controls, and bar controls.
- Default new installations to Simple. When no mode has been saved, an existing
  cached provider summary identifies an upgrade and preserves Advanced. Persist
  that choice before collecting the first summary so new installs stay Simple.
- This is presentation only: switching never changes tracking, automation,
  provider visibility, or bar settings. Clear-data confirmation is cancelled
  when switching modes, without clearing data.
- Reset labels use a local countdown (or elapsed window time in Used mode),
  with the exact local reset time on hover. Advanced adds a thin, muted time
  progress bar only when the allowance carries a valid window duration and
  the reset falls within that window. Its direction follows Left / Used;
  it is separate from quota consumption. An overdue reset remains awaiting
  fresh data rather than implying a refill or extrapolating another window.
- Keep armed Codex reset controls visible and cancellable in Simple. Also show
  errors, unknown reset state, and uncertain attempts. Indicate enabled Claude
  session scheduling even when its controls are hidden.

## Alternatives

Independent visibility settings for every section provide more customization
but add configuration and testing complexity. Hiding details only in settings
makes discovery harder. Two local presentation modes offer a small, reversible
choice while retaining safety-relevant state in both views.
