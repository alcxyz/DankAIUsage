# ADR-0023: Separate low-cadence request for Claude reset availability

**Status:** Accepted
**Date:** 2026-10-03
**Applies to:** `cmd/dankaiusage` (Claude usage headers, `claude-reset`)
**Amends:** ADR-0022's single usage request under the `claude-cli` User-Agent.
**Amended by:** [ADR-0024](ADR-0024-retry-rate-limited-reset-check.md) retries a
rate-limited check at quota cadence for an hour before the hourly backoff.

## Context

ADR-0022 moved the quota poll to Claude Code's OAuth client identity,
`claude-cli/<version> (external, cli)`, with `cedar_ember=1`, because the usage
endpoint reports limit resets only for that surface. Within hours of the 1.3.0
release the quota poll failed with HTTP 429 (`Retry-After: 0`) on almost every
attempt, from a fresh state directory as well, so the widget showed no Claude
windows at all.

Probes on 2026-10-03 isolated the cause to the identity, not the client: from
the same machine and account, Go and curl requests with the former
`claude-code/<version>` User-Agent answered 200 every time over HTTP/2 and
HTTP/1.1, while the `claude-cli` User-Agent answered 429 whatever the headers,
query, or HTTP version, with occasional successes minutes apart. Anonymous
User-Agents answered 429 throughout, as ADR-0001 records. The endpoint treats
the official client identity as a budgeted surface and polling it every few
minutes exhausts that budget.

## Decision

- The quota poll returns to `claude-code/<version>` and the plain usage URL,
  exactly as before ADR-0022. It never carries the `claude-cli` identity.
- Reset availability is a separate request to
  `/api/oauth/usage?cedar_ember=1&skip_spend=1` under the `claude-cli`
  identity, the URL Claude Code itself uses for this check. It runs from the
  summary collector at most every 30 minutes, or two quota intervals if those
  are longer, and keeps its own cache (`claude-reset-availability.json`) with
  the same lock, permissions, organization binding, and stale rule shape as
  the quota cache. A failed check backs off for at least an hour, keeps the
  last body for a six-hour stale window, and records its reason in the
  provider meta, which the Advanced dropdown shows as a notice, and in
  `claude-reset status`.
- `use` reads grants only from the availability cache. A successful redemption
  expires both caches so the next summary refetches the windows and the
  remaining grants.
- The claim request keeps the `claude-cli` identity: it is one request per
  deliberate action, not a poll.

## Alternatives Considered

- **Keep one request and poll less often.** The quota windows are the plugin's
  purpose and need the few-minute cadence; the grant list changes rarely.
- **Reproduce more of Claude Code's request shape.** `x-app`, `Accept`,
  compression and HTTP version made no difference in the probes. The endpoint
  is undocumented, and chasing its fingerprint would break again silently.
- **Drop the reset feature.** The grant still expires unused if the user cannot
  see it; a half-hourly check under the client identity worked in the probes.

## Consequences

- One extra request every 30 minutes, far below the quota cadence. If the
  provider rejects the check, the Claude card shows no reset, an Advanced
  notice and `claude-reset status` say why, and quotas are unaffected.
- The reset listing can lag a redemption or a new grant by up to 30 minutes;
  the next summary after a successful `use` refetches immediately.
- ADR-0022's alternative "a separate status request" is superseded by this
  evidence.
