# ADR-0022: Explicit, confirmed use of Claude limit resets

**Status:** Accepted
**Date:** 2026-10-03
**Applies to:** `cmd/dankaiusage` (`claude-reset`, Claude usage headers), `DankAIUsageWidget.qml`
**Builds on:** ADR-0001, ADR-0008, ADR-0010
**Amends:** ADR-0001's request identity; ADR-0008's "keep reset consumption outside the plugin" for Claude.

## Context

Since late September 2026 Anthropic grants Pro, Max and Team subscribers
occasional **limit resets**: a saved reset that refills the five-hour window
(and, for the launch grant, the weekly window) when the user chooses, until it
expires. claude.ai exposes it under Settings → Usage, and Claude Code ships a
`/limit-reset` command. A user who already watches both windows in the widget
could see the grant was there but had to open another client to spend it.

Both clients use the OAuth usage endpoint. With the `cedar_ember=1` query the
usage response carries a `cedar_ember` block: account eligibility, an
ineligibility reason, the open grants with `resets_left`, `ends_at`, what each
clears and whether it may be used before hitting a limit, plus the grant the
server offers next. A grant is redeemed with one POST to
`/api/organizations/<organization>/reset_rate_limits` carrying the program
name, the grant id and a caller-chosen request id used for idempotency.

The server decides eligibility partly by calling surface. The helper's old
`claude-code/<version>` User-Agent was classified as an unknown surface and
answered `ineligible_reason: "surface"` with no grants. Claude Code's own OAuth
API client identifies as `claude-cli/<version> (external, cli)`, for which the
same account is eligible. The helper already borrows Claude Code's identity for
this endpoint (ADR-0001); this is the same identity, stated precisely.

## Decision

- Request the usage endpoint with `cedar_ember=1` and the `claude-cli` User-Agent
  Claude Code's OAuth client sends. Spend data remains in the response, so the
  existing quota and credit buckets are unchanged.
- Parse the `cedar_ember` block into provider **resets** (type
  `claudeLimitReset`, title, expiry, what it clears, whether it needs a limit)
  and a `claudeReset` meta block (eligibility, reason, offered grant, cooldown).
  Nothing is listed while the account or surface is ineligible; the reason is
  kept in meta so the UI can explain rather than guess.
- Add `dankaiusage claude-reset status|use [--grant <id>]`. `use` is the only
  account mutation: it re-reads the cached availability, refuses without an
  eligible, unexpired grant with resets left, saves an `attempted` record with
  the request id **before** sending, then records the server's outcome. A
  transport failure keeps the record `attempted`; the next `use` always retries
  that grant with the same request id, whatever the cache offers by then, and
  refuses to pick another grant until the server has answered. Sign-in
  problems are detected before anything is saved, so they never disturb an
  unconfirmed attempt, and rate-limit, auth or "unavailable" answers to a
  retry leave it unconfirmed as well. The record stores the organization that
  made the attempt; another account cannot resend it, and an explicit `forget`
  exists for that case. The server, not the helper, decides whether the
  earlier request was already applied.
  `reset` and `already_used` count as success and expire the usage cache so the
  next summary fetches the refilled windows immediately; every other outcome
  leaves the grant untouched and says so.
- In the widget, a spendable Claude reset shows under the Claude card in
  Advanced: the generic resets line (title, expiry, countdown) and a
  **Use reset now** action that needs a second click within fifteen seconds and
  shows what it does before that click. Simple mode only mentions that a reset
  is available. Uncertain or failed attempts and this session's result remain
  visible in either mode, and an unconfirmed attempt replaces the use button
  with a single-click retry of the same request. There is no automatic or armed use and no setting:
  spending the reset is always a deliberate, confirmed action.
- The helper never reads the grant from live network state when deciding
  eligibility; it uses the shared usage cache, so one refresh cycle governs
  requests as before (ADR-0015). Grants follow the same stale-serve rule as
  the quota windows: a body kept through failed refreshes is trusted only
  within the stale window. The widget reads the saved attempt record on every
  refresh cycle, so an unconfirmed attempt stays visible across restarts.

## Alternatives Considered

- **Leave it to claude.ai or `/limit-reset`.** Works, but the point of the
  widget is to act on the windows it already shows; a visible, expiring grant
  with no way to use it is the gap this fixes.
- **Auto-use at the limit, like the one-shot Codex reset (ADR-0010).** The
  launch grant is usable any time, clears both windows and is a one-off; when
  to spend it is a judgement call about the weekly budget. Manual first; an
  armed variant can follow if grants recur.
- **A separate status request.** Would double the usage API traffic for a
  field the same endpoint already returns when asked.
- **Keep the `claude-code/` User-Agent and send extra surface headers.** The
  `x-app` header alone did not change the surface classification; only the
  exact client User-Agent did, and it is the string the endpoint's own client
  uses.

## Consequences

- One extra query parameter and a changed User-Agent on an undocumented
  endpoint. Both come from Claude Code 2.1.287; a future server change can
  remove the block or the eligibility, in which case the control disappears
  and the status explains why.
- The `claude-reset.json` state file and its lock join the helper's private
  state directory with the existing permissions.
- Using the reset is an account change that cannot be undone. The helper
  requires an explicit `use`, the widget requires two clicks, and nothing is
  ever triggered by install, refresh, or a timer.
