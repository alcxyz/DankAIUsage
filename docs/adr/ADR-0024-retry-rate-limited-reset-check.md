# ADR-0024: Retry a rate-limited Claude reset check at quota cadence

**Status:** Accepted
**Date:** 2026-10-05
**Applies to:** `cmd/dankaiusage` (`claude-reset` availability check)
**Amends:** ADR-0023's hourly backoff after a failed reset check.

## Context

ADR-0023 gives the reset availability check one request every 30 minutes
under the `claude-cli` identity and an hour of backoff after any failure, on
the reading that polling the identity every few minutes had exhausted its
budget.

Two days of hourly checks showed otherwise. From 2026-10-04 12:50 UTC every
hourly attempt answered HTTP 429 with `Retry-After: 0` and the body
`Rate limited. Please try again later.`, while probes from the same machine
on 2026-10-05 answered 200 once, then 429 five times at 15-second spacing,
then 200 again. The identity is lossy regardless of the helper's cadence:
Claude Code sessions on the same account share its budget, most requests
lose, and an attempt succeeds now and then. One draw an hour lost the listing
for 18 hours, the six-hour stale window ran out, and the Advanced warning
from the previous change appeared with nothing the user could do about it.

## Decision

- A reset check answered with HTTP 429 and no requested wait longer than the
  retry interval is retried at the quota interval, no closer than five
  minutes, for one hour after the first consecutive 429. The cache records
  that first 429 (`rateLimitedSince`); a successful check clears it.
- After that hour, and for a 429 naming a longer wait, ADR-0023's backoff
  applies unchanged: at least an hour, honouring `Retry-After`.
- Other failures keep ADR-0023's rules. The quota poll is untouched; its
  15-minute minimum backoff after a 429 stays as ADR-0001 set it.

## Alternatives Considered

- **Keep one draw an hour.** With success rates near one in six, a lost
  listing stays lost for most of a day while the grant still expires.
- **Retry at quota cadence until success.** Unbounded; in the bad state it
  adds a request every few minutes indefinitely, the behaviour ADR-0023
  removed.
- **Retry inside one check.** Several requests seconds apart from the summary
  collector would hold the widget's refresh, and the samples show no better
  odds at short spacing than at long.

## Consequences

- In the bad state the check costs at most twelve requests in the first hour
  after a loss, then one an hour, instead of one an hour throughout. At the
  observed odds the listing is back within the hour far more often than not.
- `claude-reset status` and the dropdown quote the real next attempt, which
  is now minutes rather than an hour while the window is open.
- The retry window is a property of the availability cache; the quota cache
  never sets it.
