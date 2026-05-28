import QtQuick
import qs.Common
import qs.Widgets
import qs.Modules.Plugins

PluginSettings {
    id: root

    pluginId: "dankAIUsage"

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
        label: "Token history (days)"
        description: "How many days to include in the secondary token total"
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
        text: "Subscription limits"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    StyledText {
        text: "Codex is queried from the local Codex app server. Claude is cached from Claude Code statusline data."
        width: parent.width
        wrapMode: Text.WordWrap
        font.pixelSize: Theme.fontSizeSmall
        color: Theme.surfaceVariantText
    }

    ToggleSetting {
        settingKey: "includeCachedTokens"
        label: "Include cached tokens"
        description: "Include cache read and cache creation tokens in displayed totals"
        defaultValue: true
    }

    ToggleSetting {
        settingKey: "compactPill"
        label: "Compact pill"
        description: "Show only the lowest remaining subscription percentage in the bar"
        defaultValue: false
    }

    ToggleSetting {
        settingKey: "focusWeekly"
        label: "Focus weekly limits"
        description: "Use weekly instead of session limits for compact and summary emphasis"
        defaultValue: false
    }
}
