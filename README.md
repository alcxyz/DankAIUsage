# DankAIUsage

DankAIUsage is a DankMaterialShell widget for local Codex and Claude CLI token
usage. It follows the standalone plugin shape used by DankCalendar and keeps the
QML widget thin by collecting data through the `dankaiusage` helper.

## Data sources

- Codex: reads `logs_2.sqlite` from `CODEX_HOME`, `$XDG_CONFIG_HOME/codex` when
  present, or `~/.codex`.
- Claude: reads project JSONL transcripts from `CLAUDE_CONFIG_DIR`,
  `$XDG_CONFIG_HOME/claude` when present, or `~/.claude`.
- CLI availability: reports whether `codex`, `claude`, and `sqlite3` are on
  `PATH`.

No credentials are read or written. The helper only emits aggregate token counts.

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
