import QtQuick
import QtQuick.Controls as Controls
import QtQuick.Shapes
import Quickshell
import Quickshell.Io
import qs.Common
import qs.Widgets
import qs.Services
import qs.Modules.Plugins

PluginComponent {
    id: root

    pluginId: "dankAIUsage"

    property int refreshInterval: 300
    property double resetClock: Date.now()

    Timer {
        interval: 60000
        running: true
        repeat: true
        onTriggered: root.resetClock = Date.now()
    }
    property int periodDays: 7
    property bool showCodex: true
    property bool showClaude: true
    property bool barShowPluginIcon: false
    property bool barShowProviderLogos: true
    property bool barShowClaudeSession: true
    property bool barShowClaudeWeekly: true
    property var barClaudeWeeklyOverrides: ({})
    property bool barShowClaudeCredits: false
    property bool includeCachedTokens: false
    property bool compactPill: false
    property bool showUsed: false
    property bool quickControlsOpen: false
    property bool historyOpen: false
    property bool announcementsOpen: false
    property int popoutMaxHeight: 720
    property bool publicResetAnnouncements: false
    property var publicAnnouncements: []
    property bool announcementsStale: true
    property string announcementsMessage: ""
    property string _announcementOutput: ""
    property bool _announcementInvalid: false
    property var announcementReceipts: ({})
    property double announcementClock: Date.now()
    property bool diagnosticsOpen: false
    property string diagnosticReport: ""
    property string diagnosticStatus: ""
    property string _diagnosticOutput: ""
    property bool _diagnosticOverflow: false
    property bool _diagnosticTimedOut: false
    property string dropdownMode: "simple"
    readonly property bool advancedDropdown: dropdownMode === "advanced"
    // tokenHistorySession is read once in loadCache() to migrate the former
    // two-state selector. New versions persist an explicit range key.
    property string tokenHistoryRange: "7d"
    readonly property var tokenHistoryRanges: [
        { key: "5h", label: "5h" },
        { key: "7d", label: "7d" },
        { key: "30d", label: "30d" },
        { key: "90d", label: "90d" },
        { key: "tracked", label: "Tracked" }
    ]
    property var trackingStatus: ({ known: false, enabled: false, providers: {}, errors: [] })
    property string _trackingOutput: ""
    property string trackingCommandError: ""
    property bool clearTrackingConfirm: false
    property bool enableClaudePrime: false

    property bool isLoading: true
    property bool hasError: false
    property string errorText: ""
    property string lastUpdated: ""
    property var providers: []
    property var usageHistory: []
    property string historyError: ""
    property var historyExplanationGroup: null
    property string historyExplanationLocation: ""
    property bool historyExplanationExpanded: false
    property string historyExplanationReason: ""
    property string historyExplanationNote: ""
    property string historyExplanationError: ""
    property string _historyExplanationOutput: ""
    property string _historyExplanationStderr: ""
    property string _historyExplanationPayload: ""
    property bool _historyExplanationTimedOut: false
    property var grandTotal: ({ total: 0, input: 0, output: 0, cached: 0, requests: 0, sessions: 0 })
    property var capabilities: ({})
    property string _pendingOutput: ""
    property bool _usageRefreshPending: false
    property string _claudePrimeOutput: ""
    property string _claudePrimeError: ""
    property bool isPrimingClaude: false
    property string claudePrimeText: ""
    property bool claudePrimeAutomatic: false
    property bool lastClaudeAutoPrimeFailed: false
    property double lastClaudeAutoPrimeAt: 0
    property var codexResetStatus: ({ armed: false, stateKnown: false, message: "Checking reset control..." })
    property bool codexResetReady: false
    property string _codexResetOutput: ""
    property string _codexResetAction: ""
    property bool _codexResetWasArmed: false
    property bool _refreshAfterCodexReset: false
    property bool _refreshCyclePending: false

    function normalizedRefreshInterval(value) {
        var seconds = Number(value)
        if (!isFinite(seconds) || seconds <= 0) seconds = 300
        return Math.max(180, Math.min(3600, Math.round(seconds / 60) * 60))
    }

    function loadSettings() {
        if (!pluginService || !pluginService.loadPluginData) return
        var wasEnabled = enableClaudePrime
        var announcementsWereEnabled = publicResetAnnouncements
        publicResetAnnouncements = pluginService.loadPluginData(pluginId, "publicResetAnnouncements", false) === true
        if (publicResetAnnouncements && !announcementsWereEnabled) {
            var savedReceipts = pluginService.loadPluginState(pluginId, "announcementReceipts", {})
            announcementReceipts = savedReceipts && typeof savedReceipts === "object" && !Array.isArray(savedReceipts) ? savedReceipts : {}
            Qt.callLater(root.refreshAnnouncements)
        } else if (!publicResetAnnouncements) {
            announcementProcess.running = false
            publicAnnouncements = []
            announcementsStale = true
        }
        var savedRefreshInterval = pluginService.loadPluginData(pluginId, "refreshInterval", 300)
        refreshInterval = normalizedRefreshInterval(savedRefreshInterval)
        if (refreshInterval !== Number(savedRefreshInterval) && pluginService.savePluginData)
            pluginService.savePluginData(pluginId, "refreshInterval", refreshInterval)
        periodDays = pluginService.loadPluginData(pluginId, "periodDays", 7) || 7
        showCodex = pluginService.loadPluginData(pluginId, "showCodex", true) !== false
        showClaude = pluginService.loadPluginData(pluginId, "showClaude", true) !== false
        barShowPluginIcon = pluginService.loadPluginData(pluginId, "barShowPluginIcon", false) === true
        barShowProviderLogos = pluginService.loadPluginData(pluginId, "barShowProviderLogos", true) !== false
        barShowClaudeSession = pluginService.loadPluginData(pluginId, "barShowClaudeSession", true) !== false
        barShowClaudeWeekly = pluginService.loadPluginData(pluginId, "barShowClaudeWeekly", true) !== false
        var weeklyOverrides = pluginService.loadPluginData(pluginId, "barClaudeWeeklyOverrides", {})
        barClaudeWeeklyOverrides = weeklyOverrides && typeof weeklyOverrides === "object" && !Array.isArray(weeklyOverrides) ? weeklyOverrides : {}
        barShowClaudeCredits = pluginService.loadPluginData(pluginId, "barShowClaudeCredits", false) === true
        includeCachedTokens = pluginService.loadPluginData(pluginId, "includeCachedTokens", false) === true
        compactPill = pluginService.loadPluginData(pluginId, "compactPill", false) === true
        showUsed = pluginService.loadPluginData(pluginId, "showUsed", false) === true
        enableClaudePrime = pluginService.loadPluginData(pluginId, "enableClaudePrime", false) === true
        if (!wasEnabled && enableClaudePrime) {
            lastClaudeAutoPrimeFailed = false
            if (pluginService && pluginService.savePluginState)
                pluginService.savePluginState(pluginId, "lastClaudeAutoPrimeFailed", false)
            maybeAutoPrimeClaude()
        }
    }

    function loadCache() {
        if (!pluginService || !pluginService.loadPluginState) return
        var cached = pluginService.loadPluginState(pluginId, "lastSummary", null)
        dropdownMode = resolveDropdownMode(pluginService.loadPluginState(pluginId, "dropdownMode", ""), cached)
        // Persist before the first summary so a new installation stays Simple.
        if (pluginService.savePluginState)
            pluginService.savePluginState(pluginId, "dropdownMode", dropdownMode)
        var savedRange = pluginService.loadPluginState(pluginId, "tokenHistoryRange", "") || ""
        if (isTokenHistoryRange(savedRange)) {
            tokenHistoryRange = savedRange
        } else if (pluginService.loadPluginState(pluginId, "tokenHistorySession", false) === true) {
            tokenHistoryRange = "5h"
        } else {
            tokenHistoryRange = migratedPeriodRange(periodDays)
        }
        lastClaudeAutoPrimeAt = pluginService.loadPluginState(pluginId, "lastClaudeAutoPrimeAt", 0) || 0
        lastClaudeAutoPrimeFailed = pluginService.loadPluginState(pluginId, "lastClaudeAutoPrimeFailed", false) === true
        if (cached && cached.providers) applySummary(cached, false)
    }

    function resolveDropdownMode(savedMode, cachedSummary) {
        if (savedMode === "simple" || savedMode === "advanced") return savedMode
        return cachedSummary && cachedSummary.providers ? "advanced" : "simple"
    }

    function setDropdownMode(mode) {
        if (mode !== "simple" && mode !== "advanced") return
        dropdownMode = mode
        clearTrackingConfirm = false
        if (pluginService && pluginService.savePluginState)
            pluginService.savePluginState(pluginId, "dropdownMode", mode)
    }

    function resetControlsVisible() {
        return advancedDropdown || codexResetStatus.armed === true
                || codexResetStatus.stateKnown === false || !!codexResetStatus.error
                || codexResetStatus.state === "attempted"
    }

    Component.onCompleted: {
        loadSettings()
        loadCache()
        refreshCycle()
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
        onTriggered: root.refreshCycle()
    }

    // A due armed reset must check before summary refreshes the shared provider
    // cache. Otherwise every reset check could arrive inside the cooldown and
    // never receive the fresh data required to authorize consumption.
    function refreshCycle() {
        if (codexResetProcess.running) {
            _refreshCyclePending = true
            return
        }
        if (codexResetStatus.stateKnown !== true) {
            _refreshAfterCodexReset = true
            runCodexReset("status")
            return
        }
        if (showCodex && codexResetStatus.armed === true) {
            _refreshAfterCodexReset = true
            runCodexReset("check")
            return
        }
        refreshUsage()
        // Hiding Codex pauses automatic consumption, but still updates status.
        runCodexReset("status")
    }

    function runCodexReset(action) {
        if (codexResetProcess.running) return
        _codexResetOutput = ""
        _codexResetAction = action
        _codexResetWasArmed = codexResetStatus.armed === true
        codexResetProcess.command = [
            "dankaiusage", "codex-reset", action,
            "--refresh-interval", "" + root.refreshInterval
        ]
        codexResetProcess.running = true
    }

    Process {
        id: codexResetProcess
        running: false
        stdout: SplitParser {
            onRead: data => { root._codexResetOutput += data + "\n" }
        }
        onExited: (exitCode, exitStatus) => {
            try {
                var status = JSON.parse(root._codexResetOutput.trim())
                if (typeof status.armed !== "boolean") throw new Error("Invalid reset status")
                if (status.stateKnown === false)
                    status.armed = root.codexResetStatus.armed
                root.codexResetStatus = status
                root.codexResetReady = status.stateKnown === true
            } catch (e) {
                root.codexResetReady = false
                // Do not guess the armed state after a failed status request.
                root.codexResetStatus = {
                    armed: root.codexResetStatus.armed,
                    stateKnown: false,
                    message: "Reset status unavailable. Check the helper version and retry."
                }
            }
            var continueWithResetCheck = root._refreshAfterCodexReset
                    && root._codexResetAction === "status" && root.codexResetReady
                    && root.showCodex && root.codexResetStatus.armed === true
            if (continueWithResetCheck) {
                Qt.callLater(function() { root.runCodexReset("check") })
            } else if (root._refreshCyclePending) {
                root._refreshAfterCodexReset = false
                root._usageRefreshPending = false
                root._refreshCyclePending = false
                Qt.callLater(root.refreshCycle)
            } else if (root._refreshAfterCodexReset || root._usageRefreshPending) {
                root._refreshAfterCodexReset = false
                root._usageRefreshPending = false
                Qt.callLater(root.refreshUsage)
            }
        }
    }

    function refreshUsage() {
        if (codexResetProcess.running) {
            _usageRefreshPending = true
            return
        }
        if (historyExplanationProcess.running) {
            _usageRefreshPending = true
            return
        }
        if (trackingProcess.running) {
            _usageRefreshPending = true
            return
        }
        if (usageProcess.running) {
            _usageRefreshPending = true
            return
        }
        _pendingOutput = ""
        usageProcess.command = [
            "dankaiusage", "summary",
            "--period-days", "" + root.periodDays,
            "--refresh-interval", "" + root.refreshInterval
        ]
        usageProcess.running = true
    }

    function primeClaude(automatic) {
        automatic = automatic === true
        if (!enableClaudePrime) {
            if (!automatic) claudePrimeText = "Enable Claude prime in settings first"
            return
        }
        if (claudePrimeProcess.running) return
        _claudePrimeOutput = ""
        _claudePrimeError = ""
        claudePrimeText = automatic ? "Starting Claude session timer..." : "Refreshing Claude account limits..."
        isPrimingClaude = true
        claudePrimeAutomatic = automatic
        if (!automatic) lastClaudeAutoPrimeFailed = false
        claudePrimeProcess.command = [
            "dankaiusage", "claude-prime",
            "--refresh-interval", "" + root.refreshInterval
        ]
        claudePrimeProcess.running = true
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
            if (root._usageRefreshPending) {
                root._usageRefreshPending = false
                Qt.callLater(root.refreshUsage)
            }
            if (exitCode !== 0) {
                root.recordHelperFailure()
                root.hasError = true
                root.errorText = root.errorText || "dankaiusage exited with " + exitCode
                root.isLoading = false
                return
            }
            try {
                var summary = JSON.parse(root._pendingOutput.trim())
                root.applySummary(summary, true)
                if (root.pluginService && root.pluginService.savePluginState) {
                    // Keep the bounded helper history in one store, not in the DMS cache too.
                    var cachedSummary = Object.assign({}, summary)
                    delete cachedSummary.history
                    delete cachedSummary.historyError
                    // The helper owns tracked totals and control state.
                    delete cachedSummary.tracking
                    root.pluginService.savePluginState(root.pluginId, "lastSummary", cachedSummary)
                }
            } catch (e) {
                root.recordHelperFailure()
                root.hasError = true
                root.errorText = "Could not parse usage data"
            }
            root.isLoading = false
        }
    }

    function recordHelperFailure() {
        // Fixed category only: never forward stderr, exception text or payloads.
        if (!diagnosticFailureProcess.running) diagnosticFailureProcess.running = true
    }

    function refreshAnnouncements() {
        if (!publicResetAnnouncements || announcementProcess.running) return
        _announcementOutput = ""
        _announcementInvalid = false
        announcementProcess.running = true
    }

    Timer {
        interval: 900000
        running: root.publicResetAnnouncements
        repeat: true
        onTriggered: root.refreshAnnouncements()
    }

    Timer {
        interval: 60000
        running: root.publicResetAnnouncements
        repeat: true
        onTriggered: root.announcementClock = Date.now()
    }

    Process {
        id: announcementProcess
        command: ["dankaiusage", "announcements", "--enabled"]
        stdout: SplitParser {
            onRead: data => {
                if (root._announcementOutput.length + data.length > 524288) {
                    root._announcementInvalid = true
                    return
                }
                root._announcementOutput += data + "\n"
            }
        }
        onExited: (exitCode, exitStatus) => {
            if (!root.publicResetAnnouncements) return
            root.announcementsStale = true
            root.announcementsMessage = "Public announcements unavailable; quota monitoring is unaffected."
            if (exitCode !== 0 || root._announcementInvalid) return
            try {
                var response = JSON.parse(root._announcementOutput)
                if (!Array.isArray(response.events)) return
                root.publicAnnouncements = response.events
                root.announcementsStale = response.available !== true || response.stale !== false
                root.announcementsMessage = root.announcementsStale
                        ? "Public feed is stale or unavailable. No announcement alerts or matching until it recovers."
                        : "Public reports via TokenResets; account eligibility is not verified."
                root.announcementClock = Date.now()
                root.notifyUpcomingAnnouncements()
            } catch (e) { /* No remote errors enter quota status or diagnostics. */ }
        }
    }

    Timer {
        interval: 12000
        running: announcementProcess.running
        onTriggered: {
            root._announcementInvalid = true
            announcementProcess.running = false
            root.announcementsStale = true
            root.announcementsMessage = "Public feed timed out; quota monitoring is unaffected."
        }
    }

    function announcementUpcoming(event, now) {
        if (typeof event.expectedBy !== "string" || !/T.*(Z|[+-][0-9]{2}:[0-9]{2})$/.test(event.expectedBy)) return false
        var deadline = Date.parse(event.expectedBy || "")
        var announced = Date.parse(event.announcedAt || "")
        return event.kind === "hard_reset" && event.confidence === "verified"
                && isFinite(deadline) && deadline > now && deadline - now <= 48 * 3600000
                && isFinite(announced) && announced <= now && now - announced <= 24 * 3600000
                && !event.effectiveAt && (!event.expiresAt || Date.parse(event.expiresAt) > now)
    }

    function visibleAnnouncements() {
        if (!publicResetAnnouncements) return []
        return publicAnnouncements.filter(function(event) {
            if (!root.historyProviderVisible(event)) return false
            if (event.expiresAt && Date.parse(event.expiresAt) <= root.announcementClock) return false
            return root.advancedDropdown || (!root.announcementsStale && root.announcementUpcoming(event, root.announcementClock))
        }).slice(0, 4)
    }

    function announcementSummary(event) {
        var text = event.summary || ""
        return text.length > 240 ? text.slice(0, 240) + "…" : text
    }

    function notifyUpcomingAnnouncements() {
        if (!publicResetAnnouncements || announcementsStale || !pluginService || !pluginService.savePluginState) return
        var now = Date.now()
        var receipts = Object.assign({}, announcementReceipts)
        var keys = Object.keys(receipts)
        for (var k = 0; k < keys.length; k++) {
            if (typeof receipts[keys[k]] !== "number" || now - receipts[keys[k]] > 7 * 86400000) delete receipts[keys[k]]
        }
        for (var i = 0; i < publicAnnouncements.length; i++) {
            var event = publicAnnouncements[i]
            if (!historyProviderVisible(event) || !announcementUpcoming(event, now)) continue
            var key = event.id + ":" + event.revision
            if (receipts[key]) continue
            if (Object.keys(receipts).length >= 100) break
            receipts[key] = now
            announcementReceipts = receipts
            pluginService.savePluginState(pluginId, "announcementReceipts", receipts)
            ToastService.showInfo("Public reset announcement",
                    (event.provider === "codex" ? "Codex" : "Claude") + " reset announced by "
                    + formatShortDateTime(event.expectedBy) + ". Reported via TokenResets; check eligibility in the dropdown.")
        }
        announcementReceipts = receipts
    }

    function matchingPublicAnnouncement(group) {
        if (!publicResetAnnouncements || announcementsStale || !group) return null
        var match = null
        for (var i = 0; i < group.events.length; i++) {
            var local = group.events[i]
            if (!historyProviderVisible(local)) continue
            if (local.kind !== "allowance_increased_unknown" || !historyEventNeedsNoPrompt(local)) continue
            var observed = Date.parse(local.observedAt)
            var previous = Date.parse(local.previousObservedAt)
            if (!isFinite(previous) || observed <= previous || observed - previous > 3600000) continue
            for (var j = 0; j < publicAnnouncements.length; j++) {
                var event = publicAnnouncements[j]
                var at = Date.parse(event.effectiveAt || event.announcedAt)
                if (event.provider !== local.provider || event.kind !== "hard_reset" || event.expectedBy
                        || (event.expiresAt && Date.parse(event.expiresAt) <= announcementClock)
                        || !isFinite(at) || at < previous - 900000 || at > observed + 900000) continue
                if (match && match.id !== event.id) return null
                match = event
            }
        }
        return match
    }

    Process {
        id: diagnosticFailureProcess
        command: ["dankaiusage", "diagnostics", "helper-failed"]
    }

    function refreshDiagnostics() {
        if (diagnosticProcess.running) return
        diagnosticReport = ""
        diagnosticStatus = "Loading local diagnostics…"
        _diagnosticOutput = ""
        _diagnosticOverflow = false
        _diagnosticTimedOut = false
        diagnosticProcess.running = true
    }

    Process {
        id: diagnosticProcess
        command: ["dankaiusage", "diagnostics"]
        stdout: SplitParser {
            onRead: data => {
                if (root._diagnosticOutput.length + data.length > 65536) {
                    root._diagnosticOverflow = true
                    return
                }
                root._diagnosticOutput += data + "\n"
            }
        }
        onExited: (exitCode, exitStatus) => {
            if (root._diagnosticTimedOut) return
            root.diagnosticStatus = "Local diagnostics unavailable. Check that the helper is up to date."
            if (exitCode !== 0 || root._diagnosticOverflow) return
            try {
                var result = JSON.parse(root._diagnosticOutput)
                if (result.available !== true || typeof result.report !== "string") return
                root.diagnosticReport = result.report
                root.diagnosticStatus = "Preview before copying. Times are UTC (Z); includes build identifiers. Nothing is uploaded."
            } catch (e) { /* Never include parser errors or helper output in reports. */ }
        }
    }

    Timer {
        interval: 10000
        running: diagnosticProcess.running
        onTriggered: {
            root._diagnosticTimedOut = true
            diagnosticProcess.running = false
            root.diagnosticReport = ""
            root.diagnosticStatus = "Local diagnostics timed out."
        }
    }

    function historyGroupKey(event, fallbackIndex) {
        if (!event || !event.groupId) return "legacy:" + fallbackIndex
        return event.provider + "\u0000" + event.observedAt + "\u0000" + event.groupId
    }

    function historyEventEligible(event) {
        if (!event || event.explainable !== true || !event.groupId) return false
        return event.kind !== "scheduled_window"
                && (!event.kind || event.kind.indexOf("plugin_reset_") !== 0)
    }

    function explanationChoicesForGroup(group) {
        var choices = []
        var seen = ({})
        if (!group || !group.events) return choices
        for (var i = 0; i < group.events.length; i++) {
            if (!historyEventEligible(group.events[i])) continue
            var eventChoices = group.events[i].explanationChoices || []
            for (var j = 0; j < eventChoices.length; j++) {
                var choice = eventChoices[j]
                if (!seen[choice]) {
                    seen[choice] = true
                    choices.push(choice)
                }
            }
        }
        return choices
    }

    function historyGroups(events) {
        var groups = []
        var indexes = ({})
        for (var i = 0; i < events.length; i++) {
            var event = events[i]
            if (!event) continue
            var key = historyGroupKey(event, i)
            var index = indexes[key]
            if (index === undefined) {
                index = groups.length
                indexes[key] = index
                groups.push({
                    key: key,
                    groupId: event.groupId || "",
                    provider: event.provider || "",
                    observedAt: event.observedAt || "",
                    events: [],
                    explainable: false,
                    eligibleCount: 0,
                    explanation: null,
                    explanationChoices: []
                })
            }
            var group = groups[index]
            group.events.push(event)
            if (historyEventEligible(event)) {
                group.explainable = true
                group.eligibleCount++
            }
            if (!group.explanation && event.explanation) group.explanation = event.explanation
        }
        for (var g = 0; g < groups.length; g++)
            groups[g].explanationChoices = explanationChoicesForGroup(groups[g])
        groups.sort(function(a, b) {
            return Date.parse(b.observedAt) - Date.parse(a.observedAt)
        })
        return groups
    }

    function historyProviderVisible(event) {
        return event && ((event.provider === "codex" && showCodex)
                || (event.provider === "claude" && showClaude))
    }

    function hasHistoryExplanation(group) {
        return !!(group && group.explanation && group.explanation.reason)
    }

    function latestExplanationPrompt(nowMs) {
        var eligibleEvents = usageHistory.filter(function(event) {
            return historyProviderVisible(event) && historyEventEligible(event)
        })
        var groups = historyGroups(eligibleEvents)
        if (groups.length === 0) return null
        // Select the newest candidate before checking its answer. This is one
        // prompt, not a queue that reveals older unanswered observations.
        var latest = groups[0]
        if (hasHistoryExplanation(latest)) return null
        // Keep legacy jitter in Advanced history without asking the user to
        // explain it or revealing a queue of older unanswered prompts.
        if (latest.events.every(historyEventNeedsNoPrompt)) return null
        var observed = Date.parse(latest.observedAt)
        var age = nowMs - observed
        if (!isFinite(observed) || age < 0 || age > 24 * 60 * 60 * 1000) return null
        return latest
    }

    function historyEventNeedsNoPrompt(event) {
        if (event.timingNoise === true) return true
        var before = event.before || {}
        var after = event.after || {}
        var clearRefill = typeof before.usedPercent === "number" && isFinite(before.usedPercent)
                && typeof after.usedPercent === "number" && isFinite(after.usedPercent)
                && before.usedPercent >= 0 && before.usedPercent <= 100
                && after.usedPercent >= 0 && after.usedPercent <= 100
                && after.usedPercent < before.usedPercent - 0.001
        // A clear observation does not require knowing its ultimate cause.
        // Keep optional explanations in history without claiming a bonus.
        if (event.kind === "allowance_increased_unknown") return clearRefill
        if (event.kind === "reset_redeemed_inferred")
            return clearRefill && typeof before.availableCredits === "number"
                    && typeof after.availableCredits === "number"
                    && Number.isInteger(before.availableCredits) && Number.isInteger(after.availableCredits)
                    && after.availableCredits >= 0 && before.availableCredits > after.availableCredits
        return false
    }

    function visibleHistoryGroups() {
        // Keep ADR-0011's latest-eight-event scope, but hydrate any selected
        // group from retained history so its related-change count and edit
        // target describe the whole exact observation group.
        var recentGroups = historyGroups(visibleHistory())
        var fullGroups = historyGroups(usageHistory.filter(function(event) {
            return historyProviderVisible(event)
        }))
        var fullByKey = ({})
        for (var i = 0; i < fullGroups.length; i++) fullByKey[fullGroups[i].key] = fullGroups[i]
        for (var j = 0; j < recentGroups.length; j++) {
            if (recentGroups[j].groupId && fullByKey[recentGroups[j].key])
                recentGroups[j] = fullByKey[recentGroups[j].key]
        }
        return recentGroups
    }

    function historyGroupsForDisplay() {
        var groups = visibleHistoryGroups()
        if (!historyExplanationGroup || historyExplanationLocation === "prompt") return groups
        if (!advancedDropdown) return [historyExplanationGroup]
        for (var i = 0; i < groups.length; i++) {
            if (groups[i].key === historyExplanationGroup.key) return groups
        }
        groups.push(historyExplanationGroup)
        groups.sort(function(a, b) {
            return Date.parse(b.observedAt) - Date.parse(a.observedAt)
        })
        return groups
    }

    function historyProviderName(provider) {
        return provider === "codex" ? "Codex" : provider === "claude" ? "Claude" : provider
    }

    function historyGroupSummary(group) {
        if (!group) return ""
        return historyProviderName(group.provider) + " · observed " + formatShortDateTime(group.observedAt)
                + " · " + group.eligibleCount + (group.eligibleCount === 1 ? " change" : " related changes")
    }

    function historyPromptTitle(group) {
        if (!group || !group.events) return "Recent quota change"
        for (var i = 0; i < group.events.length; i++) {
            var event = group.events[i]
            if (historyEventEligible(event) && event.timingNoise !== true)
                return historyEventTitle(event) + (event.label ? " · " + event.label : "")
        }
        return "Recent quota change"
    }

    function historyExplanationLabel(reason) {
        switch (reason) {
        case "subscription_change": return "Subscription or plan changed"
        case "external_reset": return "Reset used outside this plugin"
        case "account_change": return "Account or workspace changed"
        case "provider_bonus": return "Provider-announced bonus or reset"
        case "unknown": return "Not sure what caused this change"
        case "dismissed": return "Dismissed without assigning a cause"
        default: return "Explanation unavailable"
        }
    }

    function historyExplanationText(explanation) {
        return explanation && explanation.reason ? historyExplanationLabel(explanation.reason) : ""
    }

    function historyExplanationReasonChoices(group) {
        return explanationChoicesForGroup(group).filter(function(reason) {
            return reason !== "dismissed"
        }).map(function(reason) {
            return { key: reason, label: historyExplanationLabel(reason) }
        })
    }

    function historyNoteRuneLength(value) {
        var count = 0
        for (var i = 0; i < value.length; i++) {
            var code = value.charCodeAt(i)
            if (code >= 0xd800 && code <= 0xdbff && i + 1 < value.length) {
                var next = value.charCodeAt(i + 1)
                if (next >= 0xdc00 && next <= 0xdfff) i++
            }
            count++
        }
        return count
    }

    function truncateHistoryNote(value, maxRunes) {
        var count = 0
        var end = 0
        while (end < value.length && count < maxRunes) {
            var code = value.charCodeAt(end++)
            if (code >= 0xd800 && code <= 0xdbff && end < value.length) {
                var next = value.charCodeAt(end)
                if (next >= 0xdc00 && next <= 0xdfff) end++
            }
            count++
        }
        return value.slice(0, end)
    }

    function beginHistoryExplanation(group, location, expanded) {
        if (!group || !group.explainable || !group.groupId || historyExplanationProcess.running) return
        if (historyExplanationGroup) {
            if (historyExplanationGroup.key === group.key
                    && historyExplanationLocation === (location || "prompt")) {
                if (expanded !== false) historyExplanationExpanded = true
            }
            return
        }
        // Keep this exact group as the edit target. A poll may replace
        // usageHistory, but it must not retarget an in-progress edit.
        historyExplanationReason = group.explanation ? group.explanation.reason || "" : ""
        historyExplanationNote = group.explanation ? group.explanation.note || "" : ""
        historyExplanationError = ""
        // Publish the target only after its draft is initialized; inline
        // editors become visible synchronously when these properties change.
        historyExplanationGroup = group
        historyExplanationLocation = location || "prompt"
        historyExplanationExpanded = expanded !== false
    }

    function cancelHistoryExplanation() {
        if (historyExplanationProcess.running) return
        historyExplanationGroup = null
        historyExplanationLocation = ""
        historyExplanationExpanded = false
        historyExplanationReason = ""
        historyExplanationNote = ""
        historyExplanationError = ""
    }

    function historyExplanationChoiceAllowed(group, reason) {
        return explanationChoicesForGroup(group).indexOf(reason) >= 0
    }

    function answerHistoryPrompt(group, reason) {
        if (!group) return
        if (historyExplanationGroup && (historyExplanationGroup.key !== group.key
                || historyExplanationLocation !== "prompt")) return
        beginHistoryExplanation(group, "prompt", false)
        if (historyExplanationGroup && historyExplanationGroup.key === group.key
                && historyExplanationLocation === "prompt")
            submitHistoryExplanation(reason)
    }

    function submitHistoryExplanation(reasonOverride) {
        if (!historyExplanationGroup || historyExplanationProcess.running) return
        if (usageProcess.running) {
            historyExplanationError = "Usage is refreshing. Try again in a moment."
            return
        }
        var reason = reasonOverride || historyExplanationReason
        if (!historyExplanationChoiceAllowed(historyExplanationGroup, reason)) {
            historyExplanationError = "Choose one of the available explanations."
            return
        }
        if (reasonOverride) {
            historyExplanationReason = reason
            historyExplanationNote = ""
        }
        _historyExplanationOutput = ""
        _historyExplanationStderr = ""
        _historyExplanationTimedOut = false
        historyExplanationError = ""
        _historyExplanationPayload = JSON.stringify({
            groupId: historyExplanationGroup.groupId,
            reason: reason,
            note: historyExplanationNote
        })
        historyExplanationProcess.command = ["dankaiusage", "history", "explain"]
        historyExplanationProcess.stdinEnabled = true
        historyExplanationProcess.running = true
    }

    Process {
        id: historyExplanationProcess
        running: false

        onStarted: {
            write(root._historyExplanationPayload + "\n")
            // Quickshell closes the write channel when stdin is disabled.
            stdinEnabled = false
        }
        stdout: SplitParser {
            onRead: data => { root._historyExplanationOutput += data + "\n" }
        }
        stderr: SplitParser {
            onRead: data => { root._historyExplanationStderr += data + "\n" }
        }
        onExited: (exitCode, exitStatus) => {
            if (root._historyExplanationTimedOut) {
                root._historyExplanationTimedOut = false
                root.historyExplanationError = "Saving timed out. Your draft is still here; retry when ready."
            } else {
                try {
                    var result = JSON.parse(root._historyExplanationOutput.trim())
                    if (exitCode !== 0 || !Array.isArray(result.history))
                        throw new Error(result.historyError || "The explanation could not be saved.")
                    root.usageHistory = result.history
                    root.historyError = result.historyError || ""
                    root.cancelHistoryExplanation()
                } catch (e) {
                    root.historyExplanationError = e.message || "The explanation could not be saved. Retry when ready."
                }
            }
            root._historyExplanationPayload = ""
            if (root._usageRefreshPending) {
                root._usageRefreshPending = false
                Qt.callLater(root.refreshUsage)
            }
        }
    }

    Timer {
        id: historyExplanationTimeout
        interval: 10000
        running: historyExplanationProcess.running
        onTriggered: {
            root._historyExplanationTimedOut = true
            historyExplanationProcess.running = false
        }
    }

    function runTracking(action) {
        if (trackingProcess.running) return
        if (action !== "status" && usageProcess.running) return
        _trackingOutput = ""
        trackingCommandError = ""
        clearTrackingConfirm = false
        trackingProcess.command = ["dankaiusage", "tracking", action]
        trackingProcess.running = true
    }

    Process {
        id: trackingProcess
        running: false
        stdout: SplitParser {
            onRead: data => { root._trackingOutput += data + "\n" }
        }
        onExited: (exitCode, exitStatus) => {
            try {
                var result = JSON.parse(root._trackingOutput.trim())
                var status = result.tracking || result
                if (typeof status.known !== "boolean") throw new Error("Invalid tracking status")
                root.trackingStatus = status
                if (exitCode !== 0 || status.known !== true)
                    root.trackingCommandError = "Tracking status is unavailable. Retry after checking the helper."
            } catch (e) {
                root.trackingStatus = ({ known: false, enabled: false, providers: {}, errors: [] })
                root.trackingCommandError = "Tracking status is unavailable. Retry after checking the helper."
            }
            // Re-read all totals after each serialized helper-owned transition.
            root._usageRefreshPending = false
            Qt.callLater(root.refreshUsage)
        }
    }

    Process {
        id: claudePrimeProcess
        running: false
        stdout: SplitParser {
            onRead: data => { root._claudePrimeOutput += data + "\n" }
        }
        stderr: SplitParser {
            onRead: data => { root._claudePrimeError += data + "\n" }
        }
        onExited: (exitCode, exitStatus) => {
            var message = ""
            try {
                var result = JSON.parse(root._claudePrimeOutput.trim())
                message = result.message || ""
            } catch (e) {
                message = root._claudePrimeError.trim()
            }
            if (message === "") message = exitCode === 0 ? "Claude account limits refreshed" : "Claude account refresh failed"
            root.claudePrimeText = message
            root.isPrimingClaude = false
            root.lastClaudeAutoPrimeAt = Date.now()
            root.lastClaudeAutoPrimeFailed = exitCode !== 0 && root.claudePrimeAutomatic
            if (root.pluginService && root.pluginService.savePluginState) {
                root.pluginService.savePluginState(root.pluginId, "lastClaudeAutoPrimeAt", root.lastClaudeAutoPrimeAt)
                root.pluginService.savePluginState(root.pluginId, "lastClaudeAutoPrimeFailed", root.lastClaudeAutoPrimeFailed)
            }
            root.claudePrimeAutomatic = false
            if (exitCode !== 0) {
                root.hasError = true
                root.errorText = message
                return
            }
            root.refreshUsage()
        }
    }

    function applySummary(summary, allowAutoPrime) {
        resetClock = Date.now()
        capabilities = summary.capabilities || {}
        providers = summary.providers || []
        usageHistory = summary.history || []
        historyError = summary.historyError || ""
        if (summary.tracking) trackingStatus = summary.tracking
        grandTotal = summary.grandTotal || ({ total: 0, input: 0, output: 0, cached: 0, requests: 0, sessions: 0 })
        hasError = (summary.errors || []).length > 0
        errorText = hasError ? summary.errors.join("\n") : ""

        var d = new Date(summary.generatedAt || Date.now())
        lastUpdated = ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2)
        var claude = claudeProvider()
        if (claudeSessionIsActive(claude) && lastClaudeAutoPrimeFailed) {
            lastClaudeAutoPrimeFailed = false
            if (pluginService && pluginService.savePluginState)
                pluginService.savePluginState(pluginId, "lastClaudeAutoPrimeFailed", false)
        }
        if (allowAutoPrime !== false) maybeAutoPrimeClaude()
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

    function visibleHistory() {
        return usageHistory.filter(function(event) {
            return event && ((event.provider === "codex" && root.showCodex)
                    || (event.provider === "claude" && root.showClaude))
        }).sort(function(a, b) {
            return Date.parse(b.observedAt) - Date.parse(a.observedAt)
        }).slice(0, 8)
    }

    function historyEventTitle(event) {
        switch (event.kind) {
        case "scheduled_window": return "Scheduled window change"
        case "allowance_increased_unknown": return "Unexpected replenishment"
        case "reset_redeemed_inferred": return "Likely reset redeemed"
        case "window_changed_unknown": return event.timingNoise === true ? "Minor reset-time adjustment" : "Reset time changed"
        case "credits_changed": return "Available resets changed"
        case "plugin_reset_reset": return "Plugin reset applied"
        case "plugin_reset_already_redeemed": return "Reset already redeemed"
        case "plugin_reset_nothing_to_reset": return "Nothing eligible to reset"
        case "plugin_reset_no_credit": return "No reset credit available"
        case "plugin_reset_attempt_unknown": return "Plugin reset outcome unknown"
        default: return "Quota change observed"
        }
    }

    function historyPercent(value) {
        return Math.round((showUsed ? value : 100 - value) * 10) / 10
                + (showUsed ? "% used" : "% left")
    }

    function historyEventDetail(event) {
        var lines = []
        var provider = event.provider === "codex" ? "Codex" : "Claude"
        lines.push(provider + (event.label ? " · " + event.label : "")
                + " · " + (event.confidence || "observed"))
        lines.push("Observed " + formatShortDateTime(event.observedAt))
        if (event.previousObservedAt)
            lines.push("Previous sample " + formatShortDateTime(event.previousObservedAt))
        var before = event.before || {}
        var after = event.after || {}
        if (typeof before.usedPercent === "number" && typeof after.usedPercent === "number")
            lines.push(historyPercent(before.usedPercent) + " → " + historyPercent(after.usedPercent))
        if (typeof before.availableCredits === "number" && typeof after.availableCredits === "number")
            lines.push("Available resets: " + before.availableCredits + " → " + after.availableCredits)
        if (before.resetAt && after.resetAt && before.resetAt !== after.resetAt)
            lines.push("Reset time: " + formatShortDateTime(before.resetAt)
                    + " → " + formatShortDateTime(after.resetAt))
        if (event.message) lines.push(event.message)
        return lines.join("\n")
    }

    function displayTotal(totals) {
        if (!totals) return 0
        if (includeCachedTokens) return totals.total || 0
        return (totals.total || 0) - (totals.cached || 0)
    }

    function displayInput(totals) {
        if (!totals) return 0
        return Math.max(0, displayTotal(totals) - displayOutput(totals))
    }

    function displayOutput(totals) {
        if (!totals) return 0
        return totals.output || 0
    }

    function filteredGrandTotal() {
        var total = 0
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) {
            if (tokenHistoryAvailable(list[i])) total += displayTotal(tokenHistoryTotals(list[i]))
        }
        return total
    }

    function filteredGrandInput() {
        var total = 0
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) {
            if (tokenHistoryAvailable(list[i])) total += displayInput(tokenHistoryTotals(list[i]))
        }
        return total
    }

    function filteredGrandOutput() {
        var total = 0
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) {
            if (tokenHistoryAvailable(list[i])) total += displayOutput(tokenHistoryTotals(list[i]))
        }
        return total
    }

    function knownAllowance(allowance) {
        return allowance && allowance.known
    }

    function setQuickSetting(key, value) {
        root[key] = value
        if (pluginService && pluginService.savePluginData)
            pluginService.savePluginData(pluginId, key, value)
    }

    function displayPercent(allowance) {
        if (!knownAllowance(allowance)) return 0
        return Math.max(0, Math.min(100, (showUsed ? allowance.percentUsed : allowance.percentRemaining) || 0))
    }

    function allowanceLabel(allowance, includeMode) {
        if (allowance && allowance.source === "claude-prime local usage") return "Timer"
        if (!knownAllowance(allowance)) return "--"
        return Math.round(displayPercent(allowance)) + (includeMode === false ? "%" : (showUsed ? "% used" : "% left"))
    }

    function allowanceDetail(allowance) {
        if (allowance && allowance.source === "claude-prime local usage") return "Started by Claude prime"
        if (!knownAllowance(allowance)) return "Limit unavailable"
        if (allowance.unit === "percent") return allowanceLabel(allowance) + " of subscription window"
        return formatTokens((showUsed ? allowance.used : allowance.remaining) || 0) + (showUsed ? " used of " : " left of ") + formatTokens(allowance.limit || 0)
    }

    function allowanceSeverity(allowance) {
        if (!knownAllowance(allowance)) return "unknown"
        var pct = allowance.percentRemaining || 0
        if (pct <= 10) return "critical"
        if (pct <= 25) return "low"
        return "ok"
    }

    function allowanceColor(allowance) {
        switch (allowanceSeverity(allowance)) {
        case "critical": return Theme.error
        case "low": return Theme.warning
        case "ok": return Theme.primary
        default: return Theme.surfaceVariantText
        }
    }

    function providerQuotaBuckets(provider) {
        if (!provider || !provider.quotaBuckets) return []
        return provider.quotaBuckets
    }

    function providerQuotaHeight(provider) {
        var buckets = providerQuotaBuckets(provider)
        var height = 0
        for (var i = 0; i < buckets.length; i++) height += quotaRowHeight(buckets[i])
        // Empty state holds a two-line notice.
        return buckets.length > 0 ? height + (buckets.length - 1) * Theme.spacingS : 40
    }

    // Label + inline countdown (18px), allowance bar (6px) and, in Advanced,
    // a thin window-time bar below it.
    function quotaRowHeight(bucket) {
        return advancedDropdown && bucket && bucket.kind !== "credits"
                && resetTimeProgress(bucket.allowance, resetClock, showUsed) >= 0 ? 34 : 28
    }

    function resetTiming(allowance, now) {
        if (!allowance || !allowance.resetAt || !isFinite(now)) return null
        var end = new Date(allowance.resetAt).getTime()
        if (!isFinite(end)) return null
        var minutes = allowance.windowMinutes
        var duration = typeof minutes === "number" && isFinite(minutes) && minutes > 0
                ? minutes * 60000 : 0
        var remaining = end - now
        return { remainingMs: remaining, durationMs: duration,
            progressKnown: isFinite(duration) && duration > 0 && remaining > 0 && remaining <= duration }
    }

    function resetDuration(ms) {
        if (ms < 60000) return "<1m"
        var minutes = Math.ceil(ms / 60000)
        var days = Math.floor(minutes / 1440)
        var hours = Math.floor((minutes % 1440) / 60)
        if (days > 0) return days + "d" + (hours ? " " + hours + "h" : "")
        if (hours > 0) return hours + "h" + (minutes % 60 ? " " + (minutes % 60) + "m" : "")
        return minutes + "m"
    }

    function resetCountdown(allowance, now, used) {
        var timing = resetTiming(allowance, now)
        if (!timing) return ""
        if (timing.remainingMs <= 0) return "Reset due · awaiting update"
        if (used && timing.progressKnown)
            return "Window elapsed: " + resetDuration(timing.durationMs - timing.remainingMs)
        return "Resets in " + resetDuration(timing.remainingMs)
    }

    function resetTimeProgress(allowance, now, used) {
        var timing = resetTiming(allowance, now)
        if (!timing || !timing.progressKnown) return -1
        var left = Math.max(0, Math.min(1, timing.remainingMs / timing.durationMs))
        return used ? 1 - left : left
    }

    function quotaValue(bucket) {
        if (!bucket) return "--"
        return allowanceLabel(bucket.allowance)
    }

    function quotaDetail(bucket) {
        if (!bucket) return "Limit unavailable"
        if (bucket.kind === "credits") {
            if (showUsed && bucket.valueLabel) return bucket.valueLabel + " used"
            if (!showUsed && bucket.detail) return bucket.detail
        }
        if (bucket.detail) return bucket.detail
        var countdown = resetCountdown(bucket.allowance, resetClock, showUsed)
        return countdown || allowanceDetail(bucket.allowance)
    }

    function quotaProgress(bucket) {
        if (!bucket || !knownAllowance(bucket.allowance)) return 0
        return displayPercent(bucket.allowance)
    }

    function quotaShortLabel(bucket) {
        if (!bucket) return "Quota"
        if (bucket.kind === "credits") return "Credits"
        return bucket.label || "Quota"
    }

    function weakestProviderQuota(provider) {
        var weakest = null
        var buckets = providerQuotaBuckets(provider)
        for (var i = 0; i < buckets.length; i++) {
            if (!knownAllowance(buckets[i].allowance)) continue
            if (!weakest || (buckets[i].allowance.percentRemaining || 0) < (weakest.allowance.percentRemaining || 0)) weakest = buckets[i]
        }
        return weakest
    }

    function weakestQuotaEntry() {
        var weakest = null
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) {
            var bucket = weakestProviderQuota(list[i])
            if (!bucket) continue
            if (!weakest || (bucket.allowance.percentRemaining || 0) < (weakest.bucket.allowance.percentRemaining || 0))
                weakest = ({ provider: list[i], bucket: bucket })
        }
        return weakest
    }

    function quotaBucketCount() {
        var count = 0
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) count += providerQuotaBuckets(list[i]).length
        return count
    }

    function overallQuotaTitle() {
        var entry = weakestQuotaEntry()
        if (!entry) return "Quota overview"
        return "Most constrained · " + entry.provider.name + " · " + quotaShortLabel(entry.bucket)
    }

    function overallQuotaAllowance() {
        var entry = weakestQuotaEntry()
        return entry ? entry.bucket.allowance : null
    }

    function overallQuotaDetail() {
        var entry = weakestQuotaEntry()
        return entry ? quotaDetail(entry.bucket) : "No quota data"
    }

    function providerResets(provider) {
        if (!provider || !provider.resets) return []
        return provider.resets
    }

    function providerResetSummary(provider) {
        var resets = providerResets(provider)
        if (resets.length === 0) return ""
        if (resets.length === 1) return (resets[0].title || "Usage reset") + " available"
        return resets.length + " usage resets available"
    }

    function providerResetDetail(provider) {
        var resets = providerResets(provider)
        if (resets.length === 0) return ""
        var earliest = ""
        for (var i = 0; i < resets.length; i++) {
            var at = Date.parse(resets[i].expiresAt || "")
            if (isFinite(at) && (earliest === "" || at < Date.parse(earliest)))
                earliest = resets[i].expiresAt
        }
        var expiry = formatShortDateTime(earliest)
        return "Next expiry" + (expiry !== "" ? " · " + expiry : " not reported")
    }

    function codexResetDetailText() {
        var message = codexResetStatus.message || "Automatic reset is off"
        if (codexResetStatus.error)
            message += "\n" + codexResetStatus.error
        if (codexResetStatus.historyError)
            message += "\n" + codexResetStatus.historyError
        if (codexResetStatus.armed && codexResetStatus.expiresAt) {
            message = (codexResetStatus.title || "Usage reset") + " · expires "
                    + formatShortDateTime(codexResetStatus.expiresAt) + "\n" + message
        }
        return message
    }

    function providerAllowanceSummary(provider) {
        var bucket = weakestProviderQuota(provider)
        return bucket ? quotaShortLabel(bucket) + " " + allowanceLabel(bucket.allowance) : "--"
    }

    function claudeBucketShownInBar(bucket) {
        if (!bucket) return false
        if (bucket.kind === "credits") return barShowClaudeCredits
        if (bucket.allowance && bucket.allowance.window === "session") return barShowClaudeSession
        if (bucket.allowance && bucket.allowance.window === "weekly") {
            var selected = barClaudeWeeklyOverrides[bucket.id]
            return typeof selected === "boolean" ? selected : barShowClaudeWeekly
        }
        return false
    }

    function claudeWeeklyBarChoices() {
        var buckets = providerQuotaBuckets(claudeProvider())
        var choices = []
        for (var i = 0; i < buckets.length; i++) {
            if (buckets[i].id && buckets[i].allowance && buckets[i].allowance.window === "weekly")
                choices.push(buckets[i])
        }
        return choices
    }

    function setClaudeWeeklyBarBucket(bucket, selected) {
        var overrides = Object.assign({}, barClaudeWeeklyOverrides)
        overrides[bucket.id] = selected
        setQuickSetting("barClaudeWeeklyOverrides", overrides)
    }

    function providerTopBarBuckets(provider) {
        if (!provider) return []
        if (provider.id !== "claude") {
            var weakest = weakestProviderQuota(provider)
            return weakest ? [weakest] : []
        }

        var out = []
        var buckets = providerQuotaBuckets(provider)
        for (var i = 0; i < buckets.length; i++) {
            if (claudeBucketShownInBar(buckets[i])) out.push(buckets[i])
        }
        return out
    }

    function providerTopBarText(provider) {
        var buckets = providerTopBarBuckets(provider)
        var parts = []
        for (var i = 0; i < buckets.length; i++) {
            parts.push(quotaShortLabel(buckets[i]) + " " + allowanceLabel(buckets[i].allowance, false))
        }
        return parts.join(" · ")
    }

    function topBarSegments() {
        var list = visibleProviders()
        var segments = []

        if (compactPill) {
            for (var i = 0; i < list.length; i++) {
                var compactWeakest = null
                var compactBuckets = providerTopBarBuckets(list[i])
                for (var j = 0; j < compactBuckets.length; j++) {
                    if (!knownAllowance(compactBuckets[j].allowance)) continue
                    if (!compactWeakest || (compactBuckets[j].allowance.percentRemaining || 0) < (compactWeakest.allowance.percentRemaining || 0))
                        compactWeakest = compactBuckets[j]
                }
                if (!compactWeakest && compactBuckets.length > 0) compactWeakest = compactBuckets[0]
                if (compactWeakest) {
                    segments.push({
                        provider: list[i],
                        allowance: compactWeakest.allowance,
                        text: (barShowProviderLogos ? "" : list[i].name + " ") + quotaShortLabel(compactWeakest) + " " + allowanceLabel(compactWeakest.allowance, false)
                    })
                }
            }
            return segments
        }

        for (var k = 0; k < list.length; k++) {
            var text = providerTopBarText(list[k])
            if (text === "") continue
            // Color the segment by its most constrained shown quota.
            var shown = providerTopBarBuckets(list[k])
            var weakestShown = null
            for (var m = 0; m < shown.length; m++) {
                if (!knownAllowance(shown[m].allowance)) continue
                if (!weakestShown || (shown[m].allowance.percentRemaining || 0) < (weakestShown.allowance.percentRemaining || 0))
                    weakestShown = shown[m]
            }
            segments.push({
                provider: list[k],
                allowance: weakestShown ? weakestShown.allowance : null,
                text: (barShowProviderLogos ? "" : list[k].name + " ") + text
            })
        }
        return segments
    }

    function claudeProvider() {
        if (!showClaude) return null
        for (var i = 0; i < providers.length; i++) {
            if (providers[i].id === "claude") return providers[i]
        }
        return null
    }

    function resetIsFuture(allowance) {
        if (!allowance || !allowance.resetAt) return false
        var d = new Date(allowance.resetAt)
        return !isNaN(d.getTime()) && d.getTime() > Date.now() + 60000
    }

    function claudeSessionIsActive(provider) {
        if (!provider) return false
        var allowance = provider.sessionLeft
        if (!allowance) return false
        if (allowance.known) return resetIsFuture(allowance)
        if (provider.session && (provider.session.requests || 0) > 0) return resetIsFuture(allowance)
        return allowance.source === "claude-prime local usage" && resetIsFuture(allowance)
    }

    function maybeAutoPrimeClaude() {
        if (!enableClaudePrime || !showClaude || isPrimingClaude || claudePrimeProcess.running) return
        var provider = claudeProvider()
        if (!provider || !provider.available || claudeSessionIsActive(provider) || lastClaudeAutoPrimeFailed) return
        if (provider.meta && provider.meta.usageRefreshPending === true) return
        // A cooldown-only helper result must not create a prime -> summary ->
        // prime loop. Manual priming remains an explicit separate action.
        if (lastClaudeAutoPrimeAt > 0 && Date.now() < lastClaudeAutoPrimeAt + refreshInterval * 1000) return
        lastClaudeAutoPrimeAt = Date.now()
        if (pluginService && pluginService.savePluginState)
            pluginService.savePluginState(pluginId, "lastClaudeAutoPrimeAt", lastClaudeAutoPrimeAt)
        primeClaude(true)
    }

    function formatReset(allowance) {
        if (!allowance || !allowance.resetAt) return "--"
        var d = new Date(allowance.resetAt)
        if (isNaN(d.getTime())) return "--"
        var clock = ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2)
        if (allowance.window === "weekly") {
            var days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
            return days[d.getDay()] + " " + (d.getMonth() + 1) + "/" + d.getDate() + " " + clock
        }
        return clock
    }

    function formatShortDateTime(value) {
        if (!value) return ""
        var d = new Date(value)
        if (isNaN(d.getTime())) return ""
        var clock = ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2)
        return (d.getMonth() + 1) + "/" + d.getDate() + " " + clock
    }

    function formatTokens(value) {
        value = Math.max(0, value || 0)
        if (value >= 1000000000) return (value / 1000000000).toFixed(1) + "B"
        if (value >= 1000000) return (value / 1000000).toFixed(1) + "M"
        if (value >= 1000) return (value / 1000).toFixed(1) + "K"
        return "" + value
    }

    function inputTokenLabel(totals) {
        return formatTokens(displayInput(totals)) + " in"
    }

    function outputTokenLabel(totals) {
        return formatTokens(displayOutput(totals)) + " out"
    }

    function filteredGrandTokenBreakdown() {
        var list = visibleProviders()
        var available = 0
        var partial = false
        for (var i = 0; i < list.length; i++) {
            if (tokenHistoryAvailable(list[i])) available++
            if (!tokenHistoryAvailable(list[i])) partial = true
            if (tokenHistoryRange !== "tracked" && list[i].meta && list[i].meta.tokenDataError) partial = true
        }
        if (tokenHistoryRange === "tracked" && trackingStatus.errors && trackingStatus.errors.length > 0)
            partial = true
        if (available === 0) return "Unavailable"
        return formatTokens(filteredGrandInput()) + " in / " + formatTokens(filteredGrandOutput()) + " out" + (partial ? " (partial)" : "")
    }

    function tokenHistoryAvailable(provider) {
        if (!provider) return false
        if (tokenHistoryRange === "tracked") {
            if (trackingStatus.known !== true || !trackingStatus.startedAt) return false
            return !!tokenHistoryTotals(provider)
        }
        if (!tokenHistoryTotals(provider)) return false
        var meta = provider.meta || {}
        if (meta.tokenDataAvailable === false) return false
        if (meta.tokenDataAvailable === true) return true
        return !meta.tokenDataError || (provider.period && provider.period.requests > 0)
                || (provider.session && provider.session.requests > 0)
    }

    function isTokenHistoryRange(value) {
        if (value === "period") return periodDays !== 7 && periodDays !== 30 && periodDays !== 90
        for (var i = 0; i < tokenHistoryRanges.length; i++) {
            if (tokenHistoryRanges[i].key === value) return true
        }
        return false
    }

    function migratedPeriodRange(days) {
        if (days === 7 || days === 30 || days === 90) return days + "d"
        return "period"
    }

    function tokenHistoryRangeChoices() {
        var choices = tokenHistoryRanges.slice(0)
        if (periodDays !== 7 && periodDays !== 30 && periodDays !== 90)
            choices.splice(choices.length - 1, 0, { key: "period", label: periodDays + "d" })
        return choices
    }

    function tokenHistoryTotals(provider) {
        if (!provider) return null
        if (tokenHistoryRange === "tracked") {
            var trackedProviders = trackingStatus.providers || {}
            return trackedProviders[provider.id] || null
        }
        var rolling = provider.rolling || {}
        if (tokenHistoryRange === "5h" && rolling.fiveHours) return rolling.fiveHours
        if (tokenHistoryRange === "7d" && rolling.sevenDays) return rolling.sevenDays
        if (tokenHistoryRange === "30d" && rolling.thirtyDays) return rolling.thirtyDays
        if (tokenHistoryRange === "90d" && rolling.ninetyDays) return rolling.ninetyDays
        // Compatibility while cached summaries migrate to rolling totals.
        if (tokenHistoryRange === "5h") return provider.session
        if (tokenHistoryRange === "period") return provider.period
        return null
    }

    function tokenHistoryLabel() {
        var label = tokenHistoryRange === "period" ? periodDays + "d" : tokenHistoryRange
        return "Local tokens · " + (label === "tracked" ? "Tracked" : label)
    }

    function selectTokenHistoryRange(range) {
        if (!isTokenHistoryRange(range)) return
        clearTrackingConfirm = false
        tokenHistoryRange = range
        if (pluginService && pluginService.savePluginState)
            pluginService.savePluginState(pluginId, "tokenHistoryRange", tokenHistoryRange)
    }

    function trackingStateText() {
        if (trackingStatus.known !== true) return "Tracking status unavailable"
        var started = formatShortDateTime(trackingStatus.startedAt)
        if (trackingStatus.enabled === true)
            return "Tracking enabled" + (started ? " · started " + started : "")
        if (started) return "Tracking paused · started " + started
        return "Tracking is off · no tracked period yet"
    }

    function trackingDetailText() {
        if (trackingCommandError !== "") return trackingCommandError
        if (trackingStatus.errors && trackingStatus.errors.length > 0)
            return "Some tracked token data is incomplete. Refresh or check the helper."
        if (trackingStatus.enabled === true)
            return "The initial total may include older retained local history."
        if (trackingStatus.startedAt) return "Totals are retained; paused usage is not backfilled on resume."
        return "Enable to seed a persistent total from retained local history."
    }

    function providerTokenBreakdown(provider) {
        if (!tokenHistoryAvailable(provider)) return "Unavailable"
        var totals = tokenHistoryTotals(provider)
        return inputTokenLabel(totals) + " / " + outputTokenLabel(totals)
                + (tokenHistoryRange === "tracked"
                    ? (trackingStatus.errors && trackingStatus.errors.length > 0 ? " (partial)" : "")
                    : (provider.meta && provider.meta.tokenDataError ? " (partial)" : ""))
    }


    function providerLogoColor(provider) {
        if (!provider || !provider.available || provider.error) return Theme.error
        return Theme.primary
    }

    function providerColor(provider) {
        if (!provider || !provider.available || provider.error) return Theme.error
        var weakest = weakestProviderQuota(provider)
        return weakest ? allowanceColor(weakest.allowance) : Theme.surfaceVariantText
    }

    function barSegmentColor(allowance) {
        if (hasError) return Theme.error
        var severity = allowanceSeverity(allowance)
        if (severity === "critical") return Theme.error
        if (severity === "low") return Theme.warning
        return Theme.surfaceText
    }

    function noticeColor(level) {
        if (level === "error") return Theme.error
        if (level === "warning") return Theme.warning
        return Theme.surfaceVariantText
    }

    function noticeIcon(level) {
        if (level === "error") return "error"
        if (level === "warning") return "warning"
        return "info"
    }

    // Routine information stays neutral; only actionable failures use warning
    // or error styling, so a signed-out account is noticeable at a glance.
    function providerNotices(provider) {
        if (!provider || !provider.meta) return []
        var notices = []
        function add(text, level) {
            if (text) notices.push({ text: text, level: level })
        }
        if (provider.error) add(provider.error, "error")
        if (provider.meta.usageRefreshPending === true) {
            add("Usage refresh pending; waiting for the next eligible refresh", "info")
        } else if (provider.meta.usageStale === true || provider.meta.usageDataStale === true) {
            var staleNote = "Usage data is stale; the latest refresh failed"
            if (provider.meta.usageRefreshError) staleNote += ": " + provider.meta.usageRefreshError
            add(staleNote, "warning")
        }
        if (advancedDropdown && provider.meta.usageCached === true) {
            var cacheNote = "Cached usage"
            var nextRefresh = formatShortDateTime(provider.meta.usageNextRefreshAt)
            if (nextRefresh !== "") cacheNote += " · next refresh " + nextRefresh
            add(cacheNote, "info")
        }
        if (provider.id === "claude" && claudePrimeText !== "") add(claudePrimeText, "info")
        if (advancedDropdown && provider.id === "claude" && provider.meta.tokenDataIncludesWeb === false)
            add("Claude tokens are local Claude Code only, not web", "info")
        if (provider.id === "claude" && provider.meta.sessionFallbackSource)
            add("Session timer from Claude prime; account limits unavailable", "warning")
        if (advancedDropdown && provider.meta.tokenDataNote) {
            var note = provider.meta.tokenDataNote
            var lastUsage = formatShortDateTime(provider.meta.lastUsageAt)
            if (lastUsage !== "") note += " (last " + lastUsage + ")"
            add(note, "info")
        }
        if (provider.meta.limitError) {
            if (provider.id === "claude" && provider.meta.oauthUsageError) add(provider.meta.oauthUsageError, "error")
            else if (provider.id === "claude" && provider.meta.statuslineNextStep) add(provider.meta.statuslineNextStep, "error")
            else add(provider.meta.limitError, "error")
        }
        else if (provider.meta.tokenDataError) add(provider.meta.tokenDataError, "warning")
        return notices
    }

    function providerNote(provider) {
        var notices = providerNotices(provider)
        var parts = []
        for (var i = 0; i < notices.length; i++) parts.push(notices[i].text)
        return parts.join(" | ")
    }

    function anyUsageStale() {
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) {
            var meta = list[i].meta || {}
            if (meta.usageStale === true || meta.usageDataStale === true) return true
        }
        return false
    }

    function nextRefreshLabel() {
        var list = visibleProviders()
        var earliest = ""
        for (var i = 0; i < list.length; i++) {
            var meta = list[i].meta || {}
            if (!meta.usageNextRefreshAt) continue
            var at = Date.parse(meta.usageNextRefreshAt)
            if (isFinite(at) && (earliest === "" || at < Date.parse(earliest))) earliest = meta.usageNextRefreshAt
        }
        return formatShortDateTime(earliest)
    }

    function usageStatusText() {
        if (isLoading && providers.length === 0) return "Loading usage…"
        if (usageProcess.running) return "Refreshing…"
        var parts = []
        parts.push(lastUpdated ? "Updated " + lastUpdated : "Waiting for the first update")
        if (anyUsageStale()) parts.push("stale")
        var next = nextRefreshLabel()
        if (advancedDropdown && next !== "") parts.push("next refresh " + next)
        return parts.join(" · ")
    }

    function overviewCountdown() {
        var entry = weakestQuotaEntry()
        if (!entry) return ""
        if (entry.bucket.kind === "credits") return entry.bucket.detail || ""
        return resetCountdown(entry.bucket.allowance, resetClock, showUsed)
    }

    function nextUpcomingAnnouncement() {
        var list = visibleAnnouncements()
        for (var i = 0; i < list.length; i++) {
            if (!announcementsStale && announcementUpcoming(list[i], announcementClock)) return list[i]
        }
        return null
    }

    horizontalBarPill: Component {
        Row {
            spacing: Theme.spacingS

            DankIcon {
                name: "monitoring"
                size: Theme.fontSizeLarge
                color: root.hasError ? Theme.error : Theme.primary
                visible: root.barShowPluginIcon
                anchors.verticalCenter: parent.verticalCenter
            }

            Repeater {
                model: root.topBarSegments()

                Row {
                    spacing: Theme.spacingXS

                    ProviderLogo {
                        provider: modelData.provider
                        size: Theme.fontSizeMedium
                        visible: root.barShowProviderLogos
                        anchors.verticalCenter: parent.verticalCenter
                    }

                    StyledText {
                        text: modelData.text
                        font.pixelSize: Theme.fontSizeMedium
                        color: root.barSegmentColor(modelData.allowance)
                        anchors.verticalCenter: parent.verticalCenter
                        elide: Text.ElideRight
                        maximumLineCount: 1
                    }
                }
            }

            StyledText {
                text: root.isLoading && root.providers.length === 0 ? "..." : "--"
                visible: root.topBarSegments().length === 0
                font.pixelSize: Theme.fontSizeMedium
                color: root.hasError ? Theme.error : Theme.surfaceText
                anchors.verticalCenter: parent.verticalCenter
            }
        }
    }

    verticalBarPill: Component {
        Column {
            spacing: 1

            DankIcon {
                name: "monitoring"
                size: Theme.fontSizeLarge
                color: root.hasError ? Theme.error : Theme.primary
                anchors.horizontalCenter: parent.horizontalCenter
            }

            StyledText {
                property var entry: root.weakestQuotaEntry()
                text: entry ? root.allowanceLabel(entry.bucket.allowance, false)
                        : (root.isLoading && root.providers.length === 0 ? "..." : "--")
                font.pixelSize: Theme.fontSizeSmall
                color: entry ? root.barSegmentColor(entry.bucket.allowance) : Theme.surfaceText
                anchors.horizontalCenter: parent.horizontalCenter
            }
        }
    }

    popoutContent: Component {
        Item {
            id: popoutRoot
            implicitHeight: Math.min(popoutColumn.implicitHeight, root.popoutMaxHeight)

            DankFlickable {
                anchors.fill: parent
                contentWidth: width
                contentHeight: popoutColumn.implicitHeight
                clip: true

                Column {
                    id: popoutColumn
                    width: popoutRoot.width
                    spacing: Theme.spacingM

                    // Header: title, freshness, refresh.
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
                                text: root.usageStatusText()
                                font.pixelSize: Theme.fontSizeSmall
                                color: root.anyUsageStale() ? Theme.warning : Theme.surfaceVariantText
                                elide: Text.ElideRight
                                maximumLineCount: 1
                            }
                        }

                        DankActionButton {
                            id: refreshButton
                            buttonSize: 28
                            iconName: "refresh"
                            iconColor: usageProcess.running ? Theme.primary : Theme.surfaceVariantText
                            tooltipText: "Refresh usage (provider requests respect the selected interval)"
                            anchors.right: parent.right
                            anchors.verticalCenter: parent.verticalCenter
                            onClicked: root.refreshCycle()
                        }
                    }

                    // View controls: detail level and allowance direction.
                    Item {
                        width: parent.width
                        height: 30

                        SegmentSwitch {
                            anchors.left: parent.left
                            offLabel: "Simple"
                            onLabel: "Advanced"
                            text: root.advancedDropdown ? "Advanced" : "Simple"
                            checked: root.advancedDropdown
                            Accessible.name: text + " view; switch to " + (root.advancedDropdown ? "Simple" : "Advanced")
                            onClicked: root.setDropdownMode(root.advancedDropdown ? "simple" : "advanced")
                        }

                        SegmentSwitch {
                            anchors.right: parent.right
                            offLabel: "Left"
                            onLabel: "Used"
                            text: root.showUsed ? "Used" : "Left"
                            checked: root.showUsed
                            Accessible.name: "Quota " + text + "; switch to " + (root.showUsed ? "Left" : "Used")
                            onClicked: root.setQuickSetting("showUsed", !root.showUsed)
                        }
                    }

                    // Attention: failures and prompts that need a decision.
                    NoticeCard {
                        width: parent.width
                        level: "error"
                        text: root.errorText
                        visible: root.hasError && root.errorText !== ""
                    }

                    StyledRect {
                        id: historyPrompt
                        property var group: root.historyExplanationLocation === "prompt" && root.historyExplanationGroup
                                ? root.historyExplanationGroup : root.latestExplanationPrompt(Date.now())
                        width: parent.width
                        height: historyPromptContent.implicitHeight + 2 * Theme.spacingM
                        visible: group !== null
                        radius: Theme.cornerRadius
                        color: Theme.surfaceContainerHigh
                        border.width: 1
                        border.color: Theme.primary

                        Column {
                            id: historyPromptContent
                            x: Theme.spacingM
                            y: Theme.spacingM
                            width: parent.width - 2 * Theme.spacingM
                            spacing: Theme.spacingXS

                            Row {
                                width: parent.width
                                spacing: Theme.spacingXS

                                DankIcon {
                                    name: "help"
                                    size: Theme.fontSizeMedium
                                    color: Theme.primary
                                    anchors.verticalCenter: parent.verticalCenter
                                }

                                StyledText {
                                    width: parent.width - Theme.fontSizeMedium - Theme.spacingXS
                                    text: root.historyPromptTitle(historyPrompt.group)
                                    textFormat: Text.PlainText
                                    font.pixelSize: Theme.fontSizeSmall
                                    font.weight: Font.Medium
                                    color: Theme.surfaceText
                                    anchors.verticalCenter: parent.verticalCenter
                                    elide: Text.ElideRight
                                }
                            }

                            StyledText {
                                width: parent.width
                                text: root.historyGroupSummary(historyPrompt.group)
                                textFormat: Text.PlainText
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                                wrapMode: Text.WordWrap
                            }

                            Flow {
                                width: parent.width
                                spacing: Theme.spacingXS

                                CompactAction {
                                    text: "What changed?"
                                    visible: !(root.historyExplanationLocation === "prompt"
                                            && root.historyExplanationExpanded)
                                    enabled: !historyExplanationProcess.running
                                            && !usageProcess.running && historyPrompt.group !== null
                                            && (!root.historyExplanationGroup
                                                || root.historyExplanationLocation === "prompt")
                                    onClicked: root.beginHistoryExplanation(historyPrompt.group, "prompt", true)
                                }

                                CompactAction {
                                    text: historyExplanationProcess.running
                                            && root.historyExplanationReason === "unknown" ? "Saving..." : "Not sure"
                                    visible: historyPrompt.group !== null
                                            && root.historyExplanationChoiceAllowed(historyPrompt.group, "unknown")
                                            && !(root.historyExplanationLocation === "prompt"
                                                && root.historyExplanationExpanded)
                                    enabled: !historyExplanationProcess.running && !usageProcess.running
                                            && (!root.historyExplanationGroup
                                                || root.historyExplanationLocation === "prompt")
                                    onClicked: root.answerHistoryPrompt(historyPrompt.group, "unknown")
                                }

                                CompactAction {
                                    text: historyExplanationProcess.running
                                            && root.historyExplanationReason === "dismissed" ? "Saving..." : "Dismiss"
                                    visible: historyPrompt.group !== null
                                            && root.historyExplanationChoiceAllowed(historyPrompt.group, "dismissed")
                                            && !(root.historyExplanationLocation === "prompt"
                                                && root.historyExplanationExpanded)
                                    enabled: !historyExplanationProcess.running && !usageProcess.running
                                            && (!root.historyExplanationGroup
                                                || root.historyExplanationLocation === "prompt")
                                    onClicked: root.answerHistoryPrompt(historyPrompt.group, "dismissed")
                                }
                            }

                            StyledText {
                                width: parent.width
                                text: root.historyExplanationError
                                textFormat: Text.PlainText
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.error
                                wrapMode: Text.WordWrap
                                visible: root.historyExplanationLocation === "prompt"
                                        && !root.historyExplanationExpanded && text !== ""
                            }

                            HistoryExplanationEditor {
                                width: parent.width
                                visible: root.historyExplanationLocation === "prompt"
                                        && root.historyExplanationExpanded
                            }
                        }
                    }

                    StyledRect {
                        id: upcomingAnnouncementCard
                        property var event: root.nextUpcomingAnnouncement()
                        width: parent.width
                        height: upcomingAnnouncementContent.implicitHeight + 2 * Theme.spacingM
                        visible: event !== null
                        radius: Theme.cornerRadius
                        color: Theme.surfaceContainerHigh
                        border.width: 1
                        border.color: Theme.outlineVariant

                        Column {
                            id: upcomingAnnouncementContent
                            x: Theme.spacingM
                            y: Theme.spacingM
                            width: parent.width - 2 * Theme.spacingM
                            spacing: Theme.spacingXS

                            Row {
                                width: parent.width
                                spacing: Theme.spacingXS

                                DankIcon {
                                    name: "campaign"
                                    size: Theme.fontSizeMedium
                                    color: Theme.primary
                                    anchors.verticalCenter: parent.verticalCenter
                                }

                                StyledText {
                                    width: parent.width - Theme.fontSizeMedium - Theme.spacingXS
                                    text: upcomingAnnouncementCard.event
                                            ? "Announced reset · " + root.historyProviderName(upcomingAnnouncementCard.event.provider)
                                              + (upcomingAnnouncementCard.event.expectedBy
                                                 ? " · by " + root.formatShortDateTime(upcomingAnnouncementCard.event.expectedBy) : "")
                                            : ""
                                    textFormat: Text.PlainText
                                    font.pixelSize: Theme.fontSizeSmall
                                    font.weight: Font.Medium
                                    color: Theme.surfaceText
                                    anchors.verticalCenter: parent.verticalCenter
                                    elide: Text.ElideRight
                                }
                            }

                            StyledText {
                                width: parent.width
                                text: upcomingAnnouncementCard.event
                                        ? root.announcementSummary(upcomingAnnouncementCard.event)
                                          + "\nVerified by TokenResets · account eligibility unverified" : ""
                                textFormat: Text.PlainText
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                                wrapMode: Text.WordWrap
                            }

                            CompactAction {
                                text: "View source and evidence"
                                onClicked: if (upcomingAnnouncementCard.event) Qt.openUrlExternally(upcomingAnnouncementCard.event.url)
                            }
                        }
                    }

                    // Overview (Advanced): the single most constrained limit and local token totals.
                    StyledRect {
                        width: parent.width
                        height: overviewContent.implicitHeight + 2 * Theme.spacingM
                        visible: root.advancedDropdown
                        radius: Theme.cornerRadius
                        color: Theme.surfaceContainerHigh

                        Item {
                            id: overviewContent
                            x: Theme.spacingM
                            y: Theme.spacingM
                            width: parent.width - 2 * Theme.spacingM
                            implicitHeight: Math.max(overviewText.implicitHeight, overviewRing.height)

                            LimitBucket {
                                id: overviewText
                                anchors.left: parent.left
                                anchors.right: overviewRing.left
                                anchors.rightMargin: Theme.spacingM
                                anchors.verticalCenter: parent.verticalCenter
                                title: root.overallQuotaTitle()
                                allowance: root.overallQuotaAllowance()
                                detail: root.overviewCountdown()
                            }

                            AllowanceRing {
                                id: overviewRing
                                anchors.right: parent.right
                                anchors.verticalCenter: parent.verticalCenter
                                allowance: root.overallQuotaAllowance()
                            }
                        }
                    }

                    TokenHistoryRow {
                        width: parent.width
                        visible: root.advancedDropdown
                        value: root.filteredGrandTokenBreakdown()
                    }

                    TrackingPanel {
                        width: parent.width
                        visible: root.advancedDropdown && root.tokenHistoryRange === "tracked"
                    }

                    // Providers.
                    Column {
                        width: parent.width
                        spacing: Theme.spacingS
                        visible: root.visibleProviders().length > 0

                        Repeater {
                            model: root.visibleProviders()

                            StyledRect {
                                width: parent.width
                                height: providerContent.implicitHeight + 2 * Theme.spacingM
                                radius: Theme.cornerRadius
                                color: Theme.surfaceContainerHigh

                                Column {
                                    id: providerContent
                                    x: Theme.spacingM
                                    y: Theme.spacingM
                                    width: parent.width - 2 * Theme.spacingM
                                    spacing: Theme.spacingS

                                    Item {
                                        width: parent.width
                                        height: 26

                                        Row {
                                            anchors.left: parent.left
                                            anchors.right: providerActions.left
                                            anchors.rightMargin: Theme.spacingS
                                            anchors.verticalCenter: parent.verticalCenter
                                            spacing: Theme.spacingS

                                            ProviderLogo {
                                                provider: modelData
                                                size: Theme.fontSizeLarge
                                                anchors.verticalCenter: parent.verticalCenter
                                            }

                                            StyledText {
                                                text: modelData.name
                                                width: parent.width - Theme.fontSizeLarge - Theme.spacingS
                                                font.pixelSize: Theme.fontSizeMedium
                                                font.weight: Font.Medium
                                                color: Theme.surfaceText
                                                anchors.verticalCenter: parent.verticalCenter
                                                elide: Text.ElideRight
                                                maximumLineCount: 1
                                            }
                                        }

                                        Row {
                                            id: providerActions
                                            anchors.right: parent.right
                                            anchors.verticalCenter: parent.verticalCenter
                                            spacing: Theme.spacingXS

                                            StyledText {
                                                text: root.providerAllowanceSummary(modelData)
                                                width: Math.min(160, implicitWidth)
                                                font.pixelSize: Theme.fontSizeMedium
                                                font.weight: Font.Bold
                                                color: root.providerColor(modelData)
                                                anchors.verticalCenter: parent.verticalCenter
                                                elide: Text.ElideRight
                                                maximumLineCount: 1
                                            }

                                            DankActionButton {
                                                buttonSize: 24
                                                iconName: root.isPrimingClaude ? "hourglass_top" : "bolt"
                                                iconColor: root.isPrimingClaude ? Theme.surfaceVariantText : Theme.primary
                                                tooltipText: root.isPrimingClaude ? "Starting a Claude session window..." : "Start a Claude session window now"
                                                anchors.verticalCenter: parent.verticalCenter
                                                visible: root.advancedDropdown && modelData.id === "claude" && root.enableClaudePrime
                                                enabled: !root.isPrimingClaude
                                                onClicked: root.primeClaude()
                                            }
                                        }
                                    }

                                    Column {
                                        width: parent.width
                                        height: root.providerQuotaHeight(modelData)
                                        spacing: Theme.spacingS

                                        Repeater {
                                            model: root.providerQuotaBuckets(modelData)

                                            QuotaBar {
                                                width: parent.width
                                                bucket: modelData
                                            }
                                        }

                                        NoticeRow {
                                            width: parent.width
                                            level: modelData.available && !modelData.error ? "info" : "error"
                                            text: modelData.available && !modelData.error
                                                    ? "Quota data unavailable"
                                                    : "Not signed in or CLI unavailable · sign in with the provider CLI, then Refresh"
                                            visible: root.providerQuotaBuckets(modelData).length === 0
                                        }
                                    }

                                    TokenHistoryRow {
                                        width: parent.width
                                        visible: root.advancedDropdown
                                        value: root.providerTokenBreakdown(modelData)
                                    }

                                    Item {
                                        width: parent.width
                                        height: 34
                                        visible: root.advancedDropdown && root.providerResets(modelData).length > 0

                                        DankIcon {
                                            name: "redeem"
                                            size: Theme.fontSizeMedium
                                            color: Theme.primary
                                            anchors.left: parent.left
                                            anchors.verticalCenter: parent.verticalCenter
                                        }

                                        Column {
                                            anchors.left: parent.left
                                            anchors.leftMargin: Theme.fontSizeMedium + Theme.spacingS
                                            anchors.right: parent.right
                                            anchors.verticalCenter: parent.verticalCenter
                                            spacing: 1

                                            StyledText {
                                                width: parent.width
                                                text: root.providerResetSummary(modelData)
                                                font.pixelSize: Theme.fontSizeSmall
                                                font.weight: Font.Medium
                                                color: Theme.primary
                                                elide: Text.ElideRight
                                                maximumLineCount: 1
                                            }

                                            StyledText {
                                                width: parent.width
                                                text: root.providerResetDetail(modelData)
                                                font.pixelSize: Theme.fontSizeSmall
                                                color: Theme.surfaceVariantText
                                                elide: Text.ElideRight
                                                maximumLineCount: 1
                                            }
                                        }
                                    }

                                    Column {
                                        width: parent.width
                                        spacing: Theme.spacingXS
                                        visible: modelData.id === "codex" && root.resetControlsVisible()

                                        DankToggle {
                                            id: codexAutoResetToggle
                                            width: parent.width
                                            text: codexResetProcess.running ? "Checking reset..." : !root.codexResetReady ? "Cancel auto reset" : "Auto-use one reset"
                                            checked: root.codexResetStatus.armed === true
                                            toggling: codexResetProcess.running
                                            enabled: !codexResetProcess.running && (root.codexResetReady || root.codexResetStatus.stateKnown === false)
                                            onClicked: root.runCodexReset(!root.codexResetReady || root.codexResetStatus.armed ? "disarm" : "arm")
                                            activeFocusOnTab: true
                                            Accessible.role: Accessible.CheckBox
                                            Accessible.name: text
                                            Accessible.checked: checked
                                            Accessible.onPressAction: handleClick()
                                            Accessible.onToggleAction: handleClick()
                                            Keys.onSpacePressed: handleClick()
                                            Keys.onReturnPressed: handleClick()

                                            Rectangle {
                                                anchors.fill: parent
                                                color: "transparent"
                                                radius: Theme.cornerRadius
                                                border.width: codexAutoResetToggle.activeFocus ? 2 : 0
                                                border.color: Theme.primary
                                            }
                                        }

                                        NoticeRow {
                                            width: parent.width
                                            level: root.codexResetStatus.error ? "error"
                                                    : root.codexResetStatus.armed === true ? "warning" : "info"
                                            text: root.codexResetDetailText()
                                        }

                                        StyledText {
                                            width: parent.width
                                            text: "Uses one reset at 99% general usage or 10 min before its expiry. Turns off after one attempt; DMS must be running."
                                            visible: root.advancedDropdown
                                            font.pixelSize: Theme.fontSizeSmall
                                            color: Theme.surfaceVariantText
                                            wrapMode: Text.WordWrap
                                        }
                                    }

                                    NoticeRow {
                                        width: parent.width
                                        level: "info"
                                        text: "Claude session scheduling is enabled · manage in Advanced or settings"
                                        visible: !root.advancedDropdown && modelData.id === "claude" && root.enableClaudePrime
                                    }

                                    Repeater {
                                        model: root.providerNotices(modelData)

                                        NoticeRow {
                                            width: parent.width
                                            level: modelData.level
                                            text: modelData.text
                                        }
                                    }
                                }
                            }
                        }
                    }

                    NoticeRow {
                        width: parent.width
                        level: "info"
                        text: "No providers enabled. Turn on Codex or Claude in plugin settings."
                        visible: root.visibleProviders().length === 0 && !root.isLoading
                    }

                    NoticeRow {
                        width: parent.width
                        level: "info"
                        text: "Loading usage..."
                        visible: root.isLoading && root.providers.length === 0
                    }

                    // More (Advanced): secondary sections as consistent disclosure rows.
                    Column {
                        width: parent.width
                        spacing: Theme.spacingXS
                        visible: (root.advancedDropdown && (root.showCodex || root.showClaude))
                                || (root.historyExplanationGroup !== null
                                    && root.historyExplanationLocation !== "prompt")

                        SectionHeader {
                            width: parent.width
                            title: "Reset history"
                            badge: root.visibleHistory().length ? "" + root.visibleHistory().length : ""
                            expanded: root.historyOpen || (root.historyExplanationGroup !== null
                                    && root.historyExplanationLocation !== "prompt")
                            onClicked: {
                                if (root.historyExplanationGroup && root.historyExplanationLocation !== "prompt")
                                    root.historyOpen = true
                                else root.historyOpen = !root.historyOpen
                            }
                        }

                        Column {
                            width: parent.width
                            spacing: Theme.spacingS
                            visible: root.historyOpen || (root.historyExplanationGroup !== null
                                    && root.historyExplanationLocation !== "prompt")

                            NoticeRow {
                                width: parent.width
                                level: "warning"
                                text: root.historyError
                                visible: root.historyError !== ""
                            }

                            StyledText {
                                width: parent.width
                                text: root.visibleHistory().length === 0
                                        ? "No reset changes recorded yet. History starts with observed usage; it cannot reconstruct earlier resets."
                                        : "Latest 8 events · up to 30 days retained. Times show when changes were observed, not necessarily when they happened."
                                textFormat: Text.PlainText
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                                wrapMode: Text.WordWrap
                            }

                            Repeater {
                                model: root.historyGroupsForDisplay()

                                StyledRect {
                                    id: historyGroupCard
                                    property var historyGroup: modelData
                                    width: parent.width
                                    height: historyContent.implicitHeight + 2 * Theme.spacingM
                                    color: Theme.surfaceContainerHigh
                                    radius: Theme.cornerRadius

                                    Column {
                                        id: historyContent
                                        x: Theme.spacingM
                                        y: Theme.spacingM
                                        width: parent.width - 2 * Theme.spacingM
                                        spacing: Theme.spacingXS

                                        Repeater {
                                            model: historyGroupCard.historyGroup.events

                                            Column {
                                                width: parent.width
                                                spacing: 2

                                                StyledText {
                                                    width: parent.width
                                                    text: root.historyEventTitle(modelData)
                                                    textFormat: Text.PlainText
                                                    font.pixelSize: Theme.fontSizeSmall
                                                    font.weight: Font.Medium
                                                    color: Theme.surfaceText
                                                    wrapMode: Text.WordWrap
                                                }

                                                StyledText {
                                                    width: parent.width
                                                    text: root.historyEventDetail(modelData)
                                                    textFormat: Text.PlainText
                                                    font.pixelSize: Theme.fontSizeSmall
                                                    color: Theme.surfaceVariantText
                                                    wrapMode: Text.WordWrap
                                                }
                                            }
                                        }

                                        StyledText {
                                            width: parent.width
                                            text: "User reported · "
                                                    + root.historyExplanationText(historyGroupCard.historyGroup.explanation)
                                            textFormat: Text.PlainText
                                            font.pixelSize: Theme.fontSizeSmall
                                            font.weight: Font.Medium
                                            color: Theme.primary
                                            wrapMode: Text.WordWrap
                                            visible: root.hasHistoryExplanation(historyGroupCard.historyGroup)
                                        }

                                        StyledText {
                                            width: parent.width
                                            text: historyGroupCard.historyGroup.explanation
                                                    ? historyGroupCard.historyGroup.explanation.note || "" : ""
                                            textFormat: Text.PlainText
                                            font.pixelSize: Theme.fontSizeSmall
                                            color: Theme.surfaceText
                                            wrapMode: Text.WordWrap
                                            visible: text !== ""
                                        }

                                        StyledText {
                                            width: parent.width
                                            property var publicContext: root.matchingPublicAnnouncement(historyGroupCard.historyGroup)
                                            text: publicContext ? (publicContext.confidence === "verified"
                                                    ? "Likely linked to announced reset"
                                                    : "Public reset reported near this observation")
                                                    + " · via TokenResets · account eligibility unverified\n" + root.announcementSummary(publicContext) : ""
                                            textFormat: Text.PlainText
                                            color: Theme.surfaceVariantText
                                            font.pixelSize: Theme.fontSizeSmall
                                            wrapMode: Text.WordWrap
                                            visible: publicContext !== null
                                        }

                                        Flow {
                                            width: parent.width
                                            spacing: Theme.spacingXS

                                            CompactAction {
                                                property var publicContext: root.matchingPublicAnnouncement(historyGroupCard.historyGroup)
                                                text: "View public context"
                                                visible: publicContext !== null
                                                onClicked: if (publicContext) Qt.openUrlExternally(publicContext.url)
                                            }

                                            CompactAction {
                                                text: root.hasHistoryExplanation(historyGroupCard.historyGroup)
                                                        ? "Edit explanation" : "Explain"
                                                visible: historyGroupCard.historyGroup.explainable
                                                        && root.historyExplanationLocation !== historyGroupCard.historyGroup.key
                                                enabled: !historyExplanationProcess.running && !usageProcess.running
                                                        && root.historyExplanationGroup === null
                                                onClicked: root.beginHistoryExplanation(historyGroupCard.historyGroup,
                                                        historyGroupCard.historyGroup.key, true)
                                            }
                                        }

                                        HistoryExplanationEditor {
                                            width: parent.width
                                            visible: root.historyExplanationGroup !== null
                                                    && root.historyExplanationLocation === historyGroupCard.historyGroup.key
                                        }
                                    }
                                }
                            }
                        }

                        SectionHeader {
                            width: parent.width
                            title: "Public reset announcements"
                            badge: root.visibleAnnouncements().length ? "" + root.visibleAnnouncements().length : ""
                            expanded: root.announcementsOpen
                            visible: root.advancedDropdown && root.publicResetAnnouncements
                            onClicked: root.announcementsOpen = !root.announcementsOpen
                        }

                        Column {
                            width: parent.width
                            spacing: Theme.spacingS
                            visible: root.advancedDropdown && root.publicResetAnnouncements && root.announcementsOpen

                            StyledText {
                                width: parent.width
                                text: root.announcementsMessage
                                textFormat: Text.PlainText
                                wrapMode: Text.WordWrap
                                color: Theme.surfaceVariantText
                                font.pixelSize: Theme.fontSizeSmall
                                visible: text !== ""
                            }

                            Repeater {
                                model: root.visibleAnnouncements()
                                delegate: StyledRect {
                                    required property var modelData
                                    width: parent.width
                                    height: announcementContent.implicitHeight + 2 * Theme.spacingM
                                    radius: Theme.cornerRadius
                                    color: Theme.surfaceContainerHigh

                                    Column {
                                        id: announcementContent
                                        x: Theme.spacingM
                                        y: Theme.spacingM
                                        width: parent.width - 2 * Theme.spacingM
                                        spacing: Theme.spacingXS

                                        StyledText {
                                            width: parent.width
                                            text: modelData.title + " · " + modelData.confidence + " by TokenResets"
                                            textFormat: Text.PlainText
                                            wrapMode: Text.WordWrap
                                            color: Theme.primary
                                            font.pixelSize: Theme.fontSizeSmall
                                            font.weight: Font.Medium
                                        }

                                        StyledText {
                                            width: parent.width
                                            text: root.announcementSummary(modelData) + "\nScope: " + modelData.scopeLabel
                                                    + "\nAnnounced " + root.formatShortDateTime(modelData.announcedAt)
                                                    + (modelData.expectedBy ? " · expected by " + root.formatShortDateTime(modelData.expectedBy) : "")
                                            textFormat: Text.PlainText
                                            wrapMode: Text.WordWrap
                                            color: Theme.surfaceVariantText
                                            font.pixelSize: Theme.fontSizeSmall
                                        }

                                        CompactAction {
                                            text: "View source and evidence"
                                            onClicked: Qt.openUrlExternally(modelData.url)
                                        }
                                    }
                                }
                            }
                        }

                        SectionHeader {
                            width: parent.width
                            title: "Bar controls"
                            expanded: root.quickControlsOpen
                            visible: root.advancedDropdown
                            onClicked: root.quickControlsOpen = !root.quickControlsOpen
                        }

                        Column {
                            width: parent.width
                            spacing: Theme.spacingXS
                            visible: root.advancedDropdown && root.quickControlsOpen

                            StyledText {
                                text: "Top bar"
                                color: Theme.surfaceVariantText
                                font.pixelSize: Theme.fontSizeSmall
                            }
                            Flow {
                                width: parent.width
                                spacing: Theme.spacingXS
                                QuickToggle { text: "Compact"; checked: root.compactPill; onClicked: root.setQuickSetting("compactPill", !root.compactPill) }
                                QuickToggle { text: "Logos"; checked: root.barShowProviderLogos; onClicked: root.setQuickSetting("barShowProviderLogos", !root.barShowProviderLogos) }
                                QuickToggle { text: "Plugin icon"; checked: root.barShowPluginIcon; onClicked: root.setQuickSetting("barShowPluginIcon", !root.barShowPluginIcon) }
                            }
                            StyledText {
                                text: "Claude in the top bar"
                                color: Theme.surfaceVariantText
                                font.pixelSize: Theme.fontSizeSmall
                            }
                            Flow {
                                width: parent.width
                                spacing: Theme.spacingXS
                                QuickToggle { text: "Session"; checked: root.barShowClaudeSession; onClicked: root.setQuickSetting("barShowClaudeSession", !root.barShowClaudeSession) }
                                Repeater {
                                    model: root.claudeWeeklyBarChoices()
                                    delegate: QuickToggle {
                                        required property var modelData
                                        text: modelData.id === "general-weekly" ? "Weekly (all models)" : modelData.label
                                        checked: root.claudeBucketShownInBar(modelData)
                                        onClicked: root.setClaudeWeeklyBarBucket(modelData, !checked)
                                    }
                                }
                                QuickToggle { text: "Credits"; checked: root.barShowClaudeCredits; onClicked: root.setQuickSetting("barShowClaudeCredits", !root.barShowClaudeCredits) }
                            }
                        }

                        SectionHeader {
                            width: parent.width
                            title: "Diagnostics"
                            expanded: root.diagnosticsOpen
                            visible: root.advancedDropdown
                            onClicked: {
                                root.diagnosticsOpen = !root.diagnosticsOpen
                                if (root.diagnosticsOpen) root.refreshDiagnostics()
                            }
                        }

                        DiagnosticsPanel {
                            width: parent.width
                            visible: root.advancedDropdown && root.diagnosticsOpen
                        }
                    }
                }
            }
        }
    }

    component DiagnosticsPanel: Column {
        spacing: Theme.spacingS

        StyledText {
            width: parent.width
            text: root.diagnosticStatus
            textFormat: Text.PlainText
            wrapMode: Text.WordWrap
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
            visible: text !== ""
        }

        StyledRect {
            width: parent.width
            height: Math.min(220, diagnosticPreview.contentHeight) + 2 * Theme.spacingS
            radius: Theme.cornerRadius
            color: Theme.surfaceContainerHigh
            visible: root.diagnosticReport !== ""

            Flickable {
                x: Theme.spacingS
                y: Theme.spacingS
                width: parent.width - 2 * Theme.spacingS
                height: Math.min(220, diagnosticPreview.contentHeight)
                contentHeight: diagnosticPreview.contentHeight
                clip: true

                TextEdit {
                    id: diagnosticPreview
                    width: parent.width
                    text: root.diagnosticReport
                    textFormat: TextEdit.PlainText
                    readOnly: true
                    selectByMouse: true
                    activeFocusOnTab: true
                    wrapMode: TextEdit.Wrap
                    color: Theme.surfaceText
                    font.pixelSize: Theme.fontSizeSmall
                    font.family: "monospace"
                    Accessible.name: "Diagnostic report preview"
                }
            }
        }

        Flow {
            width: parent.width
            spacing: Theme.spacingXS
            CompactAction {
                text: "Refresh report"
                enabled: !diagnosticProcess.running
                onClicked: root.refreshDiagnostics()
            }
            CompactAction {
                text: "Copy report"
                enabled: root.diagnosticReport !== "" && !diagnosticProcess.running
                onClicked: {
                    diagnosticPreview.selectAll()
                    diagnosticPreview.copy()
                    diagnosticPreview.deselect()
                    root.diagnosticStatus = "Report copied to clipboard. Nothing was uploaded."
                }
            }
        }
    }

    component HistoryExplanationEditor: StyledRect {
        id: explanationEditor
        height: explanationEditorContent.implicitHeight + 2 * Theme.spacingS
        radius: Theme.cornerRadius
        color: "transparent"
        border.width: 1
        border.color: Theme.outlineVariant

        Column {
            id: explanationEditorContent
            x: Theme.spacingS
            y: Theme.spacingS
            width: parent.width - 2 * Theme.spacingS
            spacing: Theme.spacingXS

            StyledText {
                width: parent.width
                text: "What changed?"
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeMedium
                font.weight: Font.Medium
                color: Theme.surfaceText
            }

            StyledText {
                width: parent.width
                text: root.historyGroupSummary(root.historyExplanationGroup)
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
                wrapMode: Text.WordWrap
            }

            StyledText {
                width: parent.width
                text: "Applies to " + (root.historyExplanationGroup
                        ? root.historyExplanationGroup.eligibleCount : 0) + " changes observed together"
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
                wrapMode: Text.WordWrap
            }

            Flow {
                width: parent.width
                spacing: Theme.spacingXS

                Repeater {
                    model: root.historyExplanationReasonChoices(root.historyExplanationGroup)

                    QuickToggle {
                        text: modelData.label
                        checked: root.historyExplanationReason === modelData.key
                        enabled: !historyExplanationProcess.running
                        Accessible.name: text + (checked ? "; selected" : "")
                        onClicked: {
                            root.historyExplanationReason = modelData.key
                            root.historyExplanationError = ""
                        }
                    }
                }
            }

            StyledText {
                width: parent.width
                text: "Optional local note"
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
            }

            Rectangle {
                width: parent.width
                height: Math.max(72, historyNoteInput.contentHeight + 2 * Theme.spacingXS)
                radius: Theme.cornerRadius
                color: Theme.withAlpha(Theme.surfaceVariantText, 0.12)
                border.width: historyNoteInput.activeFocus ? 2 : 1
                border.color: historyNoteInput.activeFocus ? Theme.primary : Theme.outlineVariant

                TextEdit {
                    id: historyNoteInput
                    anchors.fill: parent
                    anchors.margins: Theme.spacingXS
                    color: Theme.surfaceText
                    font.pixelSize: Theme.fontSizeSmall
                    textFormat: TextEdit.PlainText
                    wrapMode: TextEdit.Wrap
                    selectByMouse: true
                    activeFocusOnTab: true
                    enabled: !historyExplanationProcess.running
                    Accessible.name: "Optional local explanation note"

                    Component.onCompleted: text = root.historyExplanationNote
                    onVisibleChanged: if (visible) text = root.historyExplanationNote
                    onTextChanged: {
                        var limited = root.truncateHistoryNote(text, 280)
                        if (limited !== text) {
                            var oldPosition = cursorPosition
                            text = limited
                            cursorPosition = Math.min(oldPosition, text.length)
                        }
                        root.historyExplanationNote = text
                    }

                    StyledText {
                        anchors.left: parent.left
                        anchors.top: parent.top
                        text: "Add context (optional)"
                        textFormat: Text.PlainText
                        font.pixelSize: Theme.fontSizeSmall
                        color: Theme.surfaceVariantText
                        visible: historyNoteInput.text === "" && !historyNoteInput.activeFocus
                    }
                }
            }

            StyledText {
                width: parent.width
                text: root.historyNoteRuneLength(root.historyExplanationNote) + "/280 · Stored locally with history. Do not include prompts, account details, or other sensitive information."
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
                wrapMode: Text.WordWrap
            }

            StyledText {
                width: parent.width
                text: root.historyExplanationError
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.error
                wrapMode: Text.WordWrap
                visible: text !== ""
            }

            Flow {
                width: parent.width
                spacing: Theme.spacingXS

                CompactAction {
                    text: historyExplanationProcess.running ? "Saving..."
                            : root.hasHistoryExplanation(root.historyExplanationGroup)
                                ? "Save changes" : "Save explanation"
                    primary: true
                    enabled: !historyExplanationProcess.running && !usageProcess.running
                            && root.historyExplanationChoiceAllowed(root.historyExplanationGroup,
                                root.historyExplanationReason)
                    onClicked: root.submitHistoryExplanation("")
                }

                CompactAction {
                    text: "Dismiss"
                    visible: root.historyExplanationChoiceAllowed(root.historyExplanationGroup, "dismissed")
                    enabled: !historyExplanationProcess.running && !usageProcess.running
                    onClicked: root.submitHistoryExplanation("dismissed")
                }

                CompactAction {
                    text: "Cancel"
                    enabled: !historyExplanationProcess.running
                    onClicked: root.cancelHistoryExplanation()
                }
            }
        }
    }

    component TokenHistoryRow: StyledRect {
        id: tokenRow
        property string value: ""
        property bool selectorOpen: false
        height: selectorOpen ? 36 + tokenRangeFlow.implicitHeight + Theme.spacingXS : 32
        radius: Theme.cornerRadius
        color: Theme.withAlpha(Theme.surfaceVariant, 0.35)
        border.width: activeFocus ? 2 : 1
        border.color: activeFocus ? Theme.primary : Theme.outlineVariant
        activeFocusOnTab: true
        Accessible.role: Accessible.Button
        Accessible.name: root.tokenHistoryLabel() + ": " + value
        Accessible.description: "Open token history range selector"
        Accessible.onPressAction: tokenRow.selectorOpen = !tokenRow.selectorOpen
        Keys.onSpacePressed: tokenRow.selectorOpen = !tokenRow.selectorOpen
        Keys.onReturnPressed: tokenRow.selectorOpen = !tokenRow.selectorOpen

        Item {
            id: tokenRowHeader
            width: parent.width
            height: 32
            anchors.top: parent.top
        }

        DankIcon {
            id: tokenSwitchIcon
            name: "expand_more"
            size: 16
            color: Theme.primary
            rotation: tokenRow.selectorOpen ? 180 : 0
            anchors.left: parent.left
            anchors.leftMargin: Theme.spacingS
            anchors.verticalCenter: tokenRowHeader.verticalCenter

            Behavior on rotation {
                NumberAnimation { duration: Theme.shortDuration; easing.type: Theme.standardEasing }
            }
        }
        StyledText {
            text: root.tokenHistoryLabel()
            anchors.left: tokenSwitchIcon.right
            anchors.leftMargin: Theme.spacingXS
            anchors.right: tokenValue.left
            anchors.rightMargin: Theme.spacingS
            anchors.verticalCenter: tokenRowHeader.verticalCenter
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
            elide: Text.ElideRight
        }
        StyledText {
            id: tokenValue
            text: tokenRow.value
            width: Math.min(implicitWidth, parent.width * 0.55)
            anchors.right: parent.right
            anchors.rightMargin: Theme.spacingS
            anchors.verticalCenter: tokenRowHeader.verticalCenter
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Medium
            color: Theme.surfaceText
            elide: Text.ElideRight
            horizontalAlignment: Text.AlignRight
        }
        MouseArea {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            height: 32
            cursorShape: Qt.PointingHandCursor
            onClicked: tokenRow.selectorOpen = !tokenRow.selectorOpen
        }

        Flow {
            id: tokenRangeFlow
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.leftMargin: Theme.spacingXS
            anchors.rightMargin: Theme.spacingXS
            anchors.top: parent.top
            anchors.topMargin: 36
            spacing: Theme.spacingXS
            visible: tokenRow.selectorOpen

            Repeater {
                model: root.tokenHistoryRangeChoices()

                QuickToggle {
                    text: modelData.label
                    checked: root.tokenHistoryRange === modelData.key
                    onClicked: {
                        root.selectTokenHistoryRange(modelData.key)
                        tokenRow.selectorOpen = false
                    }
                }
            }
        }
    }

    Timer {
        interval: 15000
        running: root.clearTrackingConfirm
        onTriggered: root.clearTrackingConfirm = false
    }

    component TrackingPanel: StyledRect {
        id: trackingPanel
        readonly property bool controlsReady: root.trackingStatus.known === true
                && !trackingProcess.running && !usageProcess.running
        height: trackingPanelContent.implicitHeight + 2 * Theme.spacingM
        radius: Theme.cornerRadius
        color: Theme.surfaceContainerHigh

        Column {
            id: trackingPanelContent
            x: Theme.spacingM
            y: Theme.spacingM
            width: parent.width - 2 * Theme.spacingM
            spacing: Theme.spacingXS

            StyledText {
                width: parent.width
                text: trackingProcess.running ? "Updating tracked total..." : root.trackingStateText()
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.Medium
                color: root.trackingStatus.known === true ? Theme.surfaceText : Theme.error
                wrapMode: Text.WordWrap
            }

            StyledText {
                width: parent.width
                text: root.trackingDetailText()
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
                wrapMode: Text.WordWrap
            }

            Flow {
                width: parent.width
                spacing: Theme.spacingXS

                CompactAction {
                    text: root.trackingStatus.enabled === true ? "Pause" : "Enable tracking"
                    enabled: trackingPanel.controlsReady
                    onClicked: root.runTracking(root.trackingStatus.enabled === true ? "pause" : "enable")
                }

                CompactAction {
                    text: root.clearTrackingConfirm ? "Confirm clear" : "Clear tracked data"
                    enabled: trackingPanel.controlsReady && !!root.trackingStatus.startedAt
                    warning: root.clearTrackingConfirm
                    onClicked: {
                        if (root.clearTrackingConfirm) root.runTracking("clear")
                        else root.clearTrackingConfirm = true
                    }
                }

                CompactAction {
                    text: "Cancel"
                    visible: root.clearTrackingConfirm
                    enabled: !trackingProcess.running
                    onClicked: root.clearTrackingConfirm = false
                }
            }

            NoticeRow {
                width: parent.width
                level: "warning"
                text: "Clear removes tracked totals and disables tracking. Provider transcripts and reset history are unchanged."
                visible: root.clearTrackingConfirm
            }
        }
    }

    // A compact, wrapped status line with a semantic icon. Level: info | warning | error.
    component NoticeRow: Item {
        id: noticeRow
        property string text: ""
        property string level: "info"
        implicitHeight: Math.max(noticeText.implicitHeight, noticeIcon.height)

        DankIcon {
            id: noticeIcon
            name: root.noticeIcon(noticeRow.level)
            size: Theme.fontSizeMedium
            color: root.noticeColor(noticeRow.level)
            anchors.left: parent.left
            anchors.top: parent.top
        }

        StyledText {
            id: noticeText
            anchors.left: noticeIcon.right
            anchors.leftMargin: Theme.spacingXS
            anchors.right: parent.right
            anchors.top: parent.top
            text: noticeRow.text
            textFormat: Text.PlainText
            font.pixelSize: Theme.fontSizeSmall
            color: noticeRow.level === "info" ? Theme.surfaceVariantText : root.noticeColor(noticeRow.level)
            wrapMode: Text.WordWrap
            maximumLineCount: 4
            elide: Text.ElideRight
        }
    }

    // A bordered card for failures that need attention before the quotas.
    component NoticeCard: StyledRect {
        id: noticeCard
        property string text: ""
        property string level: "error"
        height: noticeCardRow.implicitHeight + 2 * Theme.spacingM
        radius: Theme.cornerRadius
        color: Theme.withAlpha(root.noticeColor(level), 0.10)
        border.width: 1
        border.color: root.noticeColor(level)

        NoticeRow {
            id: noticeCardRow
            x: Theme.spacingM
            y: Theme.spacingM
            width: parent.width - 2 * Theme.spacingM
            level: noticeCard.level
            text: noticeCard.text
        }
    }

    // Disclosure row for secondary sections.
    component SectionHeader: Item {
        id: sectionHeader
        property string title: ""
        property string badge: ""
        property bool expanded: false
        signal clicked()
        height: 36
        activeFocusOnTab: true
        Accessible.role: Accessible.Button
        Accessible.name: title + (badge !== "" ? " (" + badge + ")" : "") + (expanded ? "; expanded" : "; collapsed")
        Accessible.onPressAction: clicked()
        Keys.onSpacePressed: clicked()
        Keys.onReturnPressed: clicked()

        StyledRect {
            anchors.fill: parent
            radius: Theme.cornerRadius
            color: sectionMouse.containsMouse ? Theme.surfaceHover : "transparent"
            border.width: sectionHeader.activeFocus ? 2 : 0
            border.color: Theme.primary
        }

        DankIcon {
            id: sectionChevron
            name: "expand_more"
            size: 18
            color: Theme.surfaceVariantText
            rotation: sectionHeader.expanded ? 180 : 0
            anchors.left: parent.left
            anchors.leftMargin: Theme.spacingXS
            anchors.verticalCenter: parent.verticalCenter

            Behavior on rotation {
                NumberAnimation { duration: Theme.shortDuration; easing.type: Theme.standardEasing }
            }
        }

        StyledText {
            anchors.left: sectionChevron.right
            anchors.leftMargin: Theme.spacingXS
            anchors.right: sectionBadge.left
            anchors.rightMargin: Theme.spacingS
            anchors.verticalCenter: parent.verticalCenter
            text: sectionHeader.title
            font.pixelSize: Theme.fontSizeMedium
            font.weight: Font.Medium
            color: Theme.surfaceText
            elide: Text.ElideRight
        }

        StyledRect {
            id: sectionBadge
            width: visible ? sectionBadgeText.implicitWidth + Theme.spacingS * 2 : 0
            height: 20
            radius: 10
            color: Theme.withAlpha(Theme.primary, 0.18)
            visible: sectionHeader.badge !== ""
            anchors.right: parent.right
            anchors.rightMargin: Theme.spacingXS
            anchors.verticalCenter: parent.verticalCenter

            StyledText {
                id: sectionBadgeText
                anchors.centerIn: parent
                text: sectionHeader.badge
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.Medium
                color: Theme.primary
            }
        }

        MouseArea {
            id: sectionMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: sectionHeader.clicked()
        }
    }

    // Two-state segmented switch. Both labels are always visible; the active
    // one is highlighted. Clicking the inactive segment emits clicked().
    component SegmentSwitch: StyledRect {
        id: segmentSwitch
        property string text: ""
        property string offLabel: ""
        property string onLabel: ""
        property bool checked: false
        signal clicked()
        readonly property int segmentPadding: Theme.spacingM
        readonly property int offWidth: offMetrics.width + segmentPadding * 2
        readonly property int onWidth: onMetrics.width + segmentPadding * 2
        width: offWidth + onWidth + 4
        height: 30
        radius: Theme.cornerRadius
        color: Theme.surfaceContainerHigh
        activeFocusOnTab: true
        border.width: activeFocus ? 2 : 0
        border.color: Theme.primary
        Accessible.role: Accessible.CheckBox
        Accessible.name: text
        Accessible.checked: checked
        Accessible.onToggleAction: clicked()
        Accessible.onPressAction: clicked()
        Keys.onSpacePressed: clicked()
        Keys.onReturnPressed: clicked()

        TextMetrics { id: offMetrics; text: segmentSwitch.offLabel; font.pixelSize: Theme.fontSizeSmall; font.weight: Font.Medium }
        TextMetrics { id: onMetrics; text: segmentSwitch.onLabel; font.pixelSize: Theme.fontSizeSmall; font.weight: Font.Medium }

        StyledRect {
            id: segmentThumb
            x: segmentSwitch.checked ? 2 + segmentSwitch.offWidth : 2
            y: 2
            width: segmentSwitch.checked ? segmentSwitch.onWidth : segmentSwitch.offWidth
            height: parent.height - 4
            radius: Math.max(0, Theme.cornerRadius - 2)
            color: Theme.primary

            Behavior on x { NumberAnimation { duration: Theme.shortDuration; easing.type: Theme.standardEasing } }
            Behavior on width { NumberAnimation { duration: Theme.shortDuration; easing.type: Theme.standardEasing } }
        }

        StyledText {
            x: 2
            width: segmentSwitch.offWidth
            height: parent.height
            text: segmentSwitch.offLabel
            horizontalAlignment: Text.AlignHCenter
            verticalAlignment: Text.AlignVCenter
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Medium
            color: segmentSwitch.checked ? Theme.surfaceVariantText : Theme.primaryText
        }

        StyledText {
            x: 2 + segmentSwitch.offWidth
            width: segmentSwitch.onWidth
            height: parent.height
            text: segmentSwitch.onLabel
            horizontalAlignment: Text.AlignHCenter
            verticalAlignment: Text.AlignVCenter
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Medium
            color: segmentSwitch.checked ? Theme.primaryText : Theme.surfaceVariantText
        }

        MouseArea {
            x: 0
            width: 2 + segmentSwitch.offWidth
            height: parent.height
            cursorShape: segmentSwitch.checked ? Qt.PointingHandCursor : Qt.ArrowCursor
            onClicked: if (segmentSwitch.checked) segmentSwitch.clicked()
        }

        MouseArea {
            x: 2 + segmentSwitch.offWidth
            width: segmentSwitch.onWidth + 2
            height: parent.height
            cursorShape: segmentSwitch.checked ? Qt.ArrowCursor : Qt.PointingHandCursor
            onClicked: if (!segmentSwitch.checked) segmentSwitch.clicked()
        }
    }

    component CompactAction: StyledRect {
        id: compactAction
        property string text: ""
        property bool warning: false
        property bool primary: false
        signal clicked()
        width: compactActionLabel.implicitWidth + Theme.spacingM * 2
        height: 30
        radius: Theme.cornerRadius
        color: warning ? Theme.withAlpha(Theme.error, 0.16)
                : primary ? Theme.primary : Theme.surfaceContainerHigh
        opacity: enabled ? 1 : 0.5
        activeFocusOnTab: enabled && visible
        border.width: activeFocus ? 2 : 1
        border.color: activeFocus ? Theme.primary : warning ? Theme.error : primary ? "transparent" : Theme.outlineVariant
        Accessible.role: Accessible.Button
        Accessible.name: text
        Accessible.onPressAction: if (enabled) clicked()
        Keys.onSpacePressed: if (enabled) clicked()
        Keys.onReturnPressed: if (enabled) clicked()

        StyledText {
            id: compactActionLabel
            anchors.centerIn: parent
            text: compactAction.text
            color: compactAction.warning ? Theme.error : compactAction.primary ? Theme.primaryText : Theme.surfaceText
            font.pixelSize: Theme.fontSizeSmall
            font.weight: compactAction.primary || compactAction.warning ? Font.Medium : Font.Normal
        }

        MouseArea {
            anchors.fill: parent
            enabled: compactAction.enabled
            cursorShape: enabled ? Qt.PointingHandCursor : Qt.ArrowCursor
            onClicked: compactAction.clicked()
        }
    }

    component QuickToggle: StyledRect {
        id: quickToggle
        property string text: ""
        property bool checked: false
        signal clicked()
        width: quickLabel.implicitWidth + Theme.spacingM * 2
        height: 30
        radius: Theme.cornerRadius
        color: checked ? Theme.primary : Theme.surfaceContainerHigh
        activeFocusOnTab: true
        border.width: activeFocus ? 2 : checked ? 0 : 1
        border.color: activeFocus ? Theme.surfaceText : Theme.outlineVariant
        Accessible.role: Accessible.CheckBox
        Accessible.name: text
        Accessible.checked: checked
        Accessible.onToggleAction: clicked()
        Accessible.onPressAction: clicked()
        Keys.onSpacePressed: clicked()
        Keys.onReturnPressed: clicked()
        StyledText {
            id: quickLabel
            anchors.centerIn: parent
            text: quickToggle.text
            color: quickToggle.checked ? Theme.primaryText : Theme.surfaceText
            font.pixelSize: Theme.fontSizeSmall
            font.weight: quickToggle.checked ? Font.Medium : Font.Normal
        }
        MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: quickToggle.clicked()
        }
    }

    // One quota: label with an inline countdown, a severity-colored value,
    // the allowance bar, and (Advanced) a thin window-time bar beneath it.
    component QuotaBar: Item {
        id: quotaBar
        property var bucket: null
        property real timeProgress: bucket && bucket.kind !== "credits"
                ? root.resetTimeProgress(bucket.allowance, root.resetClock, root.showUsed) : -1
        readonly property bool showTime: root.advancedDropdown && timeProgress >= 0
        readonly property color severityColor: root.allowanceColor(bucket ? bucket.allowance : null)
        height: root.quotaRowHeight(bucket)

        HoverHandler { id: resetHover }
        Controls.ToolTip {
            id: resetTooltip
            visible: resetHover.hovered && !!root.resetTiming(bucket ? bucket.allowance : null, root.resetClock)
            delay: 400
            text: bucket && bucket.allowance
                    ? "Reset: " + new Date(bucket.allowance.resetAt).toLocaleString()
                        + (quotaBar.showTime
                           ? "\nThin bar: window time " + (root.showUsed ? "elapsed" : "remaining")
                             + " (not quota usage)" : "") : ""
            contentItem: StyledText {
                text: resetTooltip.text
                textFormat: Text.PlainText
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceText
            }
            background: StyledRect {
                color: Theme.surfaceContainerHigh
                radius: Theme.cornerRadius
                border.color: Theme.outlineVariant
                border.width: 1
            }
        }

        StyledText {
            id: quotaBarLabel
            text: bucket ? bucket.label : "Quota"
            anchors.left: parent.left
            anchors.top: parent.top
            width: Math.min(implicitWidth, Math.max(80, (parent.width - quotaBarValue.width - Theme.spacingS) * 0.6))
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Medium
            color: Theme.surfaceText
            elide: Text.ElideRight
            maximumLineCount: 1
        }

        StyledText {
            id: quotaBarDetail
            text: root.quotaDetail(bucket)
            anchors.left: quotaBarLabel.right
            anchors.leftMargin: Theme.spacingXS
            anchors.right: quotaBarValue.left
            anchors.rightMargin: Theme.spacingS
            anchors.baseline: quotaBarLabel.baseline
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
            elide: Text.ElideRight
            maximumLineCount: 1
            visible: text !== "" && width > 24
        }

        StyledText {
            id: quotaBarValue
            text: root.quotaValue(bucket)
            anchors.right: parent.right
            anchors.top: parent.top
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Bold
            color: quotaBar.severityColor
            elide: Text.ElideRight
            maximumLineCount: 1
        }

        StyledRect {
            id: quotaTrack
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            anchors.topMargin: 20
            height: 6
            radius: 3
            color: Theme.withAlpha(Theme.surfaceVariantText, 0.18)

            StyledRect {
                width: parent.width * root.quotaProgress(bucket) / 100
                height: parent.height
                radius: parent.radius
                color: quotaBar.severityColor

                Behavior on width {
                    NumberAnimation { duration: Theme.mediumDuration; easing.type: Theme.standardEasing }
                }
            }
        }

        StyledRect {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: quotaTrack.bottom
            anchors.topMargin: 3
            height: 2
            radius: 1
            visible: quotaBar.showTime
            color: Theme.withAlpha(Theme.surfaceVariantText, 0.12)
            StyledRect {
                width: parent.width * Math.max(0, quotaBar.timeProgress)
                height: parent.height
                radius: parent.radius
                color: Theme.surfaceVariantText
                opacity: 0.6
            }
        }
    }

    component ProviderLogo: Item {
        id: providerLogoRoot

        property var provider: null
        property int size: 24
        readonly property string logoPath: provider && provider.id === "claude"
            ? "m4.7144 15.9555 4.7174-2.6471.079-.2307-.079-.1275h-.2307l-.7893-.0486-2.6956-.0729-2.3375-.0971-2.2646-.1214-.5707-.1215-.5343-.7042.0546-.3522.4797-.3218.686.0608 1.5179.1032 2.2767.1578 1.6514.0972 2.4468.255h.3886l.0546-.1579-.1336-.0971-.1032-.0972L6.973 9.8356l-2.55-1.6879-1.3356-.9714-.7225-.4918-.3643-.4614-.1578-1.0078.6557-.7225.8803.0607.2246.0607.8925.686 1.9064 1.4754 2.4893 1.8336.3643.3035.1457-.1032.0182-.0728-.164-.2733-1.3539-2.4467-1.445-2.4893-.6435-1.032-.17-.6194c-.0607-.255-.1032-.4674-.1032-.7285L6.287.1335 6.6997 0l.9957.1336.419.3642.6192 1.4147 1.0018 2.2282 1.5543 3.0296.4553.8985.2429.8318.091.255h.1579v-.1457l.1275-1.706.2368-2.0947.2307-2.6957.0789-.7589.3764-.9107.7468-.4918.5828.2793.4797.686-.0668.4433-.2853 1.8517-.5586 2.9021-.3643 1.9429h.2125l.2429-.2429.9835-1.3053 1.6514-2.0643.7286-.8196.85-.9046.5464-.4311h1.0321l.759 1.1293-.34 1.1657-1.0625 1.3478-.8804 1.1414-1.2628 1.7-.7893 1.36.0729.1093.1882-.0183 2.8535-.607 1.5421-.2794 1.8396-.3157.8318.3886.091.3946-.3278.8075-1.967.4857-2.3072.4614-3.4364.8136-.0425.0304.0486.0607 1.5482.1457.6618.0364h1.621l3.0175.2247.7892.522.4736.6376-.079.4857-1.2142.6193-1.6393-.3886-3.825-.9107-1.3113-.3279h-.1822v.1093l1.0929 1.0686 2.0035 1.8092 2.5075 2.3314.1275.5768-.3218.4554-.34-.0486-2.2039-1.6575-.85-.7468-1.9246-1.621h-.1275v.17l.4432.6496 2.3436 3.5214.1214 1.0807-.17.3521-.6071.2125-.6679-.1214-1.3721-1.9246L14.38 17.959l-1.1414-1.9428-.1397.079-.674 7.2552-.3156.3703-.7286.2793-.6071-.4614-.3218-.7468.3218-1.4753.3886-1.9246.3157-1.53.2853-1.9004.17-.6314-.0121-.0425-.1397.0182-1.4328 1.9672-2.1796 2.9446-1.7243 1.8456-.4128.164-.7164-.3704.0667-.6618.4008-.5889 2.386-3.0357 1.4389-1.882.929-1.0868-.0062-.1579h-.0546l-6.3385 4.1164-1.1293.1457-.4857-.4554.0608-.7467.2307-.2429 1.9064-1.3114Z"
            : "M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z"

        implicitWidth: size
        implicitHeight: size

        Shape {
            width: 24
            height: 24
            anchors.centerIn: parent
            scale: providerLogoRoot.size / 24
            antialiasing: true
            preferredRendererType: Shape.CurveRenderer

            ShapePath {
                fillColor: root.providerLogoColor(providerLogoRoot.provider)
                strokeColor: "transparent"
                strokeWidth: 0

                PathSvg {
                    path: providerLogoRoot.logoPath
                }
            }
        }
    }

    // Circular allowance indicator for the overview card.
    component AllowanceRing: Item {
        id: allowanceRing
        property var allowance: null
        property int size: 52
        readonly property real fraction: root.displayPercent(allowance) / 100
        width: size
        height: size

        Shape {
            anchors.fill: parent
            antialiasing: true
            preferredRendererType: Shape.CurveRenderer

            ShapePath {
                strokeColor: Theme.withAlpha(Theme.surfaceVariantText, 0.18)
                strokeWidth: 5
                fillColor: "transparent"
                capStyle: ShapePath.RoundCap
                PathAngleArc {
                    centerX: allowanceRing.size / 2
                    centerY: allowanceRing.size / 2
                    radiusX: allowanceRing.size / 2 - 3
                    radiusY: allowanceRing.size / 2 - 3
                    startAngle: -90
                    sweepAngle: 360
                }
            }

            ShapePath {
                strokeColor: root.allowanceColor(allowanceRing.allowance)
                strokeWidth: 5
                fillColor: "transparent"
                capStyle: ShapePath.RoundCap
                PathAngleArc {
                    centerX: allowanceRing.size / 2
                    centerY: allowanceRing.size / 2
                    radiusX: allowanceRing.size / 2 - 3
                    radiusY: allowanceRing.size / 2 - 3
                    startAngle: -90
                    sweepAngle: 360 * Math.max(0, Math.min(1, allowanceRing.fraction))
                }
            }
        }

        StyledText {
            anchors.centerIn: parent
            text: root.allowanceLabel(allowanceRing.allowance, false)
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Bold
            color: root.allowanceColor(allowanceRing.allowance)
        }
    }

    component LimitBucket: Item {
        property string title: ""
        property var allowance: null
        property string detail: ""

        implicitHeight: limitBucketColumn.implicitHeight

        Column {
            id: limitBucketColumn
            width: parent.width
            spacing: 2

            StyledText {
                width: parent.width
                text: title
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.Medium
                color: Theme.surfaceVariantText
                elide: Text.ElideRight
                maximumLineCount: 1
            }

            StyledText {
                width: parent.width
                text: root.allowanceLabel(allowance)
                font.pixelSize: Theme.fontSizeXLarge
                font.weight: Font.Bold
                color: root.allowanceColor(allowance)
                elide: Text.ElideRight
                maximumLineCount: 1
            }

            StyledText {
                width: parent.width
                text: detail !== "" ? detail : root.formatReset(allowance)
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceVariantText
                elide: Text.ElideRight
                maximumLineCount: 1
            }
        }
    }

    popoutWidth: 420
    popoutHeight: 480
}
