import QtQuick
import Quickshell.Io
import qs.Common
import qs.Widgets
import qs.Modules.Plugins

PluginSettings {
    id: root

    pluginId: "dankAIUsage"

    property var barClaudeWeeklyOverrides: ({})
    property bool barShowClaudeWeeklyValue: true
    property var cachedClaudeWeeklyChoices: []
    property var codexResetStatus: ({ armed: false, stateKnown: false, message: "Checking reset control..." })
    property bool codexResetReady: false
    property string _codexResetOutput: ""
    property string _codexResetAction: ""

    function runCodexReset(action) {
        if (codexResetProcess.running) return
        _codexResetOutput = ""
        _codexResetAction = action
        var refreshInterval = Number(root.loadValue("refreshInterval", 300))
        if (!isFinite(refreshInterval) || refreshInterval <= 0) refreshInterval = 300
        codexResetProcess.command = [
            "dankaiusage", "codex-reset", action,
            "--refresh-interval", "" + refreshInterval
        ]
        codexResetProcess.running = true
    }

    function codexResetDetailText() {
        var message = codexResetStatus.message || "Automatic Codex reset is off"
        if (codexResetStatus.error) message += "\n" + codexResetStatus.error
        if (codexResetStatus.armed && codexResetStatus.expiresAt)
            message = (codexResetStatus.title || "Usage reset") + " · expires "
                    + new Date(codexResetStatus.expiresAt).toLocaleString(Qt.locale(), Locale.ShortFormat)
                    + "\n" + message
        return message
    }

    Process {
        id: codexResetProcess

        running: false
        stdout: SplitParser {
            onRead: data => { root._codexResetOutput += data + "\n" }
        }
        onExited: (exitCode, exitStatus) => {
            var parsed = false
            try {
                var status = JSON.parse(root._codexResetOutput.trim())
                if (typeof status.armed !== "boolean") throw new Error("Invalid reset status")
                if (status.stateKnown === false)
                    status.armed = root.codexResetStatus.armed
                root.codexResetStatus = status
                root.codexResetReady = status.stateKnown === true
                parsed = true
            } catch (e) {
                root.codexResetReady = false
                root.codexResetStatus = {
                    armed: root.codexResetStatus.armed,
                    stateKnown: false,
                    message: "Reset status unavailable. Check the helper version and retry."
                }
            }
            if (parsed && root._codexResetAction !== "status"
                    && root.pluginService && root.pluginService.savePluginState)
                root.pluginService.savePluginState(root.pluginId, "codexResetRevision", Date.now())
        }
    }

    Timer {
        interval: 5000
        repeat: true
        running: root.visible
        onTriggered: if (!codexResetProcess.running) root.runCodexReset("status")
    }

    Component.onCompleted: Qt.callLater(function() { root.runCodexReset("status") })
    onVisibleChanged: if (visible && !codexResetProcess.running) root.runCodexReset("status")

    function claudeWeeklyBarChoices() {
        var summary = root.loadState("lastSummary", null)
        var providers = summary && Array.isArray(summary.providers) ? summary.providers : []
        var choices = []
        var seen = ({})
        for (var i = 0; i < providers.length; i++) {
            if (!providers[i] || providers[i].id !== "claude") continue
            var buckets = Array.isArray(providers[i].quotaBuckets) ? providers[i].quotaBuckets : []
            for (var j = 0; j < buckets.length; j++) {
                var bucket = buckets[j]
                if (bucket && bucket.id && bucket.allowance
                        && bucket.allowance.window === "weekly" && !seen[bucket.id]) {
                    choices.push(bucket)
                    seen[bucket.id] = true
                }
            }
            break
        }
        return choices
    }

    function loadClaudeWeeklySettings() {
        barShowClaudeWeeklyValue = root.loadValue("barShowClaudeWeekly", true) !== false
        var saved = root.loadValue("barClaudeWeeklyOverrides", {})
        barClaudeWeeklyOverrides = saved && typeof saved === "object" && !Array.isArray(saved) ? saved : {}
        cachedClaudeWeeklyChoices = claudeWeeklyBarChoices()
    }

    function claudeBucketShownInBar(bucket) {
        if (!bucket || !bucket.id) return false
        var selected = barClaudeWeeklyOverrides[bucket.id]
        return typeof selected === "boolean" ? selected : barShowClaudeWeeklyValue
    }

    function setClaudeWeeklyBarBucket(bucket, selected) {
        if (!bucket || !bucket.id) return
        var overrides = Object.assign({}, barClaudeWeeklyOverrides)
        overrides[bucket.id] = selected
        barClaudeWeeklyOverrides = overrides
        root.saveValue("barClaudeWeeklyOverrides", overrides)
    }

    Connections {
        target: root.pluginService
        enabled: root.pluginService !== null

        function onPluginStateChanged(changedPluginId) {
            if (changedPluginId === root.pluginId)
                root.cachedClaudeWeeklyChoices = root.claudeWeeklyBarChoices()
        }
    }

    StyledText {
        text: "Display and providers"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    ToggleSetting {
        settingKey: "showUsed"
        label: "Show used allowance"
        description: "Show used percentages and bar fill instead of remaining allowance. Also available as Left / Used in the dropdown."
        defaultValue: false
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

    ToggleSetting {
        settingKey: "includeCachedTokens"
        label: "Include cached tokens"
        description: "Include cached tokens in combined totals. The Input / Cached / Output breakdown always shows cached tokens separately."
        defaultValue: false
    }

    SliderSetting {
        settingKey: "periodDays"
        label: "Legacy token history (days)"
        description: "Kept for compatibility with older cached summaries; choose 5h, 7d, 30d, 90d, or Tracked in the dropdown"
        minimum: 1
        maximum: 90
        defaultValue: 7
    }

    StyledText {
        text: "Codex limits come from its local app server. Claude uses its existing sign-in with statusline data as a fallback. The one-shot Codex reset is off by default and can be managed below or from the Advanced dropdown when a reset is available."
        width: parent.width
        wrapMode: Text.WordWrap
        font.pixelSize: Theme.fontSizeSmall
        color: Theme.surfaceVariantText
    }

    StyledText {
        text: "Top bar layout and icons"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
    }

    ToggleSetting {
        settingKey: "compactPill"
        label: "Compact pill"
        description: "Show only the most constrained selected quota for each visible provider"
        defaultValue: false
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
        text: "Claude quotas in the top bar"
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
        label: "New weekly limits by default"
        description: "Default for reported weekly limits without an individual choice. Individual choices below take precedence."
        defaultValue: true
    }

    Column {
        id: weeklyQuotaSettings

        width: parent.width
        spacing: Theme.spacingS

        property var loadValue: function() {
            root.loadClaudeWeeklySettings()
        }

        Component.onCompleted: Qt.callLater(weeklyQuotaSettings.loadValue)

        StyledText {
            text: "Reported weekly limits"
            font.pixelSize: Theme.fontSizeMedium
            font.weight: Font.Medium
            color: Theme.surfaceText
        }

        StyledText {
            width: parent.width
            text: root.cachedClaudeWeeklyChoices.length > 0
                    ? "Choose each weekly limit independently. These choices come from the latest cached Claude summary."
                    : "No weekly limits are available in the latest cached Claude summary. New limits follow the default above."
            wrapMode: Text.WordWrap
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
        }

        Repeater {
            model: root.cachedClaudeWeeklyChoices

            delegate: Row {
                required property var modelData

                width: parent ? parent.width : 0
                spacing: Theme.spacingM

                StyledText {
                    width: parent.width - weeklyBucketToggle.width - Theme.spacingM
                    anchors.verticalCenter: parent.verticalCenter
                    text: modelData.id === "general-weekly"
                            ? "Weekly (all models)" : (modelData.label || modelData.id)
                    textFormat: Text.PlainText
                    wrapMode: Text.WordWrap
                    font.pixelSize: Theme.fontSizeLarge
                    font.weight: Font.Medium
                    color: Theme.surfaceText
                }

                DankToggle {
                    id: weeklyBucketToggle

                    anchors.verticalCenter: parent.verticalCenter
                    checked: root.claudeBucketShownInBar(modelData)
                    onToggled: isChecked => root.setClaudeWeeklyBarBucket(modelData, isChecked)
                }
            }
        }
    }

    ToggleSetting {
        settingKey: "barShowClaudeCredits"
        label: "Claude extra-use credits"
        description: "Include Claude's paid extra-usage credit balance in the top-bar overview"
        defaultValue: false
    }

    StyledText {
        text: "Collection and automation"
        font.pixelSize: Theme.fontSizeLarge
        font.weight: Font.Bold
        color: Theme.surfaceText
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

    Column {
        id: codexResetSetting

        width: parent.width
        spacing: Theme.spacingS

        StyledText {
            text: "Codex earned reset"
            font.pixelSize: Theme.fontSizeMedium
            font.weight: Font.Medium
            color: Theme.surfaceText
        }

        DankToggle {
            id: codexAutoResetToggle

            width: parent.width
            text: codexResetProcess.running ? "Checking reset..."
                    : !root.codexResetReady ? "Cancel auto reset" : "Auto-use one reset"
            checked: root.codexResetStatus.armed === true
            toggling: codexResetProcess.running
            enabled: !codexResetProcess.running
            activeFocusOnTab: true
            Accessible.role: Accessible.CheckBox
            Accessible.name: text
            Accessible.checked: checked
            Accessible.description: "Uses one eligible earned reset, then turns itself off."
            Accessible.onPressAction: handleClick()
            Accessible.onToggleAction: handleClick()
            Keys.onSpacePressed: handleClick()
            Keys.onReturnPressed: handleClick()
            onClicked: root.runCodexReset(!root.codexResetReady
                    || root.codexResetStatus.armed ? "disarm" : "arm")

            Rectangle {
                anchors.fill: parent
                color: "transparent"
                radius: Theme.cornerRadius
                border.width: codexAutoResetToggle.activeFocus ? 2 : 0
                border.color: Theme.primary
            }
        }

        StyledText {
            width: parent.width
            text: "Uses one eligible earned reset at 99% general Codex usage or shortly before the reset expires, then turns itself off. DMS must be running. If no eligible reset is available, the one-shot stays off."
            textFormat: Text.PlainText
            wrapMode: Text.WordWrap
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
        }

        StyledText {
            width: parent.width
            text: root.codexResetDetailText()
            textFormat: Text.PlainText
            wrapMode: Text.WordWrap
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Medium
            color: root.codexResetStatus.error ? Theme.error
                    : root.codexResetStatus.armed === true ? Theme.warning : Theme.surfaceVariantText
        }
    }

    ToggleSetting {
        settingKey: "enableClaudePrime"
        label: "Enable Claude prime"
        description: "Automatically run a tiny Claude request when no active session timer is known"
        defaultValue: false
    }

    ToggleSetting {
        settingKey: "publicResetAnnouncements"
        label: "Public reset announcements (Alpha)"
        description: "Experimental third-party reports, advance alerts, and reset matching via TokenResets. May be incomplete or incorrect; do not rely on them to spend quota or redeem resets. The feed host sees your IP address and ordinary request metadata; no account data is sent. Checks every 15 minutes. Announcements are not guarantees for your account."
        defaultValue: false
    }
}
