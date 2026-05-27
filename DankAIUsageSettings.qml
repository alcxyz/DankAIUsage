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
        label: "Rolling period (days)"
        description: "How many days to include in the main total"
        minimum: 1
        maximum: 90
        defaultValue: 7
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
