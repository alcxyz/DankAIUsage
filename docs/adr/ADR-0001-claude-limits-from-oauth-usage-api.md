# ADR-0001: Read Claude subscription limits from the OAuth usage endpoint

**Status:** Accepted, amended 2026-10-31 by [ADR-0027](ADR-0027-remove-five-hour-windows-prime-and-statusline.md) (statusline fallback removed)
**Date:** 2026-07-02
**Applies to:** `cmd/dankaiusage/main.go` (Claude limit collection)
**History:** [archive/ADR-0001-claude-limits-from-oauth-usage-api-history.md](archive/ADR-0001-claude-limits-from-oauth-usage-api-history.md)

Refresh timing and cache coordination are amended by
[ADR-0015](ADR-0015-shared-usage-refresh-cooldown.md).

## Context

The widget originally learned Claude session/weekly limit percentages from the
Claude Code statusline: `statusLine.command` was pointed at
`dankaiusage claude-statusline`, which cached the stdin payload (containing
`rate_limits.five_hour` / `rate_limits.seven_day`) for later summary runs.

This only works when Claude Code runs as an interactive terminal TUI — the
statusline is a TUI-only feature. On hosts where Claude Code runs through an
SDK or IDE harness, the statusline command is never invoked, the cache goes
permanently stale, and the widget shows `--` for Claude regardless of actual
usage. The `claude-prime` workaround (spawning a tiny print-mode request) also
cannot recover real percentages, because print mode does not drive the
statusline; it degrades to a local session timer.

This superseded
[ADR-0005](archive/ADR-0005-claude-limits-via-statusline-capture.md) (statusline
capture as the primary source) and
[ADR-0006](archive/ADR-0006-opt-in-claude-prime.md) (claude-prime).

## Decision

Query Anthropic's OAuth usage endpoint directly as the only Claude limit
source:

- `GET https://api.anthropic.com/api/oauth/usage` with the Claude Code OAuth
  access token from `~/.claude/.credentials.json` (read by the binary itself;
  the token is never logged or passed through callers).
- Required headers: `Authorization: Bearer`, `anthropic-beta: oauth-2025-04-20`,
  and `User-Agent: claude-code/<version>` — anonymous user agents land in an
  aggressively rate-limited bucket that returns persistent HTTP 429s.
- Responses are cached in `$XDG_STATE_HOME/dankaiusage/claude-oauth-usage.json`
  with a 2-minute fresh TTL, a 30-minute stale-serve window on failures, and
  error backoff (15 minutes minimum on 429).

There is no fallback source. When the usage fetch fails and no cached
response is within the stale-serve window, Claude limits are unavailable and
the error is shown; this includes macOS installs that keep
credentials in the Keychain rather than the credentials file.

## Alternatives Considered

- **Statusline capture only (status quo):** never fires outside the TUI; the
  exact failure this ADR fixes.
- **Statusline capture as a fallback:** kept at first for TUI-only and macOS
  Keychain setups, then removed by ADR-0027 as TUI-only and no longer worth
  its surface.
- **`claude-prime` print-mode priming:** cannot obtain percentages, only a
  local 5-hour timer; adds cost and latency per prime.
- **Official Rate Limits API (`/v1/organizations/rate_limits`):** requires an
  Admin API key and covers API-key organizations, not Pro/Max subscription
  windows.
- **Parsing local transcripts for limit data:** transcripts record token usage
  but no account-level window utilization.

## Consequences

- Claude limits now work on any host with a signed-in Claude Code install,
  independent of how Claude Code is run, and reflect account-wide usage
  (including web) rather than local-only signals.
- We depend on an undocumented endpoint that Anthropic may change or restrict;
  the widget degrades to `--` plus a diagnostic (`meta.limitError`) if it
  breaks.
- The binary reads the OAuth credentials file directly. It must never print
  the token; error messages are kept generic by design.
- If the access token is expired and no Claude Code session refreshes it, the
  source fails until the user runs Claude Code once (surfaced in the widget
  note).
