import QtQuick
import Quickshell
import Quickshell.Io
import qs.Common
import qs.Widgets
import qs.Modules.Plugins

PluginComponent {
    id: root

    pluginId: "dankAIUsage"

    property int refreshInterval: 300
    property int periodDays: 7
    property bool showCodex: true
    property bool showClaude: true
    property bool includeCachedTokens: true
    property bool compactPill: false
    property bool focusWeekly: false

    property bool isLoading: true
    property bool hasError: false
    property string errorText: ""
    property string lastUpdated: ""
    property var providers: []
    property var grandTotal: ({ total: 0, input: 0, output: 0, cached: 0, requests: 0, sessions: 0 })
    property var capabilities: ({})
    property string _pendingOutput: ""

    function loadSettings() {
        if (!pluginService || !pluginService.loadPluginData) return
        refreshInterval = pluginService.loadPluginData(pluginId, "refreshInterval", 300) || 300
        periodDays = pluginService.loadPluginData(pluginId, "periodDays", 7) || 7
        showCodex = pluginService.loadPluginData(pluginId, "showCodex", true) !== false
        showClaude = pluginService.loadPluginData(pluginId, "showClaude", true) !== false
        includeCachedTokens = pluginService.loadPluginData(pluginId, "includeCachedTokens", true) !== false
        compactPill = pluginService.loadPluginData(pluginId, "compactPill", false) === true
        focusWeekly = pluginService.loadPluginData(pluginId, "focusWeekly", false) === true
    }

    function loadCache() {
        if (!pluginService || !pluginService.loadPluginState) return
        var cached = pluginService.loadPluginState(pluginId, "lastSummary", null)
        if (cached && cached.providers) applySummary(cached)
    }

    Component.onCompleted: {
        loadSettings()
        loadCache()
        refreshUsage()
    }

    Timer {
        interval: 5000
        running: true
        repeat: true
        onTriggered: root.loadSettings()
    }

    Timer {
        interval: root.refreshInterval * 1000
        running: true
        repeat: true
        onTriggered: root.refreshUsage()
    }

    function refreshUsage() {
        if (usageProcess.running) return
        _pendingOutput = ""
        usageProcess.command = [
            "dankaiusage", "summary",
            "--period-days", "" + root.periodDays
        ]
        usageProcess.running = true
    }

    Process {
        id: usageProcess
        running: false
        stdout: SplitParser {
            onRead: data => { root._pendingOutput += data + "\n" }
        }
        stderr: SplitParser {
            onRead: data => { root.errorText = data }
        }
        onExited: (exitCode, exitStatus) => {
            if (exitCode !== 0) {
                root.hasError = true
                root.errorText = root.errorText || "dankaiusage exited with " + exitCode
                root.isLoading = false
                return
            }
            try {
                var summary = JSON.parse(root._pendingOutput.trim())
                root.applySummary(summary)
                if (root.pluginService && root.pluginService.savePluginState)
                    root.pluginService.savePluginState(root.pluginId, "lastSummary", summary)
            } catch (e) {
                root.hasError = true
                root.errorText = "Could not parse usage data"
            }
            root.isLoading = false
        }
    }

    function applySummary(summary) {
        capabilities = summary.capabilities || {}
        providers = summary.providers || []
        grandTotal = summary.grandTotal || ({ total: 0, input: 0, output: 0, cached: 0, requests: 0, sessions: 0 })
        hasError = (summary.errors || []).length > 0
        errorText = hasError ? summary.errors.join("\n") : ""

        var d = new Date(summary.generatedAt || Date.now())
        lastUpdated = ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2)
    }

    function visibleProviders() {
        var out = []
        for (var i = 0; i < providers.length; i++) {
            if (providers[i].id === "codex" && !showCodex) continue
            if (providers[i].id === "claude" && !showClaude) continue
            out.push(providers[i])
        }
        return out
    }

    function displayTotal(totals) {
        if (!totals) return 0
        if (includeCachedTokens) return totals.total || 0
        return (totals.total || 0) - (totals.cached || 0)
    }

    function filteredGrandTotal() {
        var total = 0
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) total += displayTotal(list[i].period)
        return total
    }

    function knownAllowance(allowance) {
        return allowance && allowance.known
    }

    function allowanceLabel(allowance) {
        if (!knownAllowance(allowance)) return "--"
        return Math.round(allowance.percentRemaining || 0) + "%"
    }

    function allowanceDetail(allowance) {
        if (!knownAllowance(allowance)) return "Limit unavailable"
        if (allowance.unit === "percent") return Math.round(allowance.percentRemaining || 0) + "% left of subscription window"
        return formatTokens(allowance.remaining || 0) + " left of " + formatTokens(allowance.limit || 0)
    }

    function allowanceColor(allowance) {
        if (!knownAllowance(allowance)) return Theme.surfaceVariantText
        var pct = allowance.percentRemaining || 0
        if (pct <= 10) return "#ff6b6b"
        if (pct <= 25) return "#ffaa00"
        return Theme.primary
    }

    function selectedAllowanceWindow() {
        return focusWeekly ? "weekly" : "session"
    }

    function setFocusWindow(window) {
        focusWeekly = window === "weekly"
        if (pluginService && pluginService.savePluginData)
            pluginService.savePluginData(pluginId, "focusWeekly", focusWeekly)
    }

    function allowanceFor(provider, window) {
        if (!provider) return null
        return window === "weekly" ? provider.weeklyLeft : provider.sessionLeft
    }

    function windowLabel(window) {
        return window === "weekly" ? "Weekly" : "Session"
    }

    function weakestAllowance(window) {
        var weakest = null
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) {
            var a = allowanceFor(list[i], window)
            if (!knownAllowance(a)) continue
            if (!weakest || (a.percentRemaining || 0) < (weakest.percentRemaining || 0)) weakest = a
        }
        return weakest
    }

    function formatReset(allowance) {
        if (!allowance || !allowance.resetAt) return "--"
        var d = new Date(allowance.resetAt)
        if (isNaN(d.getTime())) return "--"
        return ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2)
    }

    function formatTokens(value) {
        value = Math.max(0, value || 0)
        if (value >= 1000000000) return (value / 1000000000).toFixed(1) + "B"
        if (value >= 1000000) return (value / 1000000).toFixed(1) + "M"
        if (value >= 1000) return (value / 1000).toFixed(1) + "K"
        return "" + value
    }

    function providerIcon(id) {
        return id === "codex" ? "terminal" : "psychology"
    }

    function providerColor(provider) {
        if (!provider.available || provider.error) return "#ff6b6b"
        return provider.id === "codex" ? Theme.primary : "#8bc34a"
    }

    function pillLabel() {
        if (isLoading && providers.length === 0) return "..."
        var focus = selectedAllowanceWindow()
        var weakest = weakestAllowance(focus)
        if (compactPill) return (focus === "weekly" ? "W " : "S ") + (weakest ? allowanceLabel(weakest) : "--")

        var list = visibleProviders()
        var parts = []
        for (var i = 0; i < list.length; i++) {
            parts.push((list[i].id === "codex" ? "Cx " : "Cl ") + allowanceLabel(list[i].sessionLeft) + "/" + allowanceLabel(list[i].weeklyLeft))
        }
        return parts.length > 0 ? parts.join("  ") : "--"
    }

    horizontalBarPill: Component {
        Row {
            spacing: Theme.spacingS

            DankIcon {
                name: "monitoring"
                size: Theme.fontSizeLarge
                color: root.hasError ? "#ff6b6b" : Theme.primary
                anchors.verticalCenter: parent.verticalCenter
            }

            StyledText {
                text: root.pillLabel()
                font.pixelSize: Theme.fontSizeMedium
                color: root.hasError ? "#ff6b6b" : Theme.surfaceText
                anchors.verticalCenter: parent.verticalCenter
                elide: Text.ElideRight
                maximumLineCount: 1
            }
        }
    }

    verticalBarPill: Component {
        Column {
            spacing: 1

            DankIcon {
                name: "monitoring"
                size: Theme.fontSizeLarge
                color: root.hasError ? "#ff6b6b" : Theme.primary
                anchors.horizontalCenter: parent.horizontalCenter
            }

            StyledText {
                text: root.formatTokens(root.filteredGrandTotal())
                font.pixelSize: Theme.fontSizeSmall
                color: root.hasError ? "#ff6b6b" : Theme.surfaceText
                anchors.horizontalCenter: parent.horizontalCenter
            }
        }
    }

    popoutContent: Component {
        Column {
            spacing: Theme.spacingL

            Item {
                width: parent.width
                height: headerTitle.implicitHeight + headerSubtitle.implicitHeight + 2

                Column {
                    anchors.left: parent.left
                    anchors.right: refreshButton.left
                    anchors.rightMargin: Theme.spacingS
                    anchors.verticalCenter: parent.verticalCenter
                    spacing: 2

                    StyledText {
                        id: headerTitle
                        width: parent.width
                        text: "AI Usage"
                        font.pixelSize: Theme.fontSizeLarge
                        font.weight: Font.Bold
                        color: Theme.surfaceText
                        elide: Text.ElideRight
                        maximumLineCount: 1
                    }

                    StyledText {
                        id: headerSubtitle
                        width: parent.width
                        text: root.windowLabel(root.selectedAllowanceWindow()) + " focus" + (root.lastUpdated ? " - " + root.lastUpdated : "")
                        font.pixelSize: Theme.fontSizeSmall
                        color: Theme.surfaceVariantText
                        elide: Text.ElideRight
                        maximumLineCount: 1
                    }
                }

                DankActionButton {
                    id: refreshButton
                    buttonSize: 28
                    iconName: "refresh"
                    iconColor: Theme.surfaceVariantText
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    onClicked: root.refreshUsage()
                }
            }

            StyledRect {
                width: parent.width
                height: 92
                radius: Theme.cornerRadius
                color: Theme.surfaceContainerHigh

                Row {
                    anchors.fill: parent
                    anchors.margins: Theme.spacingM
                    spacing: Theme.spacingM

                    LimitBucket {
                        width: (parent.width - Theme.spacingM) / 2
                        title: "Session"
                        allowance: root.weakestAllowance("session")
                        selected: root.selectedAllowanceWindow() === "session"
                    }

                    LimitBucket {
                        width: (parent.width - Theme.spacingM) / 2
                        title: "Weekly"
                        allowance: root.weakestAllowance("weekly")
                        selected: root.selectedAllowanceWindow() === "weekly"
                    }
                }
            }

            Item {
                width: parent.width
                height: tokensValue.implicitHeight

                StyledText {
                    text: "Token history"
                    font.pixelSize: Theme.fontSizeSmall
                    color: Theme.surfaceVariantText
                    anchors.left: parent.left
                    anchors.right: tokensValue.left
                    anchors.rightMargin: Theme.spacingS
                    anchors.verticalCenter: parent.verticalCenter
                    elide: Text.ElideRight
                    maximumLineCount: 1
                }

                StyledText {
                    id: tokensValue
                    text: root.formatTokens(root.filteredGrandTotal()) + " tokens"
                    font.pixelSize: Theme.fontSizeSmall
                    color: Theme.surfaceVariantText
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    elide: Text.ElideRight
                    maximumLineCount: 1
                }
            }

            StyledText {
                text: root.errorText
                width: parent.width
                color: "#ff6b6b"
                font.pixelSize: Theme.fontSizeSmall
                wrapMode: Text.WordWrap
                visible: root.hasError && root.errorText !== ""
            }

            Column {
                width: parent.width
                spacing: Theme.spacingS
                visible: root.visibleProviders().length > 0

                Repeater {
                    model: root.visibleProviders()

                    StyledRect {
                        width: parent.width
                        height: 144
                        radius: Theme.cornerRadius
                        color: Theme.surfaceContainerHigh

                        Column {
                            anchors.fill: parent
                            anchors.margins: Theme.spacingS
                            spacing: Theme.spacingS

                            Item {
                                width: parent.width
                                height: 26

                                Row {
                                    anchors.left: parent.left
                                    anchors.right: providerLimits.left
                                    anchors.rightMargin: Theme.spacingS
                                    anchors.verticalCenter: parent.verticalCenter
                                    spacing: Theme.spacingS

                                    DankIcon {
                                        name: root.providerIcon(modelData.id)
                                        size: Theme.fontSizeMedium
                                        color: root.providerColor(modelData)
                                        anchors.verticalCenter: parent.verticalCenter
                                    }

                                    StyledText {
                                        text: modelData.name
                                        width: parent.width - Theme.fontSizeMedium - Theme.spacingS
                                        font.pixelSize: Theme.fontSizeMedium
                                        font.weight: Font.Medium
                                        color: Theme.surfaceText
                                        anchors.verticalCenter: parent.verticalCenter
                                        elide: Text.ElideRight
                                        maximumLineCount: 1
                                    }
                                }

                                StyledText {
                                    id: providerLimits
                                    text: root.allowanceLabel(modelData.sessionLeft) + " / " + root.allowanceLabel(modelData.weeklyLeft)
                                    font.pixelSize: Theme.fontSizeMedium
                                    font.weight: Font.Bold
                                    color: root.providerColor(modelData)
                                    anchors.right: parent.right
                                    anchors.verticalCenter: parent.verticalCenter
                                    elide: Text.ElideRight
                                    maximumLineCount: 1
                                }
                            }

                            Row {
                                width: parent.width
                                spacing: Theme.spacingS

                                UsageMetric {
                                    width: (parent.width - Theme.spacingS * 2) / 3
                                    label: "Session"
                                    value: root.allowanceLabel(modelData.sessionLeft)
                                    detail: root.allowanceDetail(modelData.sessionLeft)
                                    valueColor: root.allowanceColor(modelData.sessionLeft)
                                }

                                UsageMetric {
                                    width: (parent.width - Theme.spacingS * 2) / 3
                                    label: "Week"
                                    value: root.allowanceLabel(modelData.weeklyLeft)
                                    detail: root.allowanceDetail(modelData.weeklyLeft)
                                    valueColor: root.allowanceColor(modelData.weeklyLeft)
                                }

                                UsageMetric {
                                    width: (parent.width - Theme.spacingS * 2) / 3
                                    label: "Tokens"
                                    value: root.formatTokens(root.displayTotal(modelData.period))
                                    detail: (modelData.period.requests || 0) + " requests"
                                    valueColor: Theme.surfaceText
                                }
                            }

                            StyledText {
                                width: parent.width
                                text: "Resets: session " + root.formatReset(modelData.sessionLeft) + ", week " + root.formatReset(modelData.weeklyLeft)
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                                elide: Text.ElideRight
                            }
                        }
                    }
                }
            }

            Column {
                width: parent.width
                spacing: Theme.spacingS
                visible: root.visibleProviders().length === 0 && !root.isLoading

                StyledText {
                    text: "No providers enabled."
                    color: Theme.surfaceVariantText
                    font.pixelSize: Theme.fontSizeMedium
                }
            }

            StyledText {
                text: "Loading..."
                color: Theme.surfaceVariantText
                font.pixelSize: Theme.fontSizeMedium
                visible: root.isLoading
            }
        }
    }

    component UsageMetric: Column {
        property string label: ""
        property string value: ""
        property string detail: ""
        property var valueColor: Theme.surfaceText
        spacing: 2

        StyledText {
            text: parent.value
            font.pixelSize: Theme.fontSizeMedium
            font.weight: Font.Medium
            color: parent.valueColor
            width: parent.width
            elide: Text.ElideRight
            maximumLineCount: 1
        }

        StyledText {
            text: parent.label
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
            width: parent.width
            elide: Text.ElideRight
            maximumLineCount: 1
        }

        StyledText {
            text: parent.detail
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
            elide: Text.ElideRight
            width: parent.width
            maximumLineCount: 1
        }
    }

    component LimitBucket: Item {
        property string title: ""
        property var allowance: null
        property bool selected: false

        height: parent ? parent.height : 64

        Column {
            anchors.fill: parent
            spacing: 2

            Row {
                width: parent.width
                spacing: Theme.spacingXS

                DankIcon {
                    name: selected ? "filter_alt" : "hourglass_top"
                    size: Theme.fontSizeSmall
                    color: root.allowanceColor(allowance)
                    anchors.verticalCenter: parent.verticalCenter
                }

                StyledText {
                    width: parent.width - Theme.fontSizeSmall - Theme.spacingXS
                    text: title
                    font.pixelSize: Theme.fontSizeSmall
                    font.weight: selected ? Font.Bold : Font.Medium
                    color: selected ? Theme.surfaceText : Theme.surfaceVariantText
                    anchors.verticalCenter: parent.verticalCenter
                    elide: Text.ElideRight
                    maximumLineCount: 1
                }
            }

            StyledText {
                width: parent.width
                text: root.allowanceLabel(allowance)
                font.pixelSize: Theme.fontSizeLarge
                font.weight: Font.Bold
                color: root.allowanceColor(allowance)
                elide: Text.ElideRight
                maximumLineCount: 1
            }

            StyledText {
                width: parent.width
                text: root.formatReset(allowance)
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
                elide: Text.ElideRight
                maximumLineCount: 1
            }
        }

        MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: root.setFocusWindow(title === "Weekly" ? "weekly" : "session")
        }
    }

    popoutWidth: 420
    popoutHeight: 480
}
