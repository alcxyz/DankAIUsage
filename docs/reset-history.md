# Reset history

Expand **Reset history** at the bottom of the Advanced dropdown to see the
latest four observed events for your enabled providers. **Show more** reveals
the next four; closing the dropdown returns to the first page. The helper keeps at
most 200 events for 30 days, locally, without a separate service or database.
History starts with the first observation; it cannot reconstruct earlier
resets.

Each entry includes the observation time, previous sample time where
available, before/after allowance, and changes to the reset schedule or
earned-reset count. Left/Used also controls historical allowance percentages.

The history file is `$XDG_STATE_HOME/dankaiusage/usage-history.json`, falling
back to `~/.local/state/dankaiusage/usage-history.json`. Read the retained
events without contacting either provider with `dankaiusage history`.

## Event categories

- **Scheduled window change:** a rollover consistent with the previous reset
  schedule; inferred from snapshots.
- **Unexpected replenishment:** allowance increased early. This can suggest a
  provider-granted reset, but an external manual reset, account/plan change,
  or corrected measurement cannot be ruled out.
- **Likely reset redeemed:** an early general Codex refill coincided with fewer
  available earned resets, and known expiry evidence does not explain the
  decrease. This is an inference, not a confirmed manual action.
- **Available resets changed:** the count changed; this alone cannot establish
  whether credits were granted, redeemed, expired, or withdrawn.
- **Reset time changed:** the expected reset time moved, not that allowance
  was refilled. If the recorded percentage is unchanged, the status is **Reset
  time changed · usage unchanged**, and its details show usage once with the
  old/new reset times.
- **Minor reset-time adjustment:** reset-time fluctuations of up to five
  seconds are treated as timing noise. They stay in history with any notes
  preserved but do not trigger a question.
- **Plugin reset applied / outcome unknown:** the result of an explicit
  one-shot plugin attempt, kept separate from inferred observations.

The log records when a change was observed, not its exact occurrence time.
Unchanged reset counts do not prove provider generosity: a new credit could
offset a redemption. Sleep, unavailable data, caching, and gaps between polls
can hide intermediate events. Automatic observations do not store credentials,
account identifiers, prompts, or opaque reset-credit IDs.

Observed Claude behavior: a reported reset time can move several hours later
while allowance usage remains unchanged. This is a schedule observation, not
proof of a refill or a known provider policy. Keep **Other reset events**
checked to review repeats under **Reset time changed**. Compare the old/new
reset times and observation interval; add an explanation only when the cause
is known. If it recurs, review and share only the relevant sanitized details.

## Filters

Reset history hides routine scheduled five-hour and weekly events by default.
Its separate **Scheduled 5-hour resets** and **Scheduled weekly resets**
checkboxes remember your choices. **Other reset events** is checked by default
and includes unexpected refills, redemptions, timing changes, and unclassified
windows. Uncheck all three to hide all events. Recording and retention are
unchanged; the latest four matching events are shown first, so routine events
do not crowd out unexpected refills or reset redemptions. Unknown window types
follow Other.

## Explain an unexpected change

Clear early refills and redemptions supported by a simultaneous drop in
available resets are recorded quietly, without asking you to supply a cause.
Optional explanations remain available in Advanced history. Missing or
contradictory evidence, or unexplained companion changes, can still warrant a
question.

Both Simple and Advanced can show a compact **What changed?** prompt for the
latest unexplained change observed within the last 24 hours. Related changes
sampled together for one provider share a response. Ordinary scheduled
resets, confirmed plugin actions, and reset-count drops fully explained by
known expiry evidence do not prompt.

Choose a relevant explanation: **Changed subscription**, **Used a reset
elsewhere**, **Switched account/workspace**, **Provider announced a
bonus/reset**, or **Not sure**. You can add a short optional note; it is
stored locally with history, so do not include sensitive information.
**Dismiss** hides the prompt without claiming a cause. Answering or
dismissing the latest change does not bring up a queue of older prompts.

Use **Explain / Edit explanation** in Advanced reset history to add context
later or correct your choice. Explanations are labelled **user reported** and
kept alongside the original observations, not substituted for them. They do
not confirm provider generosity or establish that a reset was redeemed.
Saving an explanation does not contact providers or change tracking or
automation. A failed save leaves the draft available for retry. Explanations
and notes expire with their events under the existing 30-day/200-event
history limit. Existing history migrates automatically when saved. Older
helpers cannot read the new history format, so keep the helper and widget
updated together; a downgrade leaves the saved history intact rather than
silently erasing notes.

See [ADR-0014](adr/ADR-0014-user-reported-history-explanations.md) for the
grouping and attribution policy.

## Notifications

The **Reset history** and **Public reset announcements** disclosure rows show
an unread badge. Viewing a section while the dropdown is open marks its
displayed items read. Read state is stored in plugin state and shared by every
bar instance, so a badge cleared on one monitor clears everywhere.

With **Desktop notifications** enabled (the default), a new history change
observed within the last day raises one desktop notification per observation
group, and a new public announcement raises one per report. More than three
new items in one refresh collapse into a single summary notification. Each
item notifies at most once across restarts and bar instances; items already
viewed in the dropdown, items that existed before the setting was first
tracked, and a stale public feed never notify. Turning the setting off keeps
tracking, so enabling it later does not replay old items.

## Public reset announcements (Alpha, optional)

Enable **Public reset announcements (Alpha)** in settings to read the
[TokenResets public feed](https://tokenresets.com/api/). It is off by default.
This experimental integration includes public reports, advance alerts, and
local reset matching. Third-party coverage and matching may be incomplete or
incorrect. Do not rely on it to decide when to spend quota or redeem a reset.
Alpha applies to these feed-dependent features, not to the entire plugin
release.

The feed host receives your IP address and ordinary request metadata, but the
plugin sends no credentials, account data, usage totals, history or notes.
See the service [privacy notice](https://tokenresets.com/privacy/).

Checks run separately from quota collection every fifteen minutes with shared
cooldowns and conditional caching. A feed outage cannot make your quotas
unavailable. Public content uses XDG cache; request reservations and
notification receipts use durable state. No additional application or login
is required.

Explicit upcoming resets marked verified by TokenResets can generate a DMS
toast (or a desktop notification when **Desktop notifications** is enabled)
and appear in either dropdown mode. Times are displayed locally;
unknown timing and eligibility stay unknown. Advanced also shows recent
reports and links to the evidence, four at a time with a **Show more** control. Corrections replace the previous snapshot;
stale feeds cannot trigger alerts or matching. Completed historical reports do
not generate notifications on installation or restart.

Nearby public reset reports can appear alongside a clear local refill in
Advanced history. This is a possible association, not confirmation of why
your account changed. Original observations and your explanations are
preserved. Banked-reset grants remain separate from immediate usage refills.
There are no statistical predictions, rumor alerts, or feed-driven automation
changes. See [ADR-0017](adr/ADR-0017-public-reset-announcements.md).
