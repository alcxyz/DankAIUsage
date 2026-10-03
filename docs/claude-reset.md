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
and the quotas refresh right away. If the request could not be confirmed, the
line says so; try again in a moment and the helper repeats the same request
rather than starting a second one. Only the provider decides whether a reset is
eligible, so a `not available` answer leaves the reset untouched.

Nothing uses a reset automatically. There is no setting to arm, and refreshing
usage, opening the dropdown, or installing the plugin never redeems one.

## From the terminal

```sh
dankaiusage claude-reset status
dankaiusage claude-reset use            # the grant Claude offers next
dankaiusage claude-reset use --grant ID # a specific grant from status
```

`status` reads the cached usage data and the helper's own attempt record; it
contacts no server. `use` sends one redemption request and prints the result.
Both print JSON.

## Why it may be missing

The usage API only reports resets for eligible accounts and recognised
clients. The helper identifies itself as Claude Code's OAuth client; if Claude
still answers that the client or plan is not eligible, the card shows no reset
and `status` names the reason. The reset remains usable on claude.ai.

See [ADR-0022](adr/ADR-0022-manual-claude-limit-reset.md) for the design and
[Data sources](data-sources.md) for the request details.
