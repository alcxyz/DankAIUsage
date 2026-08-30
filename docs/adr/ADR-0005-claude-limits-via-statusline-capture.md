# ADR-0005: Claude limits via statusline capture

**Status:** Superseded by [ADR-0001](ADR-0001-claude-limits-from-oauth-usage-api.md)
**Date:** 2026-05-28
**Applies to:** `cmd/dankaiusage/main.go` (captureClaudeStatusline, statusline cache)

## Context

ADR-0002 committed the widget to authoritative window percentages, and at the
time the only documented non-interactive way to obtain Claude subscription
percentages was the statusline: Claude Code passes a JSON snapshot (including
`rate_limits.five_hour` / `rate_limits.seven_day`) on stdin to the configured
`statusLine.command` after each interactive API response.

## Decision

Point `statusLine.command` at `dankaiusage claude-statusline`, which writes
the raw payload to `$XDG_STATE_HOME/dankaiusage/claude-statusline.json` and
prints a compact limits line for the TUI. Summary runs read limits from that
cache.

## Alternatives Considered

- **Scraping the `/usage` TUI screen:** requires driving a pty; fragile
  against layout changes.
- **Undocumented OAuth usage endpoint:** known to exist but unofficial, with
  reported aggressive rate limiting; deferred until the statusline route
  proved insufficient.
- **Token-based estimation:** already rejected in ADR-0002.

## Consequences

- Worked only while Claude Code ran as an interactive terminal TUI. On hosts
  where Claude Code runs through an SDK/IDE harness, the statusline command
  never fires and the cache goes permanently stale — the failure that led to
  ADR-0006 (priming) and ultimately ADR-0001, which demoted this capture path
  to a fallback source.
- The statusline configuration and cache remain supported as the fallback for
  TUI-only setups and for macOS installs where credentials live in the
  Keychain.
