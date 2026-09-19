# DankAIUsage

A [DankMaterialShell](https://github.com/AvengeMedia/DankMaterialShell) widget
for Codex and Claude subscription quotas, extra-usage credits, and local token
history. A small Go helper (`dankaiusage`) collects the data using the
provider CLIs and their existing local sign-ins. No proxy, no extra
monitoring app.

![Advanced dropdown with Codex and Claude quotas, local token history, and reset controls](docs/screenshot.png)

![Simple dropdown focused on Codex and Claude quotas](docs/screenshot-simple.png)

![AI Usage on the DankBar with provider logos and minimal quota percentages](docs/screenshot-bar.png)

## What you get

- **Every limit the provider reports**, as a list of quota bars: Codex weekly
  and model-scoped windows, Claude five-hour, weekly, model-scoped, and
  extra-usage credits. Nothing is inferred from position in a response.
- **Reset countdowns** inline on each row, exact time on hover, and colors
  that turn to warning at 25% and error at 10% remaining.
- **Simple or Advanced** dropdown, remembered. Advanced adds an overview,
  local token history with an optional persistent tracked total, available
  Codex resets, reset history, and diagnostics.
- **Top bar** with provider logos and the quotas you choose; percentages
  take the same warning colors.
- **Left / Used** switch for all percentages and bars at once.
- **Opt-in automation**: a one-shot Codex reset that fires once near expiry
  or exhaustion, and Claude prime as a session-window scheduler. Both are off
  by default. The Codex control is always accessible in plugin settings and
  appears in Advanced when a spendable reset is available. Armed controls and
  recovery messages remain visible even with no resets. Arming with no eligible
  reset leaves the control off; it does not automatically use future credits.
- **Local reset history** with an optional "What changed?" prompt, and an
  optional, alpha-quality public reset feed. Section badges count unread items
  and clear when viewed; read state survives restarts.
  Confirmed plugin resets are linked to matching later refill and credit
  observations, preserving the action and observation timestamps.

The scope is deliberately two providers. Token history covers local CLI
transcripts only, so it is not a complete account ledger. Subscription
percentages come from the providers, never from token estimates.

## Install

The widget needs both the plugin files and the `dankaiusage` helper on the DMS
process's `PATH`.

**Nix**

```sh
nix profile install github:alcxyz/DankAIUsage/dev
```

The default package contains both the helper and a stamped plugin directory at
`share/dms-plugins/DankAIUsage`. Use that directory as the DMS plugin source,
so DMS and the helper display the same build version. For example:

```nix
let
  source = inputs.dms-plugins.srcs.aiusage;
  package = pkgs.callPackage "${source}/default.nix" {
    revision = source.rev or source.dirtyRev or null;
  };
in {
  home.packages = [ package ];
  programs.dank-material-shell.plugins.DankAIUsage.src =
    "${package}/share/dms-plugins/DankAIUsage";
}
```

Use your DMS module's plugin-source option for the last assignment. Release
packaging is explicit: build `github:alcxyz/DankAIUsage/v1.0.0#release` (replace
the tag with the desired published release). An untagged branch build uses the
development package by default, even when its manifest base version matches a
release.

**Manual** (Go version from `go.mod`, plus Python 3)

```sh
python3 scripts/package.py --output dist/dev
install -Dm755 dist/dev/bin/dankaiusage ~/.local/bin/dankaiusage
mkdir -p ~/.config/DankMaterialShell/plugins/DankAIUsage
cp -R dist/dev/share/dms-plugins/DankAIUsage/. ~/.config/DankMaterialShell/plugins/DankAIUsage/
```

Choose a fresh output directory for each build. Make sure `~/.local/bin` is on
the shell's `PATH` before starting DMS. To package an official release, check
out its `vX.Y.Z` tag in a clean checkout and add `--release` to the packaging
command; it refuses a mismatched tag or modified checkout.

Development packages show `X.Y.Z-dev.<12-character-commit>`, with `.dirty` for
local modifications. Nix source-only imports use `X.Y.Z-dev.source.<fingerprint>`
when Git metadata is unavailable; manual source archives use `X.Y.Z-dev.unknown`.
Both installed `plugin.json` and the helper receive this same version. The
tracked `plugin.json` stays `X.Y.Z` for official release tagging. An ordinary
`go build` still works for helper-only development and reports `dev-<commit>`
(or `dev` without Git metadata); use the packaging command when installing DMS.

Then enable **AI Usage** in DMS plugin settings and add it to your bar.

## Set up providers

1. Install and sign in to the CLI for each provider you use: `codex`,
   `claude`, or both.
2. Disable the provider you do not use under **Settings → Plugins → AI
   Usage**.
3. Optional: pick which Claude quotas appear in the top bar, and the refresh
   interval (default five minutes).

If Claude quotas disappear later, run `claude auth status` and sign in again
if needed; the widget recovers on its next refresh.

## Learn more

| Topic | Read |
|---|---|
| Reading the dropdown: modes, quota bars, countdowns, local tokens, tracked totals | [docs/dropdown.md](docs/dropdown.md) |
| Plugin settings, top-bar layout, Claude quota selection | [docs/settings.md](docs/settings.md) |
| One-shot Codex reset | [docs/codex-reset.md](docs/codex-reset.md) |
| Reset history, explaining changes, public announcements (Alpha) | [docs/reset-history.md](docs/reset-history.md) |
| Where the numbers come from, refresh cooldown, Claude statusline fallback, Claude prime | [docs/data-sources.md](docs/data-sources.md) |
| Troubleshooting and local diagnostics | [docs/diagnostics.md](docs/diagnostics.md) |
| Helper CLI reference | [docs/cli.md](docs/cli.md) |
| Similar plugins and when they fit better | [docs/similar-plugins.md](docs/similar-plugins.md) |
| Anecdotal usage tips | [docs/usage-tips.md](docs/usage-tips.md) |
| Design decisions | [docs/adr/README.md](docs/adr/README.md) |

## Privacy in one paragraph

The helper reads provider sign-ins locally and never prints credentials.
Everything the widget stores is local and bounded: quota snapshots, reset
observations, optional explanations, hashed token checkpoints, and a
diagnostics log with no raw errors or identifiers. The only optional network
call beyond the two providers is the public reset feed, which is off by
default.

## License

[MIT](LICENSE)
