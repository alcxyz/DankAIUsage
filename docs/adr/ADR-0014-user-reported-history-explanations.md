# ADR-0014: User-reported explanations for quota changes

**Status:** Accepted
**Date:** 2026-09-08
**Applies to:** history helper and widget dropdown
**Amends:** ADR-0011 and ADR-0013

## Context

An early quota refill can accompany a subscription upgrade, a reset redeemed
elsewhere, an account switch, or a provider bonus. The observed percentages
and reset counts do not establish the cause. Users may know what happened and
should be able to supply that context directly in the widget.

## Decision

- Keep the original event kind, confidence, message, and before/after values.
  Store a separate, editable explanation explicitly labelled **user reported**.
  Never promote a user's explanation to provider-confirmed evidence.
- Offer explanations for ambiguous refills, reset-count changes, reset-schedule
  changes, and inferred external redemptions. Do not prompt for ordinary
  scheduled windows or confirmed plugin actions.
- Group related eligible changes observed for the same provider at the same
  time. One response applies to that group; grouping denotes a shared
  observation, not proof of a shared cause.
- Show one compact, dismissible prompt in either dropdown mode for the latest
  visible eligible group, only while it is unanswered and at most 24 hours old.
  Answering it must not reveal a queue of older prompts. Groups shown in
  Advanced reset history remain accessible through Explain / Edit explanation.
- Offer relevant choices from subscription change, reset used elsewhere,
  account/workspace switch, provider-announced bonus/reset, and not sure.
  Dismiss records no cause and suppresses the prompt. A bonus is never inferred
  from eliminating other choices. Permit an optional short local note, with a
  warning not to include sensitive details.
- The helper owns explanations in the existing private, bounded history file.
  Use its lock and atomic writes, retain the 30-day/200-event policy, and handle
  old events without requiring a destructive migration. Reject missing,
  expired, or ineligible targets instead of annotating a different event.
- Write history state version 2 and read version 1 through an additive
  migration. Older helpers must refuse the new state instead of silently
  dropping explanations when rewriting their version-1 event structures.
  During a downgrade, quota collection can continue but retained history
  requires a compatible helper; never reset the file automatically.
- Pass user notes through bounded standard input, not shell commands or process
  arguments. Annotation commands make no provider requests, redeem no resets,
  and alter no quota, tracking, or automation settings.
- Update the UI only after the helper confirms a saved explanation. Preserve
  the draft on failure, show a retryable error, and prevent an older concurrent
  summary from overwriting newly returned history.

## Alternatives and consequences

Automatically labelling an unexplained refill as a gift or upgrade would
overstate the evidence. Terminal-only annotations make ordinary correction
hard to discover. A prompt per quota bar is repetitive; loose time-window
grouping risks conflating unrelated actions. Exact provider/observation groups
are a conservative compromise, with facts retained alongside the explanation.

Notes are deliberately user-entered text, not harvested transcript contents.
They share the history's local storage and retention, rather than creating a
separate permanent journal or telemetry feed.
