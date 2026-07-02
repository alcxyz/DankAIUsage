# ADR-0003: Token history from local CLI artifacts only

**Status:** Accepted
**Date:** 2026-05-28
**Applies to:** `cmd/dankaiusage/main.go` (collectCodex, collectClaude token scans)

## Context

Token history needs a per-request event stream. Neither subscription offers a
usage API with token granularity (the Anthropic usage endpoint returns only
window utilization percentages; the official Usage and Cost API requires an
organization Admin key). Both CLIs, however, leave detailed artifacts on disk.

## Decision

Read token events from the local CLI artifacts:

- **Codex:** OTEL rows in `~/.codex/logs_2.sqlite`
  (`codex.sse_event` / `response.completed`), queried through the external
  `sqlite3` binary rather than a linked driver. This keeps the Go binary
  CGO-free and dependency-light; `sqlite3` availability is reported as a
  capability in the summary instead of being a hard requirement.
- **Claude:** usage blocks in the project transcript JSONL files under
  `~/.claude/projects/`.

Accept the scope limitation this implies: token history covers this machine's
CLI usage only. Usage from claude.ai, mobile, or other devices never appears.
The summary states this explicitly via `meta.tokenDataScope` and
`meta.tokenDataIncludesWeb = false` so the UI can annotate it, because limits
(account-wide) and tokens (local-only) intentionally have different scopes.

## Alternatives Considered

- **Provider cloud usage APIs:** not available at token granularity for
  subscription accounts (admin/organization only).
- **Linking a sqlite driver (CGO or pure-Go):** heavier builds for a
  read-only query that a ubiquitous CLI tool already handles.
- **Codex session files instead of OTEL logs:** less structured; the sqlite
  log already carries per-response token counts with timestamps.
- **Hiding the scope mismatch:** rejected; a user comparing widget totals with
  Anthropic's own dashboards would be misled without the annotation.

## Consequences

- Token numbers are trustworthy for local CLI work and useless for judging
  account-wide consumption — that job belongs to the window percentages
  (ADR-0002).
- Codex token history silently degrades when `sqlite3` is missing (surfaced
  as `tokenDataError`).
- Transcript scans are bounded by file mtime against the selected period to
  keep summary runs fast.
