# ADR-0003: Token history from local CLI artifacts only

**Status:** Accepted, amended 2026-10-31 by [ADR-0027](ADR-0027-remove-five-hour-windows-prime-and-statusline.md) (five-hour range removed)
**Date:** 2026-05-28, updated 2026-11-01 (transcript parse cache added)
**Applies to:** `cmd/dankaiusage/main.go` (collectCodex, collectClaude token scans), `cmd/dankaiusage/transcript_cache.go`
**History:** [archive/ADR-0003-token-history-from-local-cli-artifacts-history.md](archive/ADR-0003-token-history-from-local-cli-artifacts-history.md)

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

Do not re-read unchanged transcript bytes. A per-file parse cache,
`transcript-parse-cache.gob` in the helper's private state directory
(owner-only, `encoding/gob` with a format version, replaced atomically under
a lock), keeps only what the parsers extract: Claude token events (timestamp,
session ID, model, token counts and the hashed tracking identities) and Codex
usage snapshots with the file's session ID, start time, current model and
record counts. It never holds message text or other transcript content. Each
entry is keyed by transcript path and records the file size, modification
time, the offset after the last complete line parsed, and hashes of the first
4 KiB and of the 4 KiB before that offset:

- unchanged size and modification time: the file is not read;
- grown with matching hashes: only the bytes after the offset are parsed,
  with the parser state restored, including Codex session metadata;
- shrunk, hash mismatch or anything unexpected: the file is parsed again;
- a final line without a newline is parsed but not cached, so it is parsed
  again once complete;
- entries for files no summary scan reads any more are dropped.

Full and incremental reads use the same line parser, and everything after it
(Claude revision deduplication, Codex session grouping and snapshot diffing,
totals and metadata) runs on every summary, so the output equals a full scan.
A missing, corrupt or other-version cache is rebuilt, never fatal. The
rolling scans and the tracked-total scan of one summary share the cache.

Accept the scope limitation this implies: token history covers this machine's
CLI usage only. Usage from claude.ai, mobile, or other devices never appears.
The summary states this explicitly via `meta.tokenDataScope` and
`meta.tokenDataIncludesWeb = false` so the UI can annotate it, because limits
(account-wide) and tokens (local-only) intentionally have different scopes.

The dropdown token rows select explicitly labeled rolling ranges together:
seven days, thirty days, and ninety days. A saved five-hour choice falls back
to seven days. The configured history
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
- **Re-parse every transcript on each refresh:** rejected after a summary
  took about 50 s of CPU and 1.5 GB of memory on 3,500 transcripts (7 GB),
  every five minutes.
- **A JSON cache file:** larger and slower to load for roughly 400,000
  records; gob is in the standard library.
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
- A warm summary reads only transcript bytes written since the last one. The
  cache adds a private file of tens of megabytes holding transcript paths and
  raw session IDs; deleting it only costs one full scan. A rewrite that keeps
  both size and modification time is not detected.
- Within a line, values are decoded as a JSON stream, as the whole-file
  decoder did; a JSON value spanning several lines, which the CLIs do not
  write, is now reported as malformed.
