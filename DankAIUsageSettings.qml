import QtQuick
import qs.Common
import qs.Widgets
import qs.Modules.Plugins

PluginSettings {
    id: root

    pluginId: "dankAIUsage"

    ToggleSetting {
        settingKey: "showUsed"
        label: "Show used allowance"
        description: "Show used percentages and bar fill instead of remaining allowance. Also available as Left / Used in the dropdown."
        defaultValue: false
    }

    SliderSetting {
        settingKey: "refreshInterval"
        label: "Refresh interval (seconds)"
        description: "How often to poll local Codex and Claude usage data"
        minimum: 30
        maximum: 1800
        defaultValue: 300
    }

    SliderSetting {
        settingKey: "periodDays"
        label: "Legacy token history (days)"
        description: "Kept for compatibility with older cached summaries; choose 5h, 7d, 30d, 90d, or Tracked in the dropdown"
        minimum: 1
        maximum: 90
        defaultValue: 7
    }

    ToggleSetting {
        settingKey: "showCodex"
        label: "Show Codex"
        description: "Display subscription limits and local token history from Codex"
        defaultValue: true
    }

    ToggleSetting {
        settingKey: "showClaude"
        label: "Show Claude"
        description: "Display cached Claude Code subscription limits and local token history"
        defaultValue: true
    }

    StyledText {
        text: "Top bar: icons"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    ToggleSetting {
        settingKey: "barShowPluginIcon"
        label: "AI Usage plugin icon"
        description: "Show the generic monitoring icon at the start of the top-bar pill"
        defaultValue: false
    }

    ToggleSetting {
        settingKey: "barShowProviderLogos"
        label: "Provider logos"
        description: "Show the OpenAI and Claude logos beside their top-bar quota values"
        defaultValue: true
    }

    StyledText {
        text: "Top bar: Claude quotas"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    ToggleSetting {
        settingKey: "barShowClaudeSession"
        label: "Claude 5-hour session"
        description: "Include Claude's current five-hour window in the top-bar overview"
        defaultValue: true
    }

    ToggleSetting {
        settingKey: "barShowClaudeWeekly"
        label: "Claude weekly limit"
        description: "Include Claude's weekly window in the top-bar overview"
        defaultValue: true
    }

    ToggleSetting {
        settingKey: "barShowClaudeCredits"
        label: "Claude extra-use credits"
        description: "Include Claude's paid extra-usage credit balance in the top-bar overview"
        defaultValue: false
    }

    ToggleSetting {
        settingKey: "enableClaudePrime"
        label: "Enable Claude prime"
        description: "Automatically run a tiny Claude request when no active session timer is known"
        defaultValue: false
    }

    StyledText {
        text: "Subscription limits"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    StyledText {
        text: "Codex limits come from its local app server. Claude uses its existing sign-in with statusline data as a fallback. Arm or cancel the one-shot Codex reset from the dropdown; it is off by default."
        width: parent.width
        wrapMode: Text.WordWrap
        font.pixelSize: Theme.fontSizeSmall
        color: Theme.surfaceVariantText
    }

    ToggleSetting {
        settingKey: "includeCachedTokens"
        label: "Include cached tokens"
        description: "Include cache read and cache creation tokens in displayed totals"
        defaultValue: false
    }

    ToggleSetting {
        settingKey: "compactPill"
        label: "Compact pill"
        description: "Show only the most constrained selected quota for each visible provider"
        defaultValue: false
    }

}
