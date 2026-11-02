# ADR-0027: Five-hour windows only where reported, and no statusline fallback

**Status:** Accepted
**Date:** 2026-10-31, updated 2026-11-01 (ADR-0024 retired) and 2026-11-02 (premise corrected: five-hour windows restored where reported, Claude prime restored by ADR-0028)
**History:** [archive/ADR-0027-remove-five-hour-windows-prime-and-statusline-history.md](archive/ADR-0027-remove-five-hour-windows-prime-and-statusline-history.md)
**Applies to:** `cmd/dankaiusage`, `DankAIUsageWidget.qml`, settings
**Retires:** finalises the already superseded
[ADR-0005](archive/ADR-0005-claude-limits-via-statusline-capture.md).
[ADR-0006](archive/ADR-0006-opt-in-claude-prime.md) and
[ADR-0007](archive/ADR-0007-auto-prime-as-window-scheduler.md) stay retired;
Claude prime is governed by
[ADR-0028](ADR-0028-prime-only-an-idle-reported-five-hour-window.md).
**Amends:** ADR-0001 (statusline fallback removed); ADR-0003, ADR-0008,
ADR-0009, ADR-0011, ADR-0012, ADR-0021 and ADR-0022 where they describe
five-hour windows.

## Context

Version 2.0.0 removed every five-hour window on the premise that Anthropic and
OpenAI had removed them. That held only for the accounts observed on
2026-10-31 (a verified Claude Ultra and a Codex Pro account): Claude's OAuth
usage response had `five_hour: null` and a `limits` array with only
`weekly_all` and `weekly_scoped` entries, and the Codex app-server returned
only a 10,080-minute `primary` window with `secondary: null`.

The five-hour window depends on the plan:

- **Claude** dropped the five-hour session limit only for verified Ultra
  accounts (formerly Max 20x), from 2026-10-30. Pro, Max (formerly Max 5x) and
  not yet verified Ultra accounts keep it.
- **Codex** lifted the five-hour limit temporarily in July 2026 and restored
  it for Plus in August; Pro reportedly has none for now.

Users on those plans lost their session window in 2.0.0 to 2.1.1.

The Claude statusline capture (ADR-0005) has been a fallback since ADR-0001
made the OAuth usage endpoint the primary source. It only works while Claude
Code runs as an interactive terminal TUI.

## Decision

- Five-hour windows are provider-defined data (ADR-0009): shown when the
  provider reports one, absent otherwise.
  - Claude: parse `five_hour` and any five-hour `limits` or top-level
    `five_hour_*` entry.
  - Codex: classify each window by duration in either slot (ADR-0008); four
    to six hours is the five-hour window.
  - The helper emits `sessionLeft`, the `general-5-hour` and scoped
    `-5-hour` buckets, and the `session` and `rolling.fiveHours` token
    totals only when a provider reports a five-hour window; the token totals
    then appear for every provider. Without one, its output is the
    weekly-only output of 2.0.0 to 2.1.1.
  - The widget shows the five-hour bar and its `5h` tags, the **5h** token
    range, the **Claude 5-hour session** top-bar switch and the **Scheduled
    5-hour resets** history filter. The range and the switch appear only
    while a five-hour window is reported; a saved 5h range shows 7d until
    then.
- Claude limit resets (ADR-0022, ADR-0023) name and clear the five-hour window
  where a grant includes it. Codex resets (ADR-0010) consider the five-hour
  and weekly windows and ignore windows of other lengths.
- Claude prime and auto-prime return, guarded by the usage response and
  without a local-timer fallback
  ([ADR-0028](ADR-0028-prime-only-an-idle-reported-five-hour-window.md)).
- The Claude statusline fallback stays removed (`claude-statusline` and its
  cache). When the OAuth usage fetch fails, Claude limits are unavailable and
  the error is shown. `dankaiusage claude-statusline` stays as a silent no-op
  that drains its input and prints nothing, so a Claude Code
  `statusLine.command` still pointing at it shows a blank status line rather
  than an error. A statusline cache left by an earlier version is ignored.

## Alternatives Considered

- **Keep the five-hour removal:** rejected once the premise was disproved.
  Pro, Max, unverified Ultra and Codex Plus users would keep missing the
  window that limits them most often.
- **Always emit the session fields, unknown when absent:** what 1.x did.
  Rejected: accounts without the window would see a changed summary and, with
  1.x prime, a local timer that kept priming for nothing.
- **Keep the statusline fallback:** rejected. It is TUI-only, has not been the
  primary source since ADR-0001, and removing it shrinks the surface.

## Consequences

- Accounts without a five-hour window see the same output and widget as in
  2.1.1; accounts with one get it back without a setting.
- If a provider drops or adds the window for an account, the widget follows on
  the next usage refresh; saved choices such as the 5h range and the top-bar
  switch are kept meanwhile.
- Hosts that relied on the statusline fallback (TUI-only setups, macOS
  installs with credentials in the Keychain) show no Claude limits, only the
  usage-fetch error.
- Existing reset history stays readable without migration; stored five-hour
  events follow the restored filter.
