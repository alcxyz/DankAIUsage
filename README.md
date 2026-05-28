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
  `dankaiusage claude-statusline`.
- Token history: reads Codex `logs_2.sqlite` and Claude project JSONL
  transcripts from their normal CLI config locations.
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
