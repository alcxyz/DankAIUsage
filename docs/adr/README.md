# Architecture Decision Records

| ADR | Title | Area |
|---|---|---|
| [ADR-0001](ADR-0001-claude-limits-from-oauth-usage-api.md) | Read Claude subscription limits from the OAuth usage endpoint | cmd/dankaiusage |
| [ADR-0002](ADR-0002-authoritative-window-percentages.md) | Display authoritative subscription-window percentages, not token-derived estimates | widget + cmd/dankaiusage |
| [ADR-0003](ADR-0003-token-history-from-local-cli-artifacts.md) | Token history from local CLI artifacts only | cmd/dankaiusage |
| [ADR-0004](ADR-0004-cached-token-display-policy.md) | Cached-token display policy and per-provider total semantics | widget + cmd/dankaiusage |
| [ADR-0005](ADR-0005-claude-limits-via-statusline-capture.md) | Claude limits via statusline capture (superseded) | cmd/dankaiusage |
| [ADR-0006](ADR-0006-opt-in-claude-prime.md) | Opt-in Claude prime with local timer fallback (superseded) | cmd/dankaiusage |
| [ADR-0007](ADR-0007-auto-prime-as-window-scheduler.md) | Auto-prime as session-window scheduler | widget + cmd/dankaiusage |
| [ADR-0008](ADR-0008-codex-duration-based-windows-and-banked-resets.md) | Classify Codex windows by duration and expose banked resets | widget + cmd/dankaiusage |
| [ADR-0009](ADR-0009-provider-defined-quota-buckets.md) | Render provider-defined quota buckets, including Claude credits | widget + cmd/dankaiusage |
| [ADR-0010](ADR-0010-one-shot-codex-reset.md) | Opt-in, one-shot Codex reset scheduling | widget + cmd/dankaiusage |
| [ADR-0011](ADR-0011-local-reset-observation-history.md) | Bounded local quota and reset history | widget + cmd/dankaiusage |
| [ADR-0012](ADR-0012-optional-persistent-token-totals.md) | Optional persistent local token totals | widget + cmd/dankaiusage |
| [ADR-0013](ADR-0013-dropdown-detail-modes.md) | Simple and Advanced dropdown modes | widget |
| [ADR-0014](ADR-0014-user-reported-history-explanations.md) | User-reported explanations for quota changes | widget + cmd/dankaiusage |
| [ADR-0015](ADR-0015-shared-usage-refresh-cooldown.md) | Shared usage refresh cooldown | widget + cmd/dankaiusage |
| [ADR-0016](ADR-0016-private-local-diagnostics.md) | Allowlisted local diagnostics | helper + widget + packaging |
