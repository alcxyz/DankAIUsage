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

    SliderSetting {
        settingKey: "sessionHours"
        label: "Session window (hours)"
        description: "Rolling window used for session allowance estimates"
        minimum: 1
        maximum: 24
        defaultValue: 5
    }

    ToggleSetting {
        settingKey: "showCodex"
        label: "Show Codex"
        description: "Display usage collected from the local Codex CLI data store"
        defaultValue: true
    }

    ToggleSetting {
        settingKey: "showClaude"
        label: "Show Claude"
        description: "Display usage collected from the local Claude CLI data store"
        defaultValue: true
    }

    StyledText {
        text: "Codex allowance"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    StringSetting {
        settingKey: "codexSessionLimit"
        label: "Session limit"
        description: "Token allowance for the configured session window. Use 0 when unknown."
        placeholder: "0"
    }

    StringSetting {
        settingKey: "codexWeeklyLimit"
        label: "Weekly limit"
        description: "Token allowance for the current local calendar week. Use 0 when unknown."
        placeholder: "0"
    }

    StyledText {
        text: "Claude allowance"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    StringSetting {
        settingKey: "claudeSessionLimit"
        label: "Session limit"
        description: "Token allowance for the configured session window. Use 0 when unknown."
        placeholder: "0"
    }

    StringSetting {
        settingKey: "claudeWeeklyLimit"
        label: "Weekly limit"
        description: "Token allowance for the current local calendar week. Use 0 when unknown."
        placeholder: "0"
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
        description: "Show only the combined rolling total in the bar"
        defaultValue: false
    }
}
