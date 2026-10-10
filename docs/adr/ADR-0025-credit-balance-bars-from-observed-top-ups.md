# ADR-0025: Credit balance bars from observed top-ups

**Status:** Accepted
**Date:** 2026-10-10
**Applies to:** `cmd/dankaiusage` (usage history), `DankAIUsageWidget.qml`, settings
**Amends:** ADR-0021's rule that credits stay out of quota-bar mode.

## Context

A prepaid balance without a spend limit has no window or limit, so ADR-0021
kept credits out of quota-bar mode and the dropdown showed only the amount.
The credit switches then did nothing while quota bars were on, which read as
a bug. Neither provider reports purchases, only the current balance.

## Decision

- The helper keeps a bounded ledger per prepaid balance in the usage-history
  state: the last balance, when it was first seen, and up to 64 top-ups. The
  first sighting is recorded as a top-up from zero; afterwards any increase is
  a top-up and any decrease is spending. Only fresh snapshots newer than the
  ledger update it, so an older cached snapshot cannot fake a top-up, and a
  balance reported empty (which hides its bucket) still records zero so the
  next refill counts. Summaries attach the ledger to the balance's bucket.
  Buckets with a spend limit keep their own allowance.
- The widget measures the balance against every top-up within the selected
  token-history range, and never fewer than the latest one. The bar's limit is
  the balance just before the earliest counted top-up plus the counted
  amounts, so leftovers from older purchases count. Tracked uses the tracking
  start, or every retained top-up.
- The dropdown draws that bar under the balance with "left of … since …".
  In quota-bar mode, the provider's credit switch adds it as a `cr` row; text
  mode still shows the amount. The bar never counts as the most constrained
  quota.

## Alternatives

Using the first observed balance as a fixed limit ignores later purchases.
Asking users for their purchase amounts adds a setting for something the
helper can observe. Leaving credits out of bar mode keeps the switches
confusing.

## Consequences

Purchases made before the first summary that includes this ledger are
unknown; the bar starts full at the balance seen then. Refunds and grants also
raise the balance and count as top-ups. A balance that rises and is spent
between two refreshes is only partly seen. Older helpers ignore the ledger and
drop it if they rewrite the history file.
