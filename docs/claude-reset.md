# Claude limit resets

Anthropic occasionally grants paid Claude plans a saved **limit reset** that
refills your usage windows when you choose to use it, until it expires. The
same reset appears on claude.ai under **Settings → Usage** and behind Claude
Code's `/limit-reset` command.

## In the dropdown

When your account has a usable reset, the Claude card lists it like a Codex
reset: its title, expiry and countdown, plus what it clears and whether it can
be used before you hit a limit. In **Advanced**, a **Use reset now** action
sits under it. The first click shows what will happen; a second click within
fifteen seconds sends the request. **Keep it** cancels. Simple mode only notes
that a reset is available.

The outcome appears under the card. `Limits reset` means Claude confirmed it
and the quotas refresh right away. If the request could not be confirmed (a
timeout, a rate-limit answer, a sign-in problem), the line says so and a
**Retry unconfirmed reset** action replaces the use button in both modes. The
retry repeats the same request for the same grant, so it cannot spend a second
reset; Claude answers with the earlier outcome. Only the provider decides
whether a reset is eligible, so a `not available` answer leaves the reset
untouched. A reset that can only be used at a limit is offered as-is; Claude
keeps it if you are not at one, and the confirmation says so beforehand.

Nothing uses a reset automatically. There is no setting to arm, and refreshing
usage, opening the dropdown, or installing the plugin never redeems one.

## From the terminal

```sh
dankaiusage claude-reset status
dankaiusage claude-reset use            # the grant Claude offers next, or retry
dankaiusage claude-reset use --grant ID # a specific grant from status
dankaiusage claude-reset forget         # drop an unconfirmed attempt record
```

`status` reads the cached usage data and the helper's own attempt record; it
contacts no server. `use` sends one redemption request and prints the result;
while an attempt is unconfirmed it always retries that attempt first. An
attempt is bound to the Claude account that made it. After switching accounts,
sign back in to retry it, or run `forget` once you have checked the outcome on
claude.ai; `forget` never contacts Claude. All commands print JSON.

## Why it may be missing

The usage API only reports resets for eligible accounts and recognised
clients. The helper identifies itself as Claude Code's OAuth client; if Claude
still answers that the client or plan is not eligible, the card shows no reset
and `status` names the reason. The reset remains usable on claude.ai.

See [ADR-0022](adr/ADR-0022-manual-claude-limit-reset.md) for the design and
[Data sources](data-sources.md) for the request details.
