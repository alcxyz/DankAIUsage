# Similar plugins

Several registry plugins cover overlapping needs. Used/remaining views,
provider logos, and configurable bar values are shared features, not exclusive
to DankAIUsage. The dependency distinction is clearest against
[CodexBar](https://github.com/zakstam/dms-codexbar#readme), which wraps the
separate CodexBar CLI, and
[CLIProxyAPI Quota](https://github.com/SpyrosPsarras/dms-cliproxy-quota#readme),
which requires a CLIProxyAPI server with pi-bridge. Other plugins also use
local provider sign-ins directly, so this is not a claim of fewer dependencies
than every alternative. These options are worth considering for different
setups:

| Plugin | When it may fit your workflow |
|---|---|
| [AI Quotas](https://github.com/agneswd/dms-ai-quotas#readme) | You want additional providers and balances, with per-limit pinning and a used/remaining toggle. |
| [AiOverviewControl](https://github.com/bernardopg/AiOverviewControl#readme) | You want a broader provider dashboard with quota notifications, usage analytics, and history export. |
| [Claude Usage](https://github.com/bogdan-velicu/DankClaudeUsage#readme) | You want a focused Claude limit display with rings or numbers and support for existing Claude Code or OpenCode sign-ins. |
| [Claude Code Usage](https://github.com/titeya/dms-claudecode#readme) | You want Claude pacing, daily activity charts, profile breakdowns, and estimated API costs. |
| [CodexBar](https://github.com/zakstam/dms-codexbar#readme) | You already use the CodexBar CLI and want its quota output in DMS. |
| [CLIProxyAPI Quota](https://github.com/SpyrosPsarras/dms-cliproxy-quota#readme) | You want to monitor accounts behind a CLIProxyAPI server running pi-bridge. |

These comparisons describe the linked projects' documentation as reviewed in
September 2026; check their current documentation for changes.
