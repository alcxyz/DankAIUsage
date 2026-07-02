# DankAIUsage

DankAIUsage is a DankMaterialShell widget for Codex and Claude subscription
usage. It follows the standalone plugin shape used by DankCalendar and keeps
the QML widget thin by collecting data through the `dankaiusage` helper.

The main display is remaining session and weekly allowance. Token totals are
kept as a secondary detail. Cached tokens are excluded from displayed totals by
default because Claude Code can attach large cached prompt/context blocks to
very small requests; enable "Include cached tokens" when you want to inspect
that overhead.

## Data sources

- Codex limits: queries the local Codex app server with
  `account/rateLimits/read`.
- Claude limits: queries Anthropic's OAuth usage endpoint using the local
  Claude Code sign-in (see
  [ADR-0001](docs/adr/ADR-0001-claude-limits-from-oauth-usage-api.md)).
  The statusline JSON cached by `dankaiusage claude-statusline` is the
  fallback source.
- Token history: reads Codex `logs_2.sqlite` and Claude project JSONL
  transcripts from their normal CLI config locations. Claude token totals are
  local Claude Code history only; usage from claude.ai, mobile, or other online
  surfaces is not written to those transcripts and is not exposed through a
  Claude CLI usage command.
- CLI availability: reports whether `codex`, `claude`, and `sqlite3` are on
  `PATH`.

For Claude limits the helper reads the Claude Code OAuth token from
`~/.claude/.credentials.json` itself, uses it only for the usage request, and
never prints or logs it. Everything it emits is aggregate local usage and
subscription-window percentages.

## Claude statusline (fallback)

Claude Code passes statusline commands a JSON snapshot on stdin. This is the
fallback limit source for setups where the usage endpoint is unavailable
(for example macOS installs keeping credentials in the Keychain). Configure it
to let DankAIUsage cache the rate-limit data without making extra model calls:

```json
{
  "statusLine": {
    "type": "command",
    "command": "dankaiusage claude-statusline",
    "padding": 0
  }
}
```

The cache is written to
`$XDG_STATE_HOME/dankaiusage/claude-statusline.json`, or
`~/.local/state/dankaiusage/claude-statusline.json` when `XDG_STATE_HOME` is
unset.

The statusline payload is produced by Claude Code after an interactive API
response. If the cache does not exist yet, open Claude Code in a trusted
workspace and send one message so Claude Code can pass fresh account limit data
to `dankaiusage claude-statusline`.

Claude prime is a legacy workaround from the statusline-only era (see
[ADR-0006](docs/adr/ADR-0006-opt-in-claude-prime.md)); with the usage endpoint
integration there is normally no reason to enable it. You can still
deliberately start or refresh the Claude Code subscription window with one
tiny request:

```sh
dankaiusage claude-prime
```

The command refuses to run unless the statusline command is configured. If a
local prime timer is already active, it returns without making another Claude
request. Otherwise, it sends one small `claude -p` prompt with safe mode, no
session persistence, tools disabled, a tiny replacement system prompt, `sonnet`
as the default model, prompt suggestions disabled, and a low budget cap. It
then returns Claude statusline allowances when available. If Claude does not
publish statusline rate-limit data, the helper records a local five-hour
session timer from the successful prime request. This spends a small amount of
Claude usage by design.

When the "Enable Claude prime" setting is on, the widget automatically runs the
prime request whenever Claude is visible and no active session timer is known.
Any current local Claude session with a future reset time prevents another
automatic prime until that timer expires. After a successful prime, the cached
five-hour session timer provides that reset time even when Claude statusline
does not publish account limits. If an automatic prime fails without producing
local usage, the widget does not keep retrying; use the Claude bolt or toggle
the setting off and on to try again.

## Build

```sh
nix build
```

or:

```sh
go build ./cmd/dankaiusage
```

## Usage

```sh
dankaiusage summary --period-days 7 --pretty
```

The widget polls that command and caches the last successful summary in DMS
plugin state so the bar can render immediately after shell restart.
