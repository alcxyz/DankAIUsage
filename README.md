# DankAIUsage

DankAIUsage is a DankMaterialShell widget for Codex and Claude subscription
usage. It follows the standalone plugin shape used by DankCalendar and keeps
the QML widget thin by collecting data through the `dankaiusage` helper.

The main display is remaining session and weekly allowance. Token totals are
kept as a secondary detail.

## Data sources

- Codex limits: queries the local Codex app server with
  `account/rateLimits/read`.
- Claude limits: reads the latest Claude Code statusline JSON cached by
  `dankaiusage claude-statusline`. This is the only supported non-interactive
  Claude Code source for account limit percentages.
- Token history: reads Codex `logs_2.sqlite` and Claude project JSONL
  transcripts from their normal CLI config locations. Claude token totals are
  local Claude Code history only; usage from claude.ai, mobile, or other online
  surfaces is not written to those transcripts and is not exposed through a
  Claude CLI usage command.
- CLI availability: reports whether `codex`, `claude`, and `sqlite3` are on
  `PATH`.

No credentials are read or written. The helper only emits aggregate local usage
and subscription-window percentages already exposed by the local CLIs.

## Claude statusline

Claude Code passes statusline commands a JSON snapshot on stdin. Configure it
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

You can deliberately start or refresh the Claude Code subscription window with
one tiny request:

```sh
dankaiusage claude-prime
```

The command refuses to run unless the statusline command is configured. When it
runs, it starts Claude Code under a pseudo-terminal, sends one small prompt with
tools disabled, waits for the statusline cache to publish fresh rate-limit data,
and then returns the refreshed session and weekly allowances. This spends a
small amount of Claude usage by design; the widget only shows the Claude prime
action when the "Enable Claude prime" setting is on, and only runs it when you
press that action.

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
