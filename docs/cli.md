# Helper CLI reference

The `dankaiusage` helper collects usage for the widget and exposes the same
state from the terminal. It never prints credentials.

## Build

```sh
nix build
```

or:

```sh
go build ./cmd/dankaiusage
```

## Summary

```sh
dankaiusage summary --period-days 7 --pretty
dankaiusage version
```

`summary` is the command the widget polls. Provider diagnostics appear under
`meta`.

## Tracking

Terminal controls use the same helper-owned state as the widget:

```sh
dankaiusage tracking status
dankaiusage tracking enable
dankaiusage tracking pause
```

The separate `dankaiusage tracking clear` command permanently clears the local
tracked period and disables tracking. See
[Optional tracked totals](dropdown.md#optional-tracked-totals).

## Codex reset

```sh
dankaiusage codex-reset status
dankaiusage codex-reset arm
dankaiusage codex-reset disarm
```

See [One-shot Codex reset](codex-reset.md).

## History and diagnostics

```sh
dankaiusage history
dankaiusage diagnostics
```

`history` reads the retained reset events without contacting either provider.
`diagnostics` prints the same local-only report as the dropdown. See
[Reset history](reset-history.md) and
[Troubleshooting and diagnostics](diagnostics.md).

## Claude

```sh
dankaiusage claude-statusline
dankaiusage claude-prime
```

`claude-statusline` is configured as a Claude Code statusline command and
caches the fallback limit source. `claude-prime` starts a Claude session
window with one tiny request. See [Data sources](data-sources.md).
