# Troubleshooting and diagnostics

## Common problems

**Claude quotas disappeared.** Run `claude auth status`. If signed out, run
`claude auth login`. The helper backs off briefly after authentication errors;
after the retry window expires, click Refresh in the dropdown or wait for the
next automatic refresh. Restarting DMS is not required after signing in.

**The widget cannot run its helper.** Check `dankaiusage version` from the
same environment as DMS. For Nix installations, ensure the helper and plugin
files come from the same revision.

**A provider shows as available but has no quotas.** Available means the CLI
is on `PATH`, not that its account is signed in. Sign in with the provider
CLI, then Refresh. `dankaiusage summary --pretty` reports provider diagnostics
under `meta`.

**Claude limits show `--`.** Check `meta.limitError` in the summary output.
Claude limits come only from the usage request, so its error explains the
missing values. See
[Data sources](data-sources.md#where-the-numbers-come-from).

## Local diagnostics

Expand **Advanced → Diagnostics** at the bottom of the dropdown to preview
recent failures and recoveries. **Refresh report** reads local state only; it
does not request provider usage. **Copy report** copies exactly the preview
for you to review and share in an issue. Nothing is uploaded automatically.
The same local report is available with `dankaiusage diagnostics`.

When the latest Claude limit-reset check failed for a reason other than a
sign-in rejection, for example a rate limit, the section also shows a muted
line with the reason, the last successful check and the next attempt. It
comes from the current usage summary, not from the stored report, so
**Copy report** does not include it.

Diagnostics retain at most 100 events for seven days in
`$XDG_STATE_HOME/dankaiusage/diagnostics.json`, falling back to
`~/.local/state/dankaiusage/diagnostics.json`. Files are owner-only and
bounded; old events are pruned when diagnostics are accessed. Durable quota
refresh reservations also belong in state, not disposable cache.

Reports contain only event timestamps, provider names, predefined categories,
HTTP status/cooldown information, and validated build identifiers. They
exclude raw errors, responses, credentials, account IDs, paths, usage totals,
prompts, and notes. Report timestamps use UTC (`Z`) for unambiguous issue
reports. Timestamps can reveal activity times: preview before sharing. Local
failures distinguish refresh-lock timeouts, invalid timestamps, invalid
caches, and other state failures without including raw error details. Build
identifiers distinguish development revisions; flake-less Nix packages use a
public-source fingerprint. Diagnostics start with this version and cannot
recover failures that were previously overwritten or never recorded. See
[ADR-0016](adr/ADR-0016-private-local-diagnostics.md).
