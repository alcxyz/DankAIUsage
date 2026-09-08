# ADR-0003: Token history from local CLI artifacts only

**Status:** Accepted, amended 2026-09-08
**Date:** 2026-05-28
**Applies to:** `cmd/dankaiusage/main.go` (collectCodex, collectClaude token scans)

## Context

Token history needs a per-request event stream. Neither subscription offers a
usage API with token granularity (the Anthropic usage endpoint returns only
window utilization percentages; the official Usage and Cost API requires an
organization Admin key). Both CLIs, however, leave detailed artifacts on disk.

## Decision

Read token events from the local CLI artifacts:

- **Codex:** token-count events in local `sessions/` and `archived_sessions/`
  JSONL files, streamed using the Go standard library. Current local logs no
  longer contain the OTEL completion rows used by the original collector.
  Cumulative snapshots must not be summed directly, and repeated or inherited
  history must not be counted twice. Do not mix transcript and OTEL totals.
  No sqlite binary or linked database driver is required.
- **Claude:** usage blocks in the project transcript JSONL files under
  `~/.claude/projects/`.

Accept the scope limitation this implies: token history covers this machine's
CLI usage only. Usage from claude.ai, mobile, or other devices never appears.
The summary states this explicitly via `meta.tokenDataScope` and
`meta.tokenDataIncludesWeb = false` so the UI can annotate it, because limits
(account-wide) and tokens (local-only) intentionally have different scopes.

The dropdown token rows select explicitly labeled rolling ranges together:
five hours, seven days, thirty days, and ninety days. The configured history
period remains available for compatibility. None means the active conversation,
all-time history, or an account quota window. Remember this presentation choice
locally; switching views does not trigger provider calls or alter the collected
totals. Optional persistent totals are separate, governed by
[ADR-0012](ADR-0012-optional-persistent-token-totals.md).

## Alternatives Considered

- **Provider cloud usage APIs:** not available at token granularity for
  subscription accounts (admin/organization only).
- **Linking a sqlite driver (CGO or pure-Go):** heavier builds for a
  read-only query that a ubiquitous CLI tool already handles.
- **Continue using OTEL logs:** rejected after local evidence showed active
  sessions with token events but no matching telemetry rows. Returning zero
  from an empty telemetry query hid a collection failure.
- **Mix transcript and legacy OTEL sources:** rejected because overlapping
  events lack a reliable shared identity and can inflate totals.
- **Hiding the scope mismatch:** rejected; a user comparing widget totals with
  Anthropic's own dashboards would be misled without the annotation.

## Consequences

- Token numbers are trustworthy for local CLI work and useless for judging
  account-wide consumption — that job belongs to the window percentages
  (ADR-0002).
- Missing or unreadable Codex token history is explicitly unavailable; partial
  collection is marked rather than presented as a complete zero total.
- Transcript scans are bounded by file mtime against the selected period to
  keep summary runs fast.
