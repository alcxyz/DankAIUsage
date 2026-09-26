# The dropdown

This guide covers what the AI Usage dropdown shows and how to read it. For
settings that change the top bar, see [Settings](settings.md).

## Simple and Advanced

The dropdown has a remembered **Simple / Advanced** segmented switch under the
header; both labels are visible and the active one is highlighted.

- **Simple** focuses on provider quota bars, percentages, reset countdowns,
  and warnings.
- **Advanced** adds the overall summary with an allowance ring, local token
  history and tracking controls, available reset details, and a set of
  collapsible sections at the bottom: reset history, public reset
  announcements (Alpha), and diagnostics.

New installations start in Simple; existing installations with a cached usage
summary retain Advanced on upgrade. Either choice leaves the top-bar layout,
tracking, and automation settings unchanged. Armed Codex resets remain visible
and cancellable in Simple, as do reset errors or unknown outcomes. Enabled
Claude session scheduling is also indicated there.

The header line shows when usage was last updated, whether it is stale, and
(in Advanced) the next scheduled refresh. Failures and prompts that need a
decision appear above the provider cards. Routine information such as cached
usage or local-history caveats uses neutral styling; sign-in or refresh
failures use warning and error colors. The dropdown scrolls when it would
otherwise exceed the screen.

## Quota bars

The main display is a provider-defined list of quota bars rather than a fixed
session/weekly grid. Codex may expose its general subscription allowance as a
weekly-only window alongside separate model-scoped limits. Claude exposes
five-hour, weekly, model-scoped, and extra-usage credit limits. Missing buckets
are omitted instead of inferred from their position in an API response.

Prepaid credits appear as a **Credits** bucket for either provider when the
account reports a balance: Codex from its rate-limit snapshot, Claude from
extra-usage spend. A balance without a monthly limit shows the amount instead
of a percentage and has no progress bar, because there is no window to fill.

Bar and percentage colors follow remaining allowance: the theme's primary color
while healthy, warning at or below 25% remaining, and error at or below 10% or
on failure. The provider header shows its most constrained limit in the same
colors.

### Left / Used

Use the **Left / Used** segmented switch to switch all quota percentages and
progress bars together, including credits. Left is the default: 26% left fills
26% of the bar; Used shows 74% used and fills 74%. Credit details show the
remaining balance or spending against the budget. Warning colors always reflect
proximity to the limit, regardless of display mode. Both bar layouts show only
the percentage, without repeating "left" or "used"; the dropdown retains those
labels.

### Reset countdowns

Each quota row shows a locally updated countdown inline after its label; hover
the row for the exact reset date and local time. Advanced mode also shows a
thin, muted time progress bar under the quota bar when the window duration is
known. Left shows time remaining; Used shows elapsed window time. This is
separate from the colored quota bar. Unknown durations omit time progress, and
overdue resets say “Reset due · awaiting update” until fresh data arrives.
These updates make no provider requests.

Widget dates and clock times use the user's Qt locale, including date order and
12/24-hour conventions. The plugin does not infer a locale from the timezone or
change system settings. For example, an English interface can use a Norwegian
time locale. Diagnostic exports retain unambiguous UTC timestamps.

### Available Codex resets

Advanced shows banked Codex resets under the Codex card. With one reset, the
line gives its title and expiry. With several, each gets its own line with its
expiry date and a countdown, soonest first; a title shared by all of them is
shown once in the header line, otherwise each line carries its own. Expired or
unreported expiries are labelled as such.

### Why does Spark have two bars?

Spark has [separate usage limits](https://learn.chatgpt.com/docs/agent-configuration/speed#codex-spark).
When Codex reports both five-hour and weekly Spark windows, the plugin shows
both, independently of the general Codex allowance. These are live provider
buckets, not hardcoded legacy quotas. A window disappears when the provider
stops reporting it; identical percentages alone do not make two windows
duplicates.

## Local tokens

Token totals are a secondary detail. Local history shows **Input / Cached /
Output**: Input excludes cached tokens, and Cached always appears separately.
This avoids counting Codex's cached subset twice and distinguishes Claude's
additive cache accounting. **Include cached tokens** controls combined totals,
not this split. Large cached counts describe repeatedly processed context, not
new text output.

Click the **Local tokens** row to choose **5h**, **7d**, **30d**, or **90d**
directly. These are rolling ranges across local conversations, not
active-conversation totals or subscription reset windows. The configured
history range remains available when it differs from the presets. The
selection is remembered and does not make another provider request. The row
supports keyboard activation and shows unavailable or partial collection
explicitly.

In Advanced mode, the token-history selector above the provider cards controls
one shared range. The Codex and Claude rows show read-only results for that
range, including its label; they do not have separate selectors.

Claude token totals are local Claude Code history only; usage from claude.ai,
mobile, or other online surfaces is not written to those transcripts.

### Optional tracked totals

**Tracked total** is a separate, opt-in view. Tracking is **off by default**.
Enable it in the token-range controls to seed a persistent total from retained
Codex and Claude transcripts, without a 90-day cutoff. The start date records
when tracking was enabled (labelled **Tracking enabled**); the seed can include
older usage. Missing, deleted, or remote history cannot be recovered, so this
is not called all-time usage.

While enabled, normal refreshes add newly observed usage without counting the
same events again. Saved totals survive deletion of the original transcripts.
**Pause** retains the total and its duplicate-detection checkpoints; resuming
does not backfill the explicitly paused interval. **Clear** requires
confirmation, removes only tracking totals/checkpoints, and turns tracking
off. It does not delete CLI transcripts, plugin preferences, or quota reset
history.

Tracking is local, with no extra database, service, or provider calls for its
controls. It stores token counters, dates, and hashed event checkpoints, not
prompts, credentials, raw session identifiers, or transcript contents. Partial
seed history is disclosed rather than silently presented as complete. Enabled
tracking scans retained transcripts, so it can take longer than the rolling
views; checkpoint storage grows with observed usage. Usage deleted before a
refresh observes it cannot be preserved. Late or changed Codex checkpoints
behind an already observed session timestamp are conservatively skipped and
flagged as partial coverage to avoid recounting.

Tracking state lives in `$XDG_STATE_HOME/dankaiusage/token-tracking.json`,
falling back to `~/.local/state/dankaiusage/token-tracking.json`. Keep it if
you want the tracked period to survive a configuration reinstall; it is
separate from both the quota-reset observation log and provider transcripts.
Terminal controls are listed in the [CLI reference](cli.md#tracking). See
[ADR-0012](adr/ADR-0012-optional-persistent-token-totals.md) for the tracking
scope and checkpoint policy.

## Top bar

The top bar uses provider logos. Claude's five-hour, weekly, and extra-usage
credit values can each be enabled independently in plugin settings, and the
Codex prepaid credit balance has its own switch; these
choices do not remove any quota bars from the dropdown. Compact mode retains
one selected quota for each enabled provider rather than hiding a provider.
The generic plugin icon and provider logos are independently configurable, so
the bar can show either icon style, both styles, or text only. Each provider's
percentage takes the warning or error color when its most constrained shown
quota runs low.

An optional quota-bar mode replaces the text with small stacked bars per
provider; see [Settings](settings.md#top-bar-layout-and-icons).
