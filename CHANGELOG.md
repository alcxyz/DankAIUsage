# Changelog

All notable changes to this plugin are documented here. The format follows
Keep a Changelog, and the release workflow publishes each version's section
as its GitHub release notes.

## [Unreleased]

- Added optional brand-colored provider logos and quota-bar colors that follow usage from green to dark red. Both default off and preserve error colors. Thanks to @TheFacc for the contribution (#15).
- Added optional quota bars in the horizontal top bar, with provider names when logos are hidden, configurable labels and width, and a pace marker. Credit switches apply to text mode; quota-bar mode keeps credits in the dropdown. Thanks to @TheFacc for the contribution (#14).
- Advanced view lists every available Codex reset with its expiry date and countdown, soonest first. Thanks to @TheFacc for the contribution (#12).
- Added an optional refresh when opening the dropdown, limited to once every 30 seconds and subject to the shared provider cooldown. Thanks to @TheFacc for the contribution (#13).

## [1.2.0] - 2026-09-24

- Prepaid credit balances now appear as a **Credits** bucket for Codex and Claude. A balance without a monthly limit shows its amount instead of a percentage, has no progress bar, and never counts as the most constrained quota. A new **Codex credits** switch adds the balance to the top bar.
- Reset history and public announcements show the latest four entries, with a **Show more** control that reveals the next four. The page resets when the dropdown closes.
- Unread badges now stay in sync across multiple bar instances. Clearing a section in one dropdown no longer leaves the others stuck on old counts.
- Added an opt-out **Desktop notifications** setting: reset history changes and new public reset announcements raise a `notify-send` notification once, never for already-read items, first-time tracking, history older than a day, or a stale feed. While on, it replaces the upcoming-reset toast.

For Nix release packaging, use `github:alcxyz/DankAIUsage/v1.2.0#release`. Manual release packaging is available from a clean `v1.2.0` checkout with `python3 scripts/package.py --release --output dist/release`. Install the packaged plugin directory together with its helper.

## [1.1.0] - 2026-09-20

- Added subtle hourly and daily ticks to the window-time bars, plus weekly countdowns with hours and minutes and full timing details on hover.
- Reset-history and announcement badges now clear after their content is viewed.
- Consolidated quota observations associated with a confirmed plugin reset into one history entry.
- Hid the advanced auto-use reset control when no spendable reset is available, while keeping opt-in available in settings.
- Added development build identities with matching plugin/helper versions. Official releases retain plain release versions.

For Nix release packaging, use `github:alcxyz/DankAIUsage/v1.1.0#release`. Manual release packaging is available from a clean `v1.1.0` checkout with `python3 scripts/package.py --release --output dist/release`. Install the packaged plugin directory together with its helper.

## [1.0.0] - 2026-09-13

- Refreshed Simple and Advanced dropdowns, reorganized settings, and independent Claude weekly indicators on the bar.
- Added reset countdowns and time-progress bars that follow the Left/Used preference.
- Added filterable reset history with clearer explanations for reset-time changes without a quota refill.
- Consolidated local token history into one shared selector, with separate input/cached/output counts and locale-aware dates.
- Added private, bounded XDG diagnostics for troubleshooting and improved shared refresh cooldown handling.
- Three updated screenshots: Advanced, Simple, and the bar pill.

### Public reset announcements (Alpha)

Public reports, advance alerts, and local reset matching through TokenResets are experimental, opt-in, and disabled by default. Coverage and matching can be incomplete or incorrect; announcements do not guarantee eligibility for your account. Do not rely on them to decide when to spend quota or redeem resets. The feed host sees your IP address and ordinary request metadata; no account data is sent. The Alpha designation applies only to this optional integration, not to the overall 1.0.0 release.

## [0.8.0] - 2026-09-11

- Unexpected quota changes can now be explained directly in the dropdown, with optional local notes and editable history context.
- Added a configurable usage refresh interval from 3 to 60 minutes, with a marked 5-minute default.
- Provider cooldowns and retry backoff are now shared across refreshes and helper processes, and automatic reset/session checks are coordinated with cached usage.
- Minor reset-time jitter is now ignored instead of asking misleading reset questions.
- Documented an observed subscription-upgrade reset experience, clearly distinguished from guaranteed provider behavior.

Manual Refresh may reuse cached quotas. The polling minimum is conservative, not a guarantee against account restrictions; longer intervals can delay automatic checks or miss reset expiry. Saved explanations use history format v2, which older helpers cannot read. No new runtime dependencies.

## [0.7.0] - 2026-09-08

- Added Simple and Advanced dropdown modes, remembered between sessions; new installations start in Simple, existing installations keep Advanced.
- Added compact single-button toggles for Simple/Advanced and Left/Used.
- Added local token history ranges of 5 hours, 7 days, 30 days, and 90 days, preserving existing custom ranges.
- Added optional tracked token totals that survive transcript cleanup, with a tracking start date, pause/resume, and confirmed clear; tracking stays off by default and is not presented as account-wide all-time usage.
- Fixed Codex token history to read from retained CLI transcripts instead of the obsolete SQLite log format, fixing empty local counts.
- Deduplicated local usage updates and preserved tracked checkpoints across restarts and copied or removed transcripts.
- Kept armed reset controls, reset errors, and uncertain outcomes visible in Simple mode without changing background tracking or automation.
- Removed the SQLite dependency; the widget still requires the `dankaiusage` helper and the provider CLIs.

## [0.6.0] - 2026-09-08

- Added reset history: inspect recent Codex and Claude quota changes in the dropdown, including scheduled rollovers, unexpected replenishment, likely earned-reset redemptions, and explicit plugin reset outcomes.
- Simplified bar labels: both bar layouts now show quota percentages without repeating "left" or "used", while the dropdown still makes the selected mode clear.
- The "Auto-use one reset" control now uses a visible native toggle, with keyboard access and a focus indicator; the underlying state and one-shot safety are unchanged.
- Updated dropdown and bar screenshots to match the current interface.

History is local and bounded to 200 events over 30 days, with the latest eight shown in the dropdown; it starts fresh from new observations and cannot reconstruct earlier resets. Inferred events are labeled as such, since an unexpected refill does not prove a provider-granted reset. Install the widget and helper from the same revision.

## [0.5.0] - 2026-09-08

- Added an opt-in, off-by-default "Auto-use one reset" control for Codex that automatically redeems the earliest-expiring available reset with a known ID and expiry. It waits until general Codex usage reaches 99%, or until ten minutes before a reset expires with some allowance already used, and avoids triggering when a natural quota reset is already imminent.
- The control disarms itself after its single redemption attempt, including on failure, and never purchases credits or substitutes a different reset; only the provider decides whether a window is eligible to reset.
- The armed/disarmed state is owned by the helper rather than the widget, so restarting DMS does not forget an armed reset and multiple widget instances cannot independently redeem it. The state can also be inspected and controlled from the terminal.
- Requires a Codex CLI that exposes the documented earned-reset app-server method; unsupported CLI versions show an error instead of using an undocumented endpoint.

## [0.4.0] - 2026-09-07

- Replaced the fixed session/weekly grid with a provider-defined list of quota bars: Codex shows its general subscription allowance as a weekly window, and Claude shows five-hour, weekly, model-scoped, and extra-usage credit limits as the provider reports them. Missing buckets are omitted instead of inferred from their position in an API response.
- Added a Left/Used toggle in the dropdown that switches all quota percentages and progress bars together, including credits, while warning colors continue to reflect proximity to the limit regardless of display mode.
- Added dropdown "Bar controls" for immediate toggles of compact mode, provider logos, the plugin icon, and which Claude quota (session/weekly/credits) appears on the bar, saved the same as plugin settings without needing a data refresh.
- Documented Nix and manual installation, provider CLI setup, and troubleshooting steps (including how to recover from a signed-out Claude CLI) in the README.

## [0.3.0] - 2026-08-30

- Replaced token-based allowance estimates with authoritative limits: Codex quotas now come from the local Codex app server's rate-limit endpoint, and Claude quotas from the cached Claude Code statusline data, so displayed percentages match actual provider limits instead of derived guesses.
- Added a Session/Weekly focus toggle so the compact bar pill shows whichever window matters most instead of always blending both.
- Switched Claude's primary limit source to Anthropic's OAuth usage API, since the statusline route only updates while Claude Code runs as a terminal TUI and otherwise goes stale in SDK/IDE sessions; the statusline cache remains a fallback.
- Added an opt-in "Claude prime" toggle that makes small requests to start a Claude session window early, guarded against redundant priming (skipping when an active session window is already known, enforcing a 15-minute floor between requests, and refreshing cached usage right after priming), with a local timer fallback when account data is unavailable.
- Reduced noise from cached tokens in displayed Claude usage totals and reduced the request overhead of priming.
- Added input/output token breakdowns and the reset date alongside the weekly reset time.

## [0.1.0] - 2026-06-02

- Initial release: a DankMaterialShell widget showing remaining Codex and Claude session and weekly allowance, estimated from local CLI token usage logs, with token totals as a secondary detail.
- Ships a `dankaiusage` Go helper that reads Codex's local SQLite usage log and Claude's project JSONL transcripts to compute usage, and reports whether the `codex`, `claude`, and `sqlite3` CLIs are available.
- Per-provider session and weekly allowance limits are configurable in plugin settings, since neither provider exposed authoritative remaining quota through local CLI or status files at the time.
