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
`meta`. `--session-hours N` (default 5) sets the length of the `session` token
totals. The five-hour fields (`sessionLeft`, `session`, `rolling.fiveHours`,
`extraLimits[].sessionLeft`, and the `general-5-hour` and `<id>-5-hour`
buckets) appear only when a provider reports a five-hour window; the token
totals then cover every provider.

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

## Claude limit reset

```sh
dankaiusage claude-reset status
dankaiusage claude-reset use
dankaiusage claude-reset use --grant <id>
dankaiusage claude-reset retry
dankaiusage claude-reset forget
```

`status` reads the cached reset availability and the helper's attempt record
without contacting a server. `use` sends one redemption request for the grant Claude
offers next, or the given grant, and retries an unconfirmed attempt first.
`retry` only resends an unconfirmed attempt and refuses otherwise. `forget` drops an unconfirmed attempt record after an account switch, and replaces a saved record the helper cannot read. See
[Claude limit resets](claude-reset.md).

## History and diagnostics

```sh
dankaiusage history
dankaiusage diagnostics
```

`history` reads the retained reset events without contacting either provider.
`diagnostics` prints the same local-only report as the dropdown. See
[Reset history](reset-history.md) and
[Troubleshooting and diagnostics](diagnostics.md).

## Claude prime

```sh
dankaiusage claude-prime [--refresh-interval N] [--active-from H] [--active-until H] [--min-weekly-left P] [--daily-cap N] [--model M] [--prompt P] [--timeout-seconds N] [--pretty]
```

`claude-prime` starts Claude's five-hour window with one tiny request when it
is idle, using the same rules as the widget's **Claude prime** setting. It
decides only from the usage response the quota poll saved, and prints one JSON
object: `ok`, `primed`, `reason`, `message`, `checkedAt`, `primedAt` and
`sessionLeft`. `primed` is true only when a request was sent. The `reason`
values are `primed`, `usage_unknown`, `usage_error`, `usage_stale`,
`sign_in_mismatch`, `awaiting_usage`, `prime_unconfirmed`, `no_five_hour_window`,
`window_running`, `outside_active_hours`, `weekly_low` and `daily_cap`; every
reason other than `primed` is a skip that costs nothing. `--active-from` and
`--active-until` (local hours, default 7 and 23; equal means all day),
`--min-weekly-left` (percent, default 10, 0 off) and `--daily-cap` (default 5)
are the limits the **Claude prime limits** settings pass.
The command exits 1 only when the request failed or the local state cannot be
used. See [Claude prime](data-sources.md#claude-prime).

`dankaiusage claude-statusline` does nothing: it reads and discards its input
and prints nothing, so an old Claude Code `statusLine.command` that still runs
it shows a blank line rather than an error. See
[Where the numbers come from](data-sources.md#where-the-numbers-come-from).
