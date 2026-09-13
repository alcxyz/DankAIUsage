# One-shot Codex reset

The Codex card includes **Auto-use one reset**, off by default. Hover the
switch for the trigger rules; its status line appears only while it is armed,
unknown, or reporting a problem.

## What it does

Turning it on selects the earliest-expiring available reset with a known ID
and expiry. It waits until general Codex usage reaches 99%, or until ten
minutes before that reset expires with some general allowance used in a window
whose natural reset time is known and still in the future. It avoids the 99%
trigger when a natural quota reset is already within ten minutes. Spark usage
alone does not trigger it.

The control turns off before its single redemption attempt, including if that
attempt fails. After an uncertain result, check Codex's usage page before
arming again. It never purchases credits or chooses another reset silently.
Only the provider decides whether a window is eligible to reset.

If you are considering a subscription change, see the cautious, anecdotal
[upgrade timing note](usage-tips.md#timing-a-codex-subscription-upgrade).

## Limits

Checks follow the selected usage refresh interval while DMS is running and
Codex is visible. Sleeping, closing DMS, hiding Codex, or losing connectivity
can miss the expiry; there is no separate background service. The timing
balances retained allowance against a small safety margin, rather than
guaranteeing the last possible moment. Longer refresh intervals delay checks
and may miss a credit's expiry window.

The helper owns the state, so restarting DMS does not forget an armed reset and
multiple widget instances cannot independently redeem it. Terminal controls are
listed in the [CLI reference](cli.md#codex-reset).

Requires a Codex CLI exposing the documented
[earned-reset app-server method](https://learn.chatgpt.com/docs/app-server#8-earned-rate-limit-resets-chatgpt).
Unsupported helpers or CLI versions show an error rather than using an
undocumented endpoint. See
[ADR-0010](adr/ADR-0010-one-shot-codex-reset.md) for the design.

You can also apply a reset yourself from Codex **Settings → Usage**.
