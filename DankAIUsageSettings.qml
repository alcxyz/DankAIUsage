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

    Column {
        id: refreshIntervalSetting

        property string settingKey: "refreshInterval"
        property int refreshSeconds: 300

        width: parent.width
        spacing: Theme.spacingS

        function normalizedSeconds(value) {
            var seconds = Number(value)
            if (!isFinite(seconds) || seconds <= 0) seconds = 300
            return Math.max(180, Math.min(3600, Math.round(seconds / 60) * 60))
        }

        function loadValue() {
            var saved = root.loadValue("refreshInterval", 300)
            var normalized = normalizedSeconds(saved)
            refreshSeconds = normalized
            if (root.pluginService && normalized !== Number(saved))
                root.saveValue("refreshInterval", normalized)
        }

        function setMinutes(minutes) {
            var seconds = normalizedSeconds(Math.round(minutes) * 60)
            refreshSeconds = seconds
            root.saveValue("refreshInterval", seconds)
        }

        Component.onCompleted: loadValue()

        StyledText {
            text: "Usage refresh interval"
            font.pixelSize: Theme.fontSizeMedium
            font.weight: Font.Medium
            color: Theme.surfaceText
        }

        StyledText {
            text: "Less frequent updates reduce background activity and local history scans. Automatic reset checks use the same interval, so long intervals can delay detection or miss a brief expiry window."
            width: parent.width
            wrapMode: Text.WordWrap
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
        }

        Row {
            width: parent.width
            spacing: Theme.spacingM

            StyledText {
                id: refreshIntervalValue

                text: Math.round(refreshIntervalSetting.refreshSeconds / 60) + " min"
                anchors.verticalCenter: parent.verticalCenter
                font.pixelSize: Theme.fontSizeMedium
                font.weight: Font.Medium
                color: Theme.primary
            }

            Item { width: Math.max(0, parent.width - refreshIntervalValue.width - resetRefreshInterval.width - parent.spacing * 2); height: 1 }

            DankButton {
                id: resetRefreshInterval

                text: "Reset to 5 min"
                buttonHeight: 32
                enabled: refreshIntervalSetting.refreshSeconds !== 300
                activeFocusOnTab: true
                Accessible.role: Accessible.Button
                Accessible.name: text
                Accessible.onPressAction: if (enabled) refreshIntervalSetting.setMinutes(5)
                Keys.onEnterPressed: if (enabled) refreshIntervalSetting.setMinutes(5)
                Keys.onReturnPressed: if (enabled) refreshIntervalSetting.setMinutes(5)
                Keys.onSpacePressed: if (enabled) refreshIntervalSetting.setMinutes(5)
                onClicked: refreshIntervalSetting.setMinutes(5)
            }
        }

        Item {
            id: refreshSliderArea

            width: parent.width
            height: 68

            DankSlider {
                id: refreshSlider

                anchors.top: parent.top
                width: parent.width
                value: Math.round(refreshIntervalSetting.refreshSeconds / 60)
                minimum: 3
                maximum: 60
                step: 1
                unit: " min"
                wheelEnabled: false
                thumbOutlineColor: Theme.withAlpha(Theme.surfaceContainerHighest, Theme.popupTransparency)
                onSliderValueChanged: newValue => refreshIntervalSetting.setMinutes(newValue)
            }

            Rectangle {
                id: defaultMarker

                readonly property real markerRatio: (5 - refreshSlider.minimum) / (refreshSlider.maximum - refreshSlider.minimum)

                // DankSlider's four-pixel handle travels across width - 4.
                x: Math.round(markerRatio * (parent.width - 4) + 1)
                y: 15
                width: 2
                height: 18
                radius: 1
                color: Theme.primary
                z: 2

                MouseArea {
                    anchors.centerIn: parent
                    width: 20
                    height: 32
                    cursorShape: Qt.PointingHandCursor
                    onClicked: refreshIntervalSetting.setMinutes(5)
                }
            }

            StyledText {
                text: "Default 5 min"
                x: Math.max(0, Math.min(parent.width - width, defaultMarker.x + defaultMarker.width / 2 - width / 2))
                anchors.bottom: parent.bottom
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.Medium
                color: Theme.primary
            }

            StyledText {
                text: "60 min"
                anchors.right: parent.right
                anchors.bottom: parent.bottom
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
            }
        }
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
        label: "Claude weekly limits by default"
        description: "Default for weekly limits without an individual choice. Choose each reported weekly limit independently under Advanced → Bar controls in the dropdown; individual choices take precedence."
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
