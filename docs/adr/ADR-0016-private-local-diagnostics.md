# ADR-0016: Allowlisted local diagnostics

**Status:** Accepted
**Date:** 2026-09-12
**Applies to:** helper, widget, packaging

## Context

Intermittent quota failures disappear from the current status after recovery.
Maintainers and users need a small diagnostic record without accidentally
sharing credentials, account details, prompts, or raw command output.

## Decision

- Retain bounded diagnostic transitions locally under the XDG state directory,
  separate from quota history. Use owner-only files, atomic writes and locking.
  Keep at most 100 events for seven days, with a 64 KiB read limit. Pruning is
  performed on access, not by a new background service.
- Allow only timestamps, fixed provider/category identifiers, HTTP status,
  bounded cooldown duration, and validated public build version/revision.
  Never store or export raw error strings, responses, paths, account IDs,
  usage totals, user notes, credentials or transcripts. Validate stored data
  again when producing a report; disk contents are not trusted report text.
- Record failure/recovery transitions rather than successful polling traffic.
  Diagnostic failures must not change quota requests, retries or reset safety.
  A failed summary can record only a fixed helper-failure category.
- Offer a local-only CLI report and an Advanced preview/copy panel. Opening
  diagnostics never contacts providers. Copy only the previewed report on an
  explicit click; no telemetry, uploads, or automatic issue submission.
- Store durable refresh reservations in state, not disposable cache. Respect
  absolute XDG state paths with the standard home fallback. Do not move users'
  existing state as part of this feature.
- Report build revisions where available. Flake-less Nix consumers use a
  public-source fingerprint, not local build paths, to distinguish dev builds.

## Alternatives and consequences

Raw logs followed by redaction risk missing new secret formats. A strict
allowlist loses some debugging detail but prevents arbitrary error payloads
from entering reports. Remote telemetry is unnecessary for this use case.
Timestamps and build identifiers still reveal limited activity/environment
information, so reports are previewable and sharing is always explicit.

This is a prospective, bounded troubleshooting record, not an exhaustive
audit trail. It cannot establish causes of failures that occurred before
installation or while the helper could not run or write state.
