# ADR-0023: Separate low-cadence request for Claude reset availability

**Status:** Accepted, amended 2026-11-01 (hourly cadence, exponential backoff
for rate limits, last known status, warning only for a sign-in rejection;
retires [ADR-0024](archive/ADR-0024-retry-rate-limited-reset-check.md))
**Date:** 2026-10-03, amended 2026-11-01
**Applies to:** `cmd/dankaiusage` (Claude usage headers, `claude-reset`), `DankAIUsageWidget.qml`
**History:** [archive/ADR-0023-separate-claude-reset-availability-check-history.md](archive/ADR-0023-separate-claude-reset-availability-check-history.md)
**Amends:** ADR-0022's single usage request under the `claude-cli` User-Agent.

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
the official client identity as a budgeted surface, shared with the Claude
Code sessions on the account, and polling it every few minutes exhausts that
budget.

From 2026-10-04 12:50 UTC every hourly check answered 429 with
`Retry-After: 0` for 18 hours, and probes on 2026-10-05 answered 200 once,
then 429 five times at 15-second spacing, then 200 again. ADR-0024 then
retried a rate-limited check every five minutes for an hour. On 2026-10-31
and 2026-11-01 every attempt for more than eleven hours answered 429 while
the user was idle: the quick retries most likely spent the budget they were
waiting for. Once the six-hour stale window ran out, the dropdown showed an
orange "Limit-reset check failed" warning the user could do nothing about,
although the last successful answer was still cached and said the account
had no grant.

## Decision

- The quota poll uses `claude-code/<version>` and the plain usage URL, as
  before ADR-0022. It never carries the `claude-cli` identity. Its own
  15-minute minimum backoff after a 429 stays as ADR-0001 set it.
- Reset availability is a separate request to
  `/api/oauth/usage?cedar_ember=1&skip_spend=1` under the `claude-cli`
  identity, the URL Claude Code itself uses for this check. It runs from the
  summary collector at most once an hour, or every two quota intervals if
  those are longer, and keeps its own cache (`claude-reset-availability.json`)
  with the same lock, permissions, organization binding, and stale rule shape
  as the quota cache.
- Consecutive 429 answers back off exponentially: one hour after the first,
  then two, four, eight and sixteen hours, capped at 24 hours. The wait is
  never shorter than the cadence, and a longer `Retry-After` is honoured, up
  to a week. The cache counts the streak (`rateLimitStreak`) and records its
  first answer (`rateLimitedSince`); a cache that has only the start counts
  as one answer. A successful check, or any failure other than a 429, ends
  the streak. Other failures back off for at least an hour, honouring the
  fetch's own backoff. The streak is a property of the availability cache;
  the quota cache never sets it.
- A failed check keeps the last body. Its grants are listed, and `use` may
  redeem one, only within the six-hour stale window (or two check cadences if
  longer) after the fetch; a body whose last check succeeded is current.
- The summary always reports the last successful check as
  `claudeResetLastCheck`, whatever its age: when it ran, eligibility, the
  ineligibility reason, the number of resets available, and whether it is
  still fresh. It is never reported under a different organization than the
  one that fetched it. The Advanced Claude card shows it as a muted line
  ("No limit reset available · checked ..." or "1 limit reset available ·
  checked ..."), or "Limit reset status unknown" when there is no usable successful answer. The line is
  left out while current grants are listed with their use action.
  The **Use reset now** action appears only while the answer is fresh.
- Only a sign-in rejection by the server (HTTP 401/403) is a warning: it is
  reported as `claudeResetCheckError` and shown as an Advanced notice under
  the card. Any other failure is reported as `claudeResetCheckFailure` (the
  reason, HTTP status, last success, next attempt and rate-limit streak) and
  shown as a muted line in the Advanced Diagnostics section, outside the
  copied report, whose allowlist (ADR-0016) is unchanged. Missing local
  credentials stay quiet, because the quota poll reports them.
  `claude-reset status` still reports the reason of the last failed check.
- `use` reads grants only from the availability cache. A successful
  redemption expires both caches so the next summary refetches the windows
  and the remaining grants.
- The claim request keeps the `claude-cli` identity: it is one request per
  deliberate action, not a poll.

## Alternatives Considered

- **Keep one request and poll less often.** The quota windows are the plugin's
  purpose and need the few-minute cadence; the grant list changes rarely.
- **Reproduce more of Claude Code's request shape.** `x-app`, `Accept`,
  compression and HTTP version made no difference in the probes. The endpoint
  is undocumented, and chasing its fingerprint would break again silently.
- **Drop the reset feature.** The grant still expires unused if the user cannot
  see it; an occasional check under the client identity works.
- **Retry a rate-limited check at quota cadence** (ADR-0024, retired). It
  added up to twelve requests an hour against the shared budget and coincided
  with the longest run of rate limits observed.
- **Retry inside one check.** Several requests seconds apart from the summary
  collector would hold the widget's refresh, and the samples showed no better
  odds at short spacing than at long.
- **Warn whenever the listing is stale.** The warning named a failure the user
  cannot act on; the last known answer, with its age, is the useful part.

## Consequences

- At most one extra request an hour, far below the quota cadence, and fewer
  while the identity is rate limited. If the provider keeps rejecting the
  check, quotas are unaffected and the card keeps the last known status.
- The reset listing can lag a redemption or a new grant by an hour, and by up
  to a day while checks keep being rate limited; the reset stays usable on
  claude.ai. The next summary after a successful `use` refetches immediately.
- A stale status can count resets the widget no longer offers to use until a
  check succeeds again.
- A refused `claude-reset use` on a stale answer and the Diagnostics line
  quote the real next attempt.
- ADR-0022's alternative "a separate status request" is superseded by this
  evidence.
