# Data sources and refresh

## Where the numbers come from

- **Codex limits:** the local Codex app server, via `account/rateLimits/read`.
  Windows are classified by their returned duration, and available banked
  resets are shown with their expiry
  ([ADR-0008](adr/ADR-0008-codex-duration-based-windows-and-banked-resets.md)).
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
