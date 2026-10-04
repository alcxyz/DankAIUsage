# Claude limit resets

Anthropic occasionally grants paid Claude plans a saved **limit reset** that
refills your usage windows when you choose to use it, until it expires. The
same reset appears on claude.ai under **Settings → Usage** and behind Claude
Code's `/limit-reset` command.

## In the dropdown

When your account has a usable reset, the Claude card in **Advanced** lists
it like a Codex reset: its title, expiry and countdown, with a **Use reset
now** action under it. The first click shows what will happen, naming the
limits the reset clears, whether it only works at a limit, and the uses left
when there is more than one; a second click within fifteen seconds sends the
request. **Keep it** cancels. Simple mode does not mention resets.

The outcome appears under the card. `Limits reset` means Claude confirmed it
and the quotas refresh right away. Answers such as `not available`, a
rate-limit response, or a sign-in problem leave the reset untouched; the line
says so and the use button stays. If no answer arrived (a timeout, or a reply
the helper could not read), the attempt is **unconfirmed**: a **Retry
unconfirmed reset** action replaces the use button in both modes. The retry
repeats the same request for the same grant, so it cannot spend a second reset;
only `Limits reset` or `already used` settle it. If it keeps failing, check
Settings → Usage on claude.ai and then use **Forget attempt** (two clicks); it
drops only the local record. The same action replaces a saved record the helper
cannot read, for example after a downgrade. The second click of **Use reset
now** sends the grant the first click described; if Claude offers a different
grant in between, the button simply asks again. Only the provider decides
whether a reset is eligible. A reset that can only be used at a limit is offered as-is; Claude
keeps it if you are not at one, and the confirmation says so beforehand.

Nothing uses a reset automatically. There is no setting to arm, and refreshing
usage, opening the dropdown, or installing the plugin never redeems one.

## From the terminal

```sh
dankaiusage claude-reset status
dankaiusage claude-reset use            # the grant Claude offers next
dankaiusage claude-reset use --grant ID # a specific grant from status
dankaiusage claude-reset retry          # resend an unconfirmed attempt only
dankaiusage claude-reset forget         # drop an unconfirmed attempt record
```

`status` reads the cached reset availability and the helper's own attempt
record; it contacts no server. `use` sends one redemption request and prints the result;
while an attempt is unconfirmed it always retries that attempt first, and
`retry` does only that, refusing when nothing is pending. An attempt is bound to the Claude account that made it, and cached grants are
offered only to the sign-in that fetched them. After switching accounts, sign
back in to retry it, or run `forget` once you have checked the outcome on
claude.ai; `forget` never contacts Claude. All commands print JSON.

## Why it may be missing

The usage API only reports resets for eligible accounts and recognised
clients. The helper checks for resets at most every 30 minutes in a separate
request that identifies itself as Claude Code's OAuth client; the quota poll
itself never does. If Claude answers that the client or plan is not eligible,
or rate-limits the check, the card shows no reset and `status` names the
reason while the quotas keep refreshing. The Advanced warning for a failed
check appears only when no listing from the last six hours is available; a
rate-limited or unreachable check while a recent result is still shown stays
quiet. A sign-in rejection by Claude is shown beside the listing right away. The reset remains usable on
claude.ai. A new grant, or one spent elsewhere, can take up to 30 minutes to
show.

See [ADR-0022](adr/ADR-0022-manual-claude-limit-reset.md) and
[ADR-0023](adr/ADR-0023-separate-claude-reset-availability-check.md) for the
design and [Data sources](data-sources.md) for the request details.
