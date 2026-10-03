import QtQuick
import Quickshell.Io
import qs.Common
import qs.Widgets
import qs.Modules.Plugins

PluginSettings {
    id: root

    pluginId: "dankAIUsage"

    readonly property var barLabelOptions: [
        { label: "None", value: "none" },
        { label: "Quota (5h / w / model initial)", value: "tag" },
        { label: "Reset time", value: "time" },
        { label: "Percent", value: "percent" }
    ]

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

    // A titled group of settings. PluginSettings reloads values only on its
    // direct children, so each group forwards loadValue to the controls it
    // contains (including nested groups). Groups deliberately never define
    // saveValue: the framework controls look for the first ancestor with both
    // saveValue and loadValue, and must keep resolving this page's root.
    // A collapsed or hidden group keeps its controls instantiated, so nothing
    // is reset or written when it is out of view. Expanded state is view-only.
    component SettingsGroup: Column {
        id: group

        property string title: ""
        property string summary: ""
        property bool nested: false
        property bool collapsible: false
        property bool expanded: true
        default property alias content: groupContent.data

        // Assigned as a property, not declared as a method, so the refresh
        // interval's loadValue() stays the first named one in this file.
        property var loadValue: function() { group.reloadContentValues() }

        function reloadContentValues() {
            var items = groupContent.children
            for (var i = 0; i < items.length; i++) {
                if (items[i] && items[i].loadValue) items[i].loadValue()
            }
        }

        width: parent.width
        spacing: Theme.spacingS

        Rectangle {
            width: parent.width
            height: 1
            visible: !group.nested
            color: Theme.outline
            opacity: 0.3
        }

        Item {
            id: groupHeader

            width: parent.width
            height: Math.max(group.collapsible ? 32 : 0, groupTitle.implicitHeight)
            visible: group.title !== ""
            activeFocusOnTab: group.collapsible
            Accessible.role: group.collapsible ? Accessible.Button : Accessible.StaticText
            Accessible.name: group.title + (group.collapsible ? (group.expanded ? "; expanded" : "; collapsed") : "")
            Accessible.onPressAction: if (group.collapsible) group.expanded = !group.expanded
            Keys.onSpacePressed: if (group.collapsible) group.expanded = !group.expanded
            Keys.onReturnPressed: if (group.collapsible) group.expanded = !group.expanded

            StyledRect {
                anchors.fill: parent
                radius: Theme.cornerRadius
                visible: group.collapsible
                color: groupHeaderMouse.containsMouse ? Theme.surfaceHover : "transparent"
                border.width: groupHeader.activeFocus ? 2 : 0
                border.color: Theme.primary
            }

            DankIcon {
                id: groupChevron

                name: "expand_more"
                size: 18
                width: group.collapsible ? 18 : 0
                visible: group.collapsible
                color: Theme.surfaceVariantText
                rotation: group.expanded ? 180 : 0
                anchors.left: parent.left
                anchors.leftMargin: group.collapsible ? Theme.spacingXS : 0
                anchors.verticalCenter: parent.verticalCenter

                Behavior on rotation {
                    NumberAnimation { duration: Theme.shortDuration; easing.type: Theme.standardEasing }
                }
            }

            StyledText {
                id: groupTitle

                anchors.left: groupChevron.right
                anchors.leftMargin: group.collapsible ? Theme.spacingXS : 0
                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                text: group.title
                font.pixelSize: group.nested ? Theme.fontSizeMedium : Theme.fontSizeLarge
                font.weight: group.nested ? Font.Medium : Font.Bold
                color: Theme.surfaceText
                elide: Text.ElideRight
            }

            MouseArea {
                id: groupHeaderMouse

                anchors.fill: parent
                enabled: group.collapsible
                hoverEnabled: group.collapsible
                cursorShape: Qt.PointingHandCursor
                onClicked: group.expanded = !group.expanded
            }
        }

        StyledText {
            width: parent.width
            visible: group.summary !== ""
            text: group.summary
            wrapMode: Text.WordWrap
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
        }

        Column {
            id: groupContent

            width: parent.width
            spacing: Theme.spacingM
            visible: !group.collapsible || group.expanded
        }
    }

    SettingsGroup {
        title: "Display"
        summary: "Codex limits come from its local app server. Claude uses its existing sign-in, with statusline data as a fallback. Left / Used and Simple / Advanced also switch from the dropdown."

        ToggleSetting {
            settingKey: "showCodex"
            label: "Show Codex"
            description: "Subscription limits and local token history from Codex"
            defaultValue: true
        }

        ToggleSetting {
            settingKey: "showClaude"
            label: "Show Claude"
            description: "Cached Claude Code subscription limits and local token history"
            defaultValue: true
        }

        ToggleSetting {
            settingKey: "showUsed"
            label: "Show used allowance"
            description: "Percentages and bar fill show what is used instead of what is left, in the bar and the dropdown"
            defaultValue: false
        }
    }

    SettingsGroup {
        title: "Top bar"
        summary: "Layout of the horizontal pill. The vertical pill always shows the most constrained quota."

        ToggleSetting {
            settingKey: "barShowProviderLogos"
            label: "Provider logos"
            description: "Show the OpenAI and Claude logos beside their quotas. When off, provider names are shown instead."
            defaultValue: true
        }

        ToggleSetting {
            settingKey: "brandLogoColors"
            label: "Brand-colored logos"
            description: "Claude in orange and OpenAI in white (black on light themes) instead of the theme accent, in the bar and the dropdown"
            defaultValue: false
        }

        ToggleSetting {
            settingKey: "barShowPluginIcon"
            label: "Plugin icon"
            description: "Show the generic monitoring icon at the start of the pill"
            defaultValue: false
        }

        ToggleSetting {
            id: barQuotaBarsToggle

            settingKey: "barQuotaBars"
            label: "Quota bars instead of text"
            description: "Small stacked bars per provider (5-hour on top, then weekly and model limits) instead of percentages. Credits stay in the dropdown and Compact pill does not apply while this is on. Options appear below."
            defaultValue: false
        }

        ToggleSetting {
            settingKey: "compactPill"
            label: "Compact pill"
            description: "Show only the most constrained selected quota for each visible provider"
            defaultValue: false
            visible: !barQuotaBarsToggle.value
        }

        SettingsGroup {
            title: "Quota bar options"
            nested: true
            visible: barQuotaBarsToggle.value

            SliderSetting {
                settingKey: "barQuotaBarWidth"
                label: "Bar width"
                description: "Width of each quota bar"
                minimum: 16
                maximum: 120
                defaultValue: 40
                unit: "px"
            }

            ToggleSetting {
                settingKey: "barUsageColors"
                label: "Color bars by usage"
                description: "Green through yellow and orange to dark red as usage approaches 100%, instead of the theme accent with warning and error colors when low"
                defaultValue: false
            }

            SelectionSetting {
                settingKey: "barLabelLeft"
                label: "Left label"
                description: "Text left of each bar. Reset time counts down to the reset (elapsed window time with Used); percent follows Left / Used."
                options: root.barLabelOptions
                defaultValue: "none"
            }

            SelectionSetting {
                settingKey: "barLabelRight"
                label: "Right label"
                description: "Text right of each bar"
                options: root.barLabelOptions
                defaultValue: "none"
            }

            ToggleSetting {
                settingKey: "barPaceMarker"
                label: "Pace marker"
                description: "Tick at the even-pace point for the time passed in each window. With Left, fill short of the tick means you are ahead of pace; with Used, fill beyond it."
                defaultValue: false
            }
        }
    }

    SettingsGroup {
        title: "Quotas in the top bar"
        summary: "Which limits each provider contributes to the pill. The dropdown always lists every quota."

        ToggleSetting {
            settingKey: "barShowClaudeSession"
            label: "Claude 5-hour session"
            description: "Include Claude's current five-hour window"
            defaultValue: true
        }

        ToggleSetting {
            settingKey: "barShowClaudeWeekly"
            label: "New Claude weekly limits by default"
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
                text: "Reported Claude weekly limits"
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
            description: "Include Claude's paid extra-usage credit balance in the text pill. Quota-bar mode keeps credits in the dropdown."
            defaultValue: false
        }

        ToggleSetting {
            settingKey: "barShowCodexCredits"
            label: "Codex credits"
            description: "Include Codex's prepaid credit balance in the text pill when reported. Quota-bar mode keeps credits in the dropdown."
            defaultValue: false
        }
    }

    SettingsGroup {
        title: "Collection"
        summary: "How often providers are contacted. Countdowns and redraws never contact a provider."

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

        ToggleSetting {
            settingKey: "refreshOnOpen"
            label: "Refresh when the dropdown opens"
            description: "Runs the same refresh as the Refresh button, at most every 30 seconds. Providers are still only contacted once the refresh interval has passed; until then cached quotas and local token history are reloaded."
            defaultValue: false
        }
    }

    SettingsGroup {
        title: "Automation and notifications"
        summary: "Everything here is off by default except desktop notifications. An armed Codex reset always shows its state here and in the dropdown."

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
                text: "Uses one eligible earned reset at 99% general Codex usage or shortly before the reset expires, then turns itself off. DMS must be running. If no eligible reset is available, the one-shot stays off. Also available from the Advanced dropdown when a reset is available."
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
            description: "Automatically run a tiny Claude request when no active session timer is known. This consumes usage."
            defaultValue: false
        }

        ToggleSetting {
            settingKey: "publicResetAnnouncements"
            label: "Public reset announcements (Alpha)"
            description: "Experimental third-party reports, advance alerts, and reset matching via TokenResets. May be incomplete or incorrect; do not rely on them to spend quota or redeem resets. The feed host sees your IP address and ordinary request metadata; no account data is sent. Checks every 15 minutes. Announcements are not guarantees for your account."
            defaultValue: false
        }

        ToggleSetting {
            settingKey: "systemNotifications"
            label: "Desktop notifications"
            description: "Send a desktop notification for new reset history changes and, when enabled, new public reset announcements. Items already viewed in the dropdown are not announced."
            defaultValue: true
        }
    }

    SettingsGroup {
        title: "Token history and compatibility"
        summary: "Cached-token totals and the legacy history period. Choose 5h, 7d, 30d, 90d, or Tracked in the dropdown."
        collapsible: true
        expanded: false

        ToggleSetting {
            settingKey: "includeCachedTokens"
            label: "Include cached tokens"
            description: "Include cached tokens in combined totals. The Input / Cached / Output breakdown always shows cached tokens separately."
            defaultValue: false
        }

        SliderSetting {
            settingKey: "periodDays"
            label: "Legacy token history (days)"
            description: "Kept for compatibility with older cached summaries. The configured range stays available in the dropdown's range selector when it differs from the presets."
            minimum: 1
            maximum: 90
            defaultValue: 7
        }
    }
}
