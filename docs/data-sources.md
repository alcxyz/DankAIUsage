# Data sources and refresh

## Where the numbers come from

- **Codex limits:** the local Codex app server, via `account/rateLimits/read`.
  Windows are classified by their returned duration, and available banked
  resets are shown with their expiry
  ([ADR-0008](adr/ADR-0008-codex-duration-based-windows-and-banked-resets.md)).
- **Claude limits:** Anthropic's OAuth usage endpoint, using the local Claude
  Code sign-in ([ADR-0001](adr/ADR-0001-claude-limits-from-oauth-usage-api.md)).
  The helper prefers the structured `spend` object for extra-usage credits and
  falls back to `extra_usage`, deduplicating both into one monetary quota bar.
  The statusline JSON cached by `dankaiusage claude-statusline` is the
  fallback source (see below).
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
subscription-window percentages.

## Refresh interval and cooldown

**Usage refresh interval** ranges from three to sixty minutes, with a marked
five-minute default and a reset-to-default action. Longer intervals reduce
regular network requests and local history scans, at the cost of less current
information.

The helper shares a per-provider cooldown across refresh paths and processes
([ADR-0015](adr/ADR-0015-shared-usage-refresh-cooldown.md)). Manual Refresh
can reuse cached quotas; it does not bypass the minimum. An already scheduled
cooldown is not shortened by changing the slider; subsequent requests use the
new interval. Provider error backoff may extend the wait. Three minutes is a
conservative minimum, not a guarantee against account restrictions. Claude's
undocumented OAuth usage source has separate policy and compatibility risks
regardless of polling frequency. Longer intervals also delay automatic reset
checks and may miss a credit's expiry window; that feature remains best
effort.

The widget polls `dankaiusage summary` and caches the last successful summary
in DMS plugin state so the bar can render immediately after a shell restart.

## Claude statusline (fallback)

Claude Code passes statusline commands a JSON snapshot on stdin. This is the
fallback limit source for setups where the usage endpoint is unavailable (for
example macOS installs keeping credentials in the Keychain). Configure it to
let DankAIUsage cache the rate-limit data without making extra model calls:

```json
{
  "statusLine": {
    "type": "command",
    "command": "dankaiusage claude-statusline",
    "padding": 0
  }
}
```

The cache is written to `$XDG_STATE_HOME/dankaiusage/claude-statusline.json`,
or `~/.local/state/dankaiusage/claude-statusline.json` when `XDG_STATE_HOME`
is unset.

The statusline payload is produced by Claude Code after an interactive API
response. If the cache does not exist yet, open Claude Code in a trusted
workspace and send one message so Claude Code can pass fresh account limit
data to `dankaiusage claude-statusline`.

## Claude prime

Claude prime is off by default. It deliberately starts a Claude subscription
window with one tiny request, which consumes a small amount of usage. It is
not required to display quotas.

Originally a statusline-era limit workaround
([ADR-0006](adr/ADR-0006-opt-in-claude-prime.md)), it is now useful as a
window scheduler: if you reliably use up every 5-hour window, auto-priming
starts the next window's countdown as soon as the previous one closes instead
of waiting for your next real request
([ADR-0007](adr/ADR-0007-auto-prime-as-window-scheduler.md)).

```sh
dankaiusage claude-prime
```

The command refuses to run unless the statusline command is configured. It
skips without spending anything when account usage data already shows an
active session window, when a prime ran within the last 15 minutes, or, if
account data is unavailable, while the local prime timer is active. Otherwise,
it sends one small `claude -p` prompt with safe mode, no session persistence,
tools disabled, a tiny replacement system prompt, `sonnet` as the default
model, prompt suggestions disabled, and a low budget cap. It then obtains
account usage subject to the shared cooldown, and records a local five-hour
session timer as the fallback guard. New account percentages may need to wait
for the next eligible refresh.

When **Enable Claude prime** is on, the widget automatically runs the prime
request whenever Claude is visible and no active session timer is known. Any
current local Claude session with a future reset time prevents another
automatic prime until that timer expires. After a successful prime, the cached
five-hour session timer provides that reset time even when Claude statusline
does not publish account limits. If an automatic prime fails without producing
local usage, the widget does not keep retrying; use the Claude bolt in the
Advanced dropdown or toggle the setting off and on to try again.
