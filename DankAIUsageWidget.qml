import QtQuick
import QtQuick.Shapes
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
    property bool barShowPluginIcon: false
    property bool barShowProviderLogos: true
    property bool barShowClaudeSession: true
    property bool barShowClaudeWeekly: true
    property bool barShowClaudeCredits: false
    property bool includeCachedTokens: false
    property bool compactPill: false
    property bool showUsed: false
    property bool quickControlsOpen: false
    property bool historyOpen: false
    property bool enableClaudePrime: false

    property bool isLoading: true
    property bool hasError: false
    property string errorText: ""
    property string lastUpdated: ""
    property var providers: []
    property var usageHistory: []
    property string historyError: ""
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
    property var codexResetStatus: ({ armed: false, message: "Checking reset control..." })
    property bool codexResetReady: false
    property string _codexResetOutput: ""
    property string _codexResetAction: ""
    property bool _codexResetWasArmed: false

    function loadSettings() {
        if (!pluginService || !pluginService.loadPluginData) return
        var wasEnabled = enableClaudePrime
        refreshInterval = pluginService.loadPluginData(pluginId, "refreshInterval", 300) || 300
        periodDays = pluginService.loadPluginData(pluginId, "periodDays", 7) || 7
        showCodex = pluginService.loadPluginData(pluginId, "showCodex", true) !== false
        showClaude = pluginService.loadPluginData(pluginId, "showClaude", true) !== false
        barShowPluginIcon = pluginService.loadPluginData(pluginId, "barShowPluginIcon", false) === true
        barShowProviderLogos = pluginService.loadPluginData(pluginId, "barShowProviderLogos", true) !== false
        barShowClaudeSession = pluginService.loadPluginData(pluginId, "barShowClaudeSession", true) !== false
        barShowClaudeWeekly = pluginService.loadPluginData(pluginId, "barShowClaudeWeekly", true) !== false
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
        lastClaudeAutoPrimeAt = pluginService.loadPluginState(pluginId, "lastClaudeAutoPrimeAt", 0) || 0
        lastClaudeAutoPrimeFailed = pluginService.loadPluginState(pluginId, "lastClaudeAutoPrimeFailed", false) === true
        var cached = pluginService.loadPluginState(pluginId, "lastSummary", null)
        if (cached && cached.providers) applySummary(cached, false)
    }

    Component.onCompleted: {
        loadSettings()
        loadCache()
        refreshUsage()
        runCodexReset("status")
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

    Timer {
        interval: 60000
        running: true
        repeat: true
        // The helper owns the one-shot state and serializes multiple widgets.
        // Hiding Codex pauses automatic consumption, but still updates status.
        onTriggered: root.runCodexReset(root.showCodex ? "check" : "status")
    }

    function runCodexReset(action) {
        if (codexResetProcess.running) return
        _codexResetOutput = ""
        _codexResetAction = action
        _codexResetWasArmed = codexResetStatus.armed === true
        codexResetProcess.command = ["dankaiusage", "codex-reset", action]
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
            if (root.codexResetReady && root._codexResetAction === "check" && root._codexResetWasArmed
                    && root.codexResetStatus.armed !== true)
                root.refreshUsage()
        }
    }

    function refreshUsage() {
        if (usageProcess.running) {
            _usageRefreshPending = true
            return
        }
        _pendingOutput = ""
        usageProcess.command = [
            "dankaiusage", "summary",
            "--period-days", "" + root.periodDays
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
        claudePrimeProcess.command = ["dankaiusage", "claude-prime"]
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
                    root.pluginService.savePluginState(root.pluginId, "lastSummary", cachedSummary)
                }
            } catch (e) {
                root.hasError = true
                root.errorText = "Could not parse usage data"
            }
            root.isLoading = false
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
        capabilities = summary.capabilities || {}
        providers = summary.providers || []
        usageHistory = summary.history || []
        historyError = summary.historyError || ""
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
        case "window_changed_unknown": return "Reset schedule changed"
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
        for (var i = 0; i < list.length; i++) total += displayTotal(list[i].period)
        return total
    }

    function filteredGrandInput() {
        var total = 0
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) total += displayInput(list[i].period)
        return total
    }

    function filteredGrandOutput() {
        var total = 0
        var list = visibleProviders()
        for (var i = 0; i < list.length; i++) total += displayOutput(list[i].period)
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

    function allowanceColor(allowance) {
        if (!knownAllowance(allowance)) return Theme.surfaceVariantText
        var pct = allowance.percentRemaining || 0
        if (pct <= 10) return "#ff6b6b"
        if (pct <= 25) return "#ffaa00"
        return Theme.primary
    }

    function providerQuotaBuckets(provider) {
        if (!provider || !provider.quotaBuckets) return []
        return provider.quotaBuckets
    }

    function providerQuotaHeight(provider) {
        var count = providerQuotaBuckets(provider).length
        return count > 0 ? count * 48 + (count - 1) * Theme.spacingXS : 28
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
        var reset = formatReset(bucket.allowance)
        return reset === "--" ? allowanceDetail(bucket.allowance) : "Resets " + reset
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
        if (bucket.allowance && bucket.allowance.window === "weekly") return barShowClaudeWeekly
        return false
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
                        text: (barShowProviderLogos ? "" : list[i].name + " ") + quotaShortLabel(compactWeakest) + " " + allowanceLabel(compactWeakest.allowance, false)
                    })
                }
            }
            return segments
        }

        for (var k = 0; k < list.length; k++) {
            var text = providerTopBarText(list[k])
            if (text !== "") segments.push({
                provider: list[k],
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
        return formatTokens(filteredGrandInput()) + " in / " + formatTokens(filteredGrandOutput()) + " out"
    }

    function providerLogoColor(provider) {
        if (!provider || !provider.available || provider.error) return "#ff6b6b"
        return Theme.primary
    }

    function providerColor(provider) {
        if (!provider.available || provider.error) return "#ff6b6b"
        return provider.id === "codex" ? Theme.primary : "#8bc34a"
    }

    function providerNote(provider) {
        if (!provider || !provider.meta) return ""
        var parts = []
        if (provider.id === "claude" && claudePrimeText !== "") {
            parts.push(claudePrimeText)
        }
        if (provider.id === "claude" && provider.meta.tokenDataIncludesWeb === false) {
            parts.push("Claude tokens are local Claude Code only, not web")
        }
        if (provider.id === "claude" && provider.meta.sessionFallbackSource) {
            parts.push("Session timer from Claude prime; account limits unavailable")
        }
        if (provider.meta.tokenDataNote) {
            var note = provider.meta.tokenDataNote
            var lastUsage = formatShortDateTime(provider.meta.lastUsageAt)
            if (lastUsage !== "") note += " (last " + lastUsage + ")"
            parts.push(note)
        }
        if (provider.meta.limitError) {
            if (provider.id === "claude" && provider.meta.oauthUsageError) parts.push(provider.meta.oauthUsageError)
            else if (provider.id === "claude" && provider.meta.statuslineNextStep) parts.push(provider.meta.statuslineNextStep)
            else parts.push(provider.meta.limitError)
        }
        else if (provider.meta.tokenDataError) parts.push(provider.meta.tokenDataError)
        return parts.join(" | ")
    }

    horizontalBarPill: Component {
        Row {
            spacing: Theme.spacingS

            DankIcon {
                name: "monitoring"
                size: Theme.fontSizeLarge
                color: root.hasError ? "#ff6b6b" : Theme.primary
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
                        color: root.hasError ? "#ff6b6b" : Theme.surfaceText
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
                color: root.hasError ? "#ff6b6b" : Theme.surfaceText
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
                        text: root.quotaBucketCount() + " limits" + (root.lastUpdated ? " - " + root.lastUpdated : "")
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

            Flow {
                width: parent.width
                spacing: Theme.spacingXS

                QuickToggle {
                    text: "Left"
                    checked: !root.showUsed
                    onClicked: root.setQuickSetting("showUsed", false)
                }
                QuickToggle {
                    text: "Used"
                    checked: root.showUsed
                    onClicked: root.setQuickSetting("showUsed", true)
                }
                QuickToggle {
                    text: "Bar controls"
                    checked: root.quickControlsOpen
                    onClicked: root.quickControlsOpen = !root.quickControlsOpen
                }
            }

            Column {
                width: parent.width
                spacing: Theme.spacingXS
                visible: root.quickControlsOpen

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
                    QuickToggle { text: "Weekly"; checked: root.barShowClaudeWeekly; onClicked: root.setQuickSetting("barShowClaudeWeekly", !root.barShowClaudeWeekly) }
                    QuickToggle { text: "Credits"; checked: root.barShowClaudeCredits; onClicked: root.setQuickSetting("barShowClaudeCredits", !root.barShowClaudeCredits) }
                }
            }

            StyledRect {
                width: parent.width
                height: 92
                radius: Theme.cornerRadius
                color: Theme.surfaceContainerHigh

                Item {
                    anchors.fill: parent
                    anchors.margins: Theme.spacingM

                    LimitBucket {
                        anchors.fill: parent
                        title: root.overallQuotaTitle()
                        allowance: root.overallQuotaAllowance()
                        detail: root.overallQuotaDetail()
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
                    text: root.filteredGrandTokenBreakdown()
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
                        height: providerContent.implicitHeight + 2 * Theme.spacingS
                        radius: Theme.cornerRadius
                        color: Theme.surfaceContainerHigh

                        Column {
                            id: providerContent
                            x: Theme.spacingS
                            y: Theme.spacingS
                            width: parent.width - 2 * Theme.spacingS
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
                                        size: Theme.fontSizeMedium
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

                                Row {
                                    id: providerActions
                                    anchors.right: parent.right
                                    anchors.verticalCenter: parent.verticalCenter
                                    spacing: Theme.spacingXS

                                    StyledText {
                                        text: root.providerAllowanceSummary(modelData)
                                        width: Math.min(150, implicitWidth)
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
                                        anchors.verticalCenter: parent.verticalCenter
                                        visible: modelData.id === "claude" && root.enableClaudePrime
                                        enabled: !root.isPrimingClaude
                                        onClicked: root.primeClaude()
                                    }
                                }
                            }

                            Column {
                                width: parent.width
                                height: root.providerQuotaHeight(modelData)
                                spacing: Theme.spacingXS

                                Repeater {
                                    model: root.providerQuotaBuckets(modelData)

                                    QuotaBar {
                                        width: parent.width
                                        bucket: modelData
                                    }
                                }

                                StyledText {
                                    width: parent.width
                                    text: "Quota data unavailable"
                                    font.pixelSize: Theme.fontSizeSmall
                                    color: Theme.surfaceVariantText
                                    visible: root.providerQuotaBuckets(modelData).length === 0
                                }
                            }

                            Item {
                                width: parent.width
                                height: 30

                                StyledText {
                                    text: "Local token history"
                                    anchors.left: parent.left
                                    anchors.verticalCenter: parent.verticalCenter
                                    font.pixelSize: Theme.fontSizeSmall
                                    color: Theme.surfaceVariantText
                                }

                                StyledText {
                                    text: root.inputTokenLabel(modelData.period) + " / " + root.outputTokenLabel(modelData.period)
                                    anchors.right: parent.right
                                    anchors.verticalCenter: parent.verticalCenter
                                    font.pixelSize: Theme.fontSizeSmall
                                    color: Theme.surfaceText
                                }
                            }

                            Item {
                                width: parent.width
                                height: 34
                                visible: root.providerResets(modelData).length > 0

                                DankIcon {
                                    name: "refresh"
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
                                visible: modelData.id === "codex"

                                QuickToggle {
                                    text: codexResetProcess.running ? "Checking reset..." : !root.codexResetReady ? "Cancel auto reset" : "Auto-use one reset"
                                    checked: root.codexResetStatus.armed === true
                                    enabled: !codexResetProcess.running && (root.codexResetReady || root.codexResetStatus.stateKnown === false)
                                    opacity: enabled ? 1 : 0.5
                                    onClicked: root.runCodexReset(!root.codexResetReady || root.codexResetStatus.armed ? "disarm" : "arm")
                                }

                                StyledText {
                                    width: parent.width
                                    text: root.codexResetDetailText()
                                    font.pixelSize: Theme.fontSizeSmall
                                    color: Theme.surfaceVariantText
                                    wrapMode: Text.WordWrap
                                }

                                StyledText {
                                    width: parent.width
                                    text: "Uses one reset at 99% general usage or 10 min before its expiry. Turns off after one attempt; DMS must be running."
                                    font.pixelSize: Theme.fontSizeSmall
                                    color: Theme.surfaceVariantText
                                    wrapMode: Text.WordWrap
                                }
                            }

                            StyledText {
                                width: parent.width
                                text: root.providerNote(modelData)
                                font.pixelSize: Theme.fontSizeSmall
                                color: "#ffaa00"
                                elide: Text.ElideRight
                                maximumLineCount: 2
                                wrapMode: Text.WordWrap
                                visible: text !== ""
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

            Column {
                width: parent.width
                spacing: Theme.spacingS
                visible: root.showCodex || root.showClaude

                QuickToggle {
                    text: "Reset history" + (root.visibleHistory().length ? " · " + root.visibleHistory().length : "")
                    checked: root.historyOpen
                    onClicked: root.historyOpen = !root.historyOpen
                }

                StyledText {
                    width: parent.width
                    text: root.historyError
                    textFormat: Text.PlainText
                    font.pixelSize: Theme.fontSizeSmall
                    color: Theme.surfaceVariantText
                    wrapMode: Text.WordWrap
                    visible: root.historyError !== ""
                }

                Column {
                    width: parent.width
                    spacing: Theme.spacingS
                    visible: root.historyOpen

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
                        model: root.visibleHistory()

                        StyledRect {
                            width: parent.width
                            height: historyContent.implicitHeight + 2 * Theme.spacingS
                            color: Theme.surfaceContainerHigh
                            radius: Theme.cornerRadius

                            Column {
                                id: historyContent
                                x: Theme.spacingS
                                y: Theme.spacingS
                                width: parent.width - 2 * Theme.spacingS
                                spacing: Theme.spacingXS

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
                    }
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
        border.width: activeFocus ? 2 : 0
        border.color: Theme.surfaceText
        Accessible.role: Accessible.CheckBox
        Accessible.name: text
        Accessible.checked: checked
        Accessible.onPressAction: clicked()
        Keys.onSpacePressed: clicked()
        Keys.onReturnPressed: clicked()
        StyledText {
            id: quickLabel
            anchors.centerIn: parent
            text: quickToggle.text
            color: quickToggle.checked ? Theme.primaryText : Theme.surfaceText
            font.pixelSize: Theme.fontSizeSmall
        }
        MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: quickToggle.clicked()
        }
    }

    component QuotaBar: Item {
        property var bucket: null
        height: 48

        StyledText {
            text: bucket ? bucket.label : "Quota"
            anchors.left: parent.left
            anchors.right: quotaBarValue.left
            anchors.rightMargin: Theme.spacingS
            anchors.top: parent.top
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Medium
            color: Theme.surfaceText
            elide: Text.ElideRight
            maximumLineCount: 1
        }

        StyledText {
            id: quotaBarValue
            text: root.quotaValue(bucket)
            anchors.right: parent.right
            anchors.top: parent.top
            font.pixelSize: Theme.fontSizeSmall
            font.weight: Font.Medium
            color: root.allowanceColor(bucket ? bucket.allowance : null)
            elide: Text.ElideRight
            maximumLineCount: 1
        }

        StyledRect {
            id: quotaTrack
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            anchors.topMargin: 21
            height: 6
            radius: 3
            color: Qt.rgba(Theme.surfaceVariantText.r, Theme.surfaceVariantText.g, Theme.surfaceVariantText.b, 0.18)

            StyledRect {
                width: parent.width * root.quotaProgress(bucket) / 100
                height: parent.height
                radius: parent.radius
                color: root.allowanceColor(bucket ? bucket.allowance : null)
            }
        }

        StyledText {
            text: root.quotaDetail(bucket)
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
            elide: Text.ElideRight
            maximumLineCount: 1
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

    component LimitBucket: Item {
        property string title: ""
        property var allowance: null
        property string detail: ""

        height: parent ? parent.height : 64

        Column {
            anchors.fill: parent
            spacing: 2

            Row {
                width: parent.width
                spacing: Theme.spacingXS

                DankIcon {
                    name: "monitoring"
                    size: Theme.fontSizeSmall
                    color: root.allowanceColor(allowance)
                    anchors.verticalCenter: parent.verticalCenter
                }

                StyledText {
                    width: parent.width - Theme.fontSizeSmall - Theme.spacingXS
                    text: title
                    font.pixelSize: Theme.fontSizeSmall
                    font.weight: Font.Medium
                    color: Theme.surfaceVariantText
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
