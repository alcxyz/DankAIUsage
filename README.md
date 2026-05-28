# DankAIUsage

DankAIUsage is a DankMaterialShell widget for local Codex and Claude CLI usage
allowance estimates. It follows the standalone plugin shape used by
DankCalendar and keeps the QML widget thin by collecting data through the
`dankaiusage` helper.

The main display is remaining session and weekly allowance. Token totals are
kept as a secondary detail because neither Codex nor Claude currently exposes
authoritative remaining quota through the local CLI/status files.

## Data sources

- Codex: reads `logs_2.sqlite` from `CODEX_HOME`, `$XDG_CONFIG_HOME/codex` when
  present, or `~/.codex`.
- Claude: reads project JSONL transcripts from `CLAUDE_CONFIG_DIR`,
  `$XDG_CONFIG_HOME/claude` when present, or `~/.claude`.
- CLI availability: reports whether `codex`, `claude`, and `sqlite3` are on
  `PATH`.

No credentials are read or written. The helper only emits aggregate local usage.

## Allowances

Set the provider allowance limits in plugin settings:

- Session limit: allowance for the rolling session window, default 5 hours.
- Weekly limit: allowance for the current local calendar week.
- Use `0` when a provider limit is unknown.

Limits are token-unit estimates derived from local CLI usage records. They are
useful for trend/remaining visibility, but are not a server-authoritative quota.

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
dankaiusage summary --period-days 7 --session-hours 5 \
  --codex-session-limit 2000000 --codex-weekly-limit 5000000 \
  --claude-session-limit 1000000 --claude-weekly-limit 3000000 \
  --pretty
```

The widget polls that command and caches the last successful summary in DMS
plugin state so the bar can render immediately after shell restart.
