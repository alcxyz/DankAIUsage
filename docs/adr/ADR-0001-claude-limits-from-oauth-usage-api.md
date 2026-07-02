# ADR-0001: Read Claude subscription limits from the OAuth usage endpoint

**Status:** Accepted
**Date:** 2026-07-02
**Applies to:** `cmd/dankaiusage/main.go` (Claude limit collection)

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

## Decision

Query Anthropic's OAuth usage endpoint directly as the primary limit source:

- `GET https://api.anthropic.com/api/oauth/usage` with the Claude Code OAuth
  access token from `~/.claude/.credentials.json` (read by the binary itself;
  the token is never logged or passed through callers).
- Required headers: `Authorization: Bearer`, `anthropic-beta: oauth-2025-04-20`,
  and `User-Agent: claude-code/<version>` — anonymous user agents land in an
  aggressively rate-limited bucket that returns persistent HTTP 429s.
- Responses are cached in `$XDG_STATE_HOME/dankaiusage/claude-oauth-usage.json`
  with a 2-minute fresh TTL, a 30-minute stale-serve window on failures, and
  error backoff (15 minutes minimum on 429).

The statusline cache remains as a fallback source so TUI-only setups keep
working, and macOS installs (credentials in Keychain, no credentials file)
degrade gracefully to it.

## Alternatives Considered

- **Statusline capture only (status quo):** never fires outside the TUI; the
  exact failure this ADR fixes.
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
  the widget degrades to `--` plus a diagnostic (`meta.oauthUsageError`) if it
  breaks, and the statusline fallback still functions.
- The binary reads the OAuth credentials file directly. It must never print
  the token; error messages are kept generic by design.
- If the access token is expired and no Claude Code session refreshes it, the
  source fails until the user runs Claude Code once (surfaced in the widget
  note).
