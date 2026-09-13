# ADR-0017: Opt-in public reset announcements

**Status:** Accepted
**Date:** 2026-09-12
**Applies to:** helper, widget, settings

## Context

Public announcements can provide advance warning or context for a refill.
They do not establish eligibility or the cause of a particular account change.
Issue #8 requests this context without forecasts, telemetry, or interference
with quota collection and automation.

## Decision

- Use the TokenResets public events API for the initial integration. It covers
  both providers with explicit confidence, scope, revision and timing fields.
  Its published API terms allow cached use with attribution; availability and
  accuracy are not guaranteed. Codex Reset was considered, but would require
  another source for Claude and interpretation of social posts/forecast signals.
  No direct X scraping or new aggregation service is introduced.
- Default off. The settings disclosure names TokenResets and explains that its
  host sees IP/request metadata. Request only a fixed generic public feed;
  never attach credentials, cookies, local usage, history, diagnostics or notes.
- Fetch in a separate helper process on a fifteen-minute timer, with durable
  request reservations/backoff and conditional caching. Store disposable public
  content under XDG cache; retain coordination state under XDG state. Feed
  errors never affect quota errors, collection timers, or automation.
- Bound response size, event count, text, timestamps and source links. Treat
  feed and cache as untrusted data. Do not follow arbitrary redirects or detail
  URLs; render plain text and open validated evidence pages only on user action.
- Show fresh, explicit upcoming reset windows in both dropdown modes; additional
  recent public reports are available in Advanced. Use local dates/times, never
  invent a timezone or derive a countdown from prose. Unknown scope stays unknown.
- Notify only for verified-by-feed hard-reset announcements with explicit future
  deadlines within 48 hours, announced within 24 hours. Persist bounded receipts
  by event ID/revision before notification. Do not replay completed history on
  installation or restart. Revisions may notify once again; revoked, expired or
  missing events disappear from the next fresh snapshot. Stale feeds cannot
  notify or contribute local matching.
- Match only clear early-refill observations, not scheduled/plugin/inferred
  redeemed resets. Require a local observation interval no longer than one hour
  and public event timing within fifteen minutes of that interval. This is a
  temporal association, not proof of causation or eligibility. Use stronger
  "Likely linked" wording only for feed-verified records; reported evidence is
  labelled as nearby public context. Never rewrite original local history or
  user explanations. Grants of banked resets are not immediate refills.
- Keep forecasts, statistical predictions and rumor alerts out of scope. Never
  change redemption decisions or recommend spending allowance based on a feed.

## Alternatives and consequences

A maintained source adapter reduces dependencies and avoids per-user social
API credentials. It introduces an optional third-party availability/privacy
dependency. Users must opt in, and attribution/provenance remain visible.
Different sites repeating one original post are not independent confirmation.
Corrections can remove context on refresh; an already displayed toast cannot
be recalled. A disappeared record is not itself proof of cancellation.

References reviewed: [API](https://tokenresets.com/api/),
[terms](https://tokenresets.com/terms/),
[privacy](https://tokenresets.com/privacy/).
