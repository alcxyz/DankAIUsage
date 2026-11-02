# Data sources and refresh

## Where the numbers come from

- **Codex limits:** the local Codex app server, via `account/rateLimits/read`.
  Windows are classified by their returned duration, and available banked
  resets are shown with their expiry
  ([ADR-0008](adr/ADR-0008-codex-duration-based-windows-and-banked-resets.md)).
  A five-hour window (four to six hours) appears only when the snapshot reports
  one, as it does for Codex Plus.
  When the snapshot carries a `credits` object, its prepaid balance (or
  unlimited flag) becomes a **Credits** bucket. The balance is a credit count,
  not money, so it is shown without a currency. Accounts without credits omit
  the object and the bucket.
- **Claude limits:** Anthropic's OAuth usage endpoint, using the local Claude
  Code sign-in ([ADR-0001](adr/ADR-0001-claude-limits-from-oauth-usage-api.md)).
  The helper prefers the structured `spend` object for extra-usage credits and
  falls back to `extra_usage`, deduplicating both into one monetary quota bar.
  A `spend.balance` amount is shown as a prepaid balance: appended to the
  monthly-limit bar when there is one, or as a balance-only bucket without a
  percentage when extra usage is enabled with no monthly limit.
  Granted limit resets come from a separate request to the same endpoint with
  `cedar_ember=1`, made at most once an hour under Claude Code's OAuth
  client identity, which the endpoint requires before it reports them and
  rate-limits; consecutive rate limits back off exponentially up to a day ([ADR-0022](adr/ADR-0022-manual-claude-limit-reset.md),
  [ADR-0023](adr/ADR-0023-separate-claude-reset-availability-check.md)).
  The five-hour window (`five_hour`, or a five-hour entry in `limits`) appears
  only when the response reports one: Claude Pro and Max accounts do, verified
  Claude Ultra accounts do not, and an account without one has no five-hour
  bar or session figures.
  Claude limits come only from this endpoint: when the fetch fails, they are
  unavailable and the error is shown. The `claude-statusline` fallback is
  gone; if your Claude Code settings run `dankaiusage claude-statusline` as
  `statusLine.command`, remove it; the command now prints nothing.
- **Token history:** Codex session/archived-session JSONL and Claude project
  JSONL transcripts from their normal CLI config locations
  ([ADR-0003](adr/ADR-0003-token-history-from-local-cli-artifacts.md)).
  `sqlite3` is not required. Claude token totals are local Claude Code history
  only; usage from claude.ai, mobile, or other online surfaces is not written
  to those transcripts and is not exposed through a Claude CLI usage command.
  The helper keeps what it parsed from each transcript (its absolute path,
  session and record IDs, token counts, timestamps, models and any parse
  error; no message text) in
  `$XDG_STATE_HOME/dankaiusage/transcript-parse-cache.gob`, falling back to
  `~/.local/state/dankaiusage/`, so a refresh reads only new transcript lines.
  Deleting the file is safe; the next refresh rebuilds it with a full scan.
- **CLI availability:** whether `codex` and `claude` are on `PATH`. A provider
  being available means its CLI is present, not necessarily that its account is
  signed in.

For Claude limits the helper reads the Claude Code OAuth token from
`~/.claude/.credentials.json` itself, uses it only for the usage request, and
never prints or logs it. Everything it emits is aggregate local usage and
subscription limit percentages.

## Refresh interval and cooldown

**Usage refresh interval** ranges from three to sixty minutes, with a marked
five-minute default and a reset-to-default action. Longer intervals reduce
regular network requests and local history scans, at the cost of less current
information.

The helper shares a per-provider cooldown across refresh paths and processes
([ADR-0015](adr/ADR-0015-shared-usage-refresh-cooldown.md)). Manual Refresh
can reuse cached quotas; it does not bypass the minimum, and neither does the
optional refresh when the dropdown opens. An already scheduled
cooldown is not shortened by changing the slider; subsequent requests use the
new interval. Provider error backoff may extend the wait. Three minutes is a
conservative minimum, not a guarantee against account restrictions. Claude's
undocumented OAuth usage source has separate policy and compatibility risks
regardless of polling frequency. Longer intervals also delay automatic reset
checks and may miss a credit's expiry window; that feature remains best
effort.

The widget polls `dankaiusage summary` and caches the last successful summary
in DMS plugin state so the bar can render immediately after a shell restart.

## Claude prime

Claude prime is off by default. Turn on **Claude prime** under
**Automation and notifications** to start Claude's five-hour window with one
tiny request when that window is idle. The request spends a little usage:
`claude -p` with the `sonnet` model, tools disabled, no saved
session, a fixed one-word prompt and a budget cap of 0.001 USD.

Prime decides only from the latest usage response the quota poll saved
(`claude-oauth-usage.json`). It never requests usage itself and has no local
timer, so it never assumes a window is running. It sends the request only when
all of these hold:

- the saved response is readable, not invalidated, and its last refresh did
  not fail;
- it was fetched within twice the refresh interval, or within ten minutes if
  that is longer;
- it belongs to the Claude account you are signed in to now;
- it is newer than the last prime, and the last prime is at least 15 minutes old;
- it reports a five-hour window that is not running;
- after a prime, a usage response showed a window running or resetting since
  that prime; if none did, prime waits five hours before trying again;
- it is within the active hours, enough of the weekly limit is left, and the
  daily cap is not reached (the **Claude prime limits** settings).

Otherwise prime skips without spending anything and says why. The reasons are
listed in the [CLI reference](cli.md#claude-prime). Priming happens only while
DankMaterialShell runs, and a prime appears in the widget after the next
scheduled usage poll.

In the widget, while prime is on, the Claude card shows a status line in both
Simple and Advanced, for example "Claude prime is on · idle: this account has
no five-hour window". Advanced also shows a bolt button on the Claude card while
the window is idle; pressing it asks the helper, which applies the same checks.
The helper's last answer appears under **Advanced → Diagnostics**.

Auto-prime asks the helper at most once per refresh interval, and only after a
live refresh whose summary shows an idle five-hour window with current, usable
usage. A `claude-prime.json` timer left by version 1.x is ignored. The decision
and the time of the last prime are kept in `claude-prime-state.json` in the
plugin state directory.
