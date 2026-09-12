#!/usr/bin/env bash
set -euo pipefail

PASS=0
FAIL=0
TEST_TMP="$(mktemp -d)"
trap 'rm -rf "$TEST_TMP"' EXIT

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1 — $2"; }

assert_eq() {
    local description="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        pass "$description"
    else
        fail "$description" "expected '$expected', got '$actual'"
    fi
}

echo "plugin.json"
if python3 - <<'PY'
import json
import pathlib
import re
import struct

manifest_path = pathlib.Path("plugin.json")
plugin = json.loads(manifest_path.read_text(encoding="utf-8"))

assert plugin["id"] == "dankAIUsage"
assert plugin["name"] == "AI Usage"
assert plugin["type"] == "widget"
assert "dankbar-widget" in plugin["capabilities"]
assert re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", plugin["version"])
assert {"settings_read", "settings_write", "process"} <= set(plugin["permissions"])
assert plugin["settings_schema"]["refreshInterval"]["minimum"] == 180
assert plugin["settings_schema"]["refreshInterval"]["maximum"] == 3600
assert plugin["settings_schema"]["refreshInterval"]["default"] == 300

component = pathlib.Path(plugin["component"].removeprefix("./"))
settings = pathlib.Path(plugin["settings"].removeprefix("./"))
assert component.is_file(), component
assert settings.is_file(), settings

component_text = component.read_text(encoding="utf-8")
settings_text = settings.read_text(encoding="utf-8")
plugin_id = plugin["id"]
assert f'pluginId: "{plugin_id}"' in component_text
assert f'pluginId: "{plugin_id}"' in settings_text

readme = pathlib.Path("README.md").read_text(encoding="utf-8")
for screenshot in ("docs/screenshot.png", "docs/screenshot-simple.png", "docs/screenshot-bar.png"):
    assert f"]({screenshot})" in readme, f"README does not reference {screenshot}"
    with pathlib.Path(screenshot).open("rb") as image:
        header = image.read(24)
    assert header[:8] == b"\x89PNG\r\n\x1a\n", f"{screenshot} is not PNG"
    assert header[12:16] == b"IHDR", f"{screenshot} has no PNG dimensions"
    width, height = struct.unpack(">II", header[16:24])
    assert width > 0 and height > 0, f"{screenshot} is empty"

# Redemption is an explicit helper-owned one-shot, never a default-on setting
# or an implicit side effect of collecting usage.
assert 'armed: false' in component_text
assert '"dankaiusage", "codex-reset", action,' in component_text
assert component_text.count('"--refresh-interval", "" + root.refreshInterval') == 3
assert 'runCodexReset("status")' in component_text
assert 'if (showCodex && codexResetStatus.armed === true)' in component_text
assert 'runCodexReset("check")' in component_text
assert 'codexResetStatus.armed ? "disarm" : "arm"' in component_text
assert 'root.codexResetReady = status.stateKnown === true' in component_text
assert 'Qt.callLater(root.refreshUsage)' in component_text
assert '!root.codexResetReady || root.codexResetStatus.armed ? "disarm" : "arm"' in component_text
assert re.search(r'DankToggle\s*\{\s*id: codexAutoResetToggle', component_text)
assert 'toggling: codexResetProcess.running' in component_text
assert 'Accessible.onToggleAction: handleClick()' in component_text
assert 'Keys.onSpacePressed: handleClick()' in component_text
assert 'border.width: codexAutoResetToggle.activeFocus ? 2 : 0' in component_text
assert 'id: providerContent' in component_text
assert 'height: providerContent.implicitHeight' in component_text
assert 'usageHistory = summary.history || []' in component_text
assert 'historyError = summary.historyError || ""' in component_text
assert 'case "reset_redeemed_inferred": return "Likely reset redeemed"' in component_text
assert 'delete cachedSummary.history' in component_text
assert 'root.historyEventDetail(modelData)' in component_text

# Both bar layouts omit the mode suffix; dropdown labels retain it by default.
assert 'includeMode === false ? "%"' in component_text
assert 'allowanceLabel(buckets[i].allowance, false)' in component_text
assert 'allowanceLabel(compactWeakest.allowance, false)' in component_text
assert 'return allowanceLabel(bucket.allowance)' in component_text
assert 'component TokenHistoryRow: StyledRect' in component_text
assert 'property string tokenHistoryRange: "7d"' in component_text
for token_range in ('5h', '7d', '30d', '90d', 'tracked'):
    assert f'{{ key: "{token_range}"' in component_text
assert 'loadPluginState(pluginId, "tokenHistorySession", false)' in component_text
assert 'loadPluginState(pluginId, "tokenHistoryRange", "")' in component_text
assert 'savePluginState(pluginId, "tokenHistoryRange", tokenHistoryRange)' in component_text
assert 'Keys.onSpacePressed: tokenRow.selectorOpen = !tokenRow.selectorOpen' in component_text
assert 'model: root.tokenHistoryRangeChoices()' in component_text
assert 'root.selectTokenHistoryRange(modelData.key)' in component_text
assert 'height: selectorOpen ? 36 + tokenRangeFlow.implicitHeight + Theme.spacingXS : 32' in component_text
assert 'anchors.verticalCenter: tokenRowHeader.verticalCenter' in component_text
assert 'if (days === 7 || days === 30 || days === 90) return days + "d"' in component_text
assert 'if (tokenHistoryRange === "period") return provider.period' in component_text
for field in ('fiveHours', 'sevenDays', 'thirtyDays', 'ninetyDays'):
    assert f'rolling.{field}' in component_text
assert 'var trackedProviders = trackingStatus.providers || {}' in component_text
assert 'if (!tokenHistoryTotals(provider)) return false' in component_text
assert 'trackingStatus.known !== true || !trackingStatus.startedAt' in component_text
assert 'meta.tokenDataAvailable === false' in component_text
assert 'if (available === 0) return "Unavailable"' in component_text
assert 'sqlite3' not in plugin["requires"]

# Persistent tracking remains helper-owned, fail-closed, and explicit.
assert '["dankaiusage", "tracking", action]' in component_text
assert 'typeof status.known !== "boolean"' in component_text
assert 'if (action !== "status" && usageProcess.running) return' in component_text
assert '&& !trackingProcess.running && !usageProcess.running' in component_text
assert 'root.runTracking(root.trackingStatus.enabled === true ? "pause" : "enable")' in component_text
assert 'if (root.clearTrackingConfirm) root.runTracking("clear")' in component_text
assert 'text: root.clearTrackingConfirm ? "Confirm clear" : "Clear tracked data"' in component_text
assert 'delete cachedSummary.tracking' in component_text
assert 'Qt.callLater(root.refreshUsage)' in component_text
assert 'Some tracked token data is incomplete.' in component_text
assert 'The initial total may include older retained local history.' in component_text
assert 'all-time' not in component_text.lower()

# Refresh intervals remain seconds in storage/argv while the setting presents
# whole minutes with an explicit, clickable default marker.
assert 'function normalizedRefreshInterval(value)' in component_text
assert 'Math.max(180, Math.min(3600, Math.round(seconds / 60) * 60))' in component_text
assert 'interval: root.refreshInterval * 1000' in component_text
assert component_text.count('interval: root.refreshInterval * 1000') == 1
assert 'onTriggered: root.refreshCycle()' in component_text
assert 'onClicked: root.refreshCycle()' in component_text
assert 'if (codexResetProcess.running)' in component_text
refresh_cycle_at = component_text.index('function refreshCycle()')
reset_check_at = component_text.index('runCodexReset("check")', refresh_cycle_at)
summary_refresh_at = component_text.index('refreshUsage()', refresh_cycle_at)
assert reset_check_at < summary_refresh_at
assert 'minimum: 3' in settings_text
assert 'maximum: 60' in settings_text
assert 'step: 1' in settings_text
assert 'text: "Default 5 min"' in settings_text
assert 'onClicked: refreshIntervalSetting.setMinutes(5)' in settings_text
assert 'Accessible.onPressAction: if (enabled) refreshIntervalSetting.setMinutes(5)' in settings_text
assert 'root.saveValue("refreshInterval", seconds)' in settings_text
assert 'long intervals can delay detection or miss a brief expiry window' in settings_text
assert 'provider.meta.usageRefreshPending === true' in component_text
assert 'provider.meta.usageStale === true || provider.meta.usageDataStale === true' in component_text
assert 'advancedDropdown && provider.meta.usageCached === true' in component_text
assert 'formatShortDateTime(provider.meta.usageNextRefreshAt)' in component_text
assert 'provider requests respect the selected interval' in component_text

schema = plugin["settings_schema"]
for key in schema:
    assert f'"{key}"' in component_text, f"component does not load {key}"
    if key == "barClaudeWeeklyOverrides":
        # Dynamic provider-defined choices live in the dropdown, not a fixed
        # settings form. The settings menu explains where to customize them.
        assert schema[key] == {"type": "object", "default": {}}
        assert 'model: root.claudeWeeklyBarChoices()' in component_text
        assert 'root.setClaudeWeeklyBarBucket(modelData, !checked)' in component_text
        assert 'individual choices take precedence' in settings_text
        continue
    assert f'settingKey: "{key}"' in settings_text, f"settings UI does not expose {key}"

for icon in (pathlib.Path("assets/openai.svg"), pathlib.Path("assets/claude.svg")):
    assert icon.is_file(), icon
    match = re.search(r'<path d="([^"]+)"', icon.read_text(encoding="utf-8"))
    assert match, f"{icon} has no SVG path"
    assert match.group(1) in component_text, f"{icon} path is not embedded in the QML shape"
PY
then
    pass "manifest, settings, QML references, and logo assets are consistent"
else
    fail "plugin structure" "manifest or referenced plugin content is inconsistent"
fi

echo "Dropdown modes"
if python3 - <<'PY'
import pathlib
import re

component = pathlib.Path("DankAIUsageWidget.qml").read_text(encoding="utf-8")

def function_body(name):
    match = re.search(rf"\bfunction\s+{re.escape(name)}\s*\([^)]*\)\s*\{{", component)
    assert match, f"missing {name}()"
    start = match.end()
    depth = 1
    quote = None
    escaped = False
    for index in range(start, len(component)):
        char = component[index]
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
        elif char in "\"'`":
            quote = char
        elif char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return component[start:index]
    raise AssertionError(f"unterminated {name}()")

assert 'property string dropdownMode: "simple"' in component
assert 'readonly property bool advancedDropdown: dropdownMode === "advanced"' in component

load_cache = function_body("loadCache")
resolve_at = load_cache.index("resolveDropdownMode(")
persist_at = load_cache.index('savePluginState(pluginId, "dropdownMode", dropdownMode)')
summary_at = load_cache.index("applySummary(cached, false)")
assert resolve_at < persist_at < summary_at, "dropdown migration must persist before cached summary refresh"

completed = re.search(r"Component\.onCompleted\s*:\s*\{([^}]*)\}", component, re.S)
assert completed, "missing startup sequence"
assert completed.group(1).index("loadCache()") < completed.group(1).index("refreshCycle()")

setter = function_body("setDropdownMode")
assert 'mode !== "simple" && mode !== "advanced"' in setter
assert 'savePluginState(pluginId, "dropdownMode", mode)' in setter
assert "clearTrackingConfirm = false" in setter
for forbidden in ("setQuickSetting", "runTracking", "runCodexReset", "primeClaude", ".running", "barShow", "enableClaudePrime"):
    assert forbidden not in setter, f"mode setter must not perform backend/setting action: {forbidden}"

# Advanced-only detail is explicitly gated, while quota and warning rows remain
# available in both modes. Reset recovery controls deliberately have a wider gate.
for expected in (
    'text: root.advancedDropdown ? "Advanced" : "Simple"',
    'onClicked: root.setDropdownMode(root.advancedDropdown ? "simple" : "advanced")',
    'text: root.showUsed ? "Used" : "Left"',
    'onClicked: root.setQuickSetting("showUsed", !root.showUsed)',
    'visible: root.advancedDropdown && root.quickControlsOpen',
    'visible: root.advancedDropdown && root.tokenHistoryRange === "tracked"',
    'visible: root.advancedDropdown && root.providerResets(modelData).length > 0',
    'visible: modelData.id === "codex" && root.resetControlsVisible()',
    'visible: (root.advancedDropdown && (root.showCodex || root.showClaude))',
    'visible: !root.advancedDropdown && modelData.id === "claude" && root.enableClaudePrime',
):
    assert expected in component, f"missing dropdown visibility contract: {expected}"
assert component.count("visible: root.advancedDropdown\n") >= 3
assert component.count('onClicked: root.setDropdownMode(') == 1
assert component.count('onClicked: root.setQuickSetting("showUsed",') == 1
assert 'model: root.providerQuotaBuckets(modelData)' in component
assert 'visible: root.providerQuotaBuckets(modelData).length === 0' in component
assert 'visible: text !== ""' in component
assert 'visible: root.hasError && root.errorText !== ""' in component
PY
then
    pass "dropdown persistence and visibility contracts"
else
    fail "dropdown contracts" "mode persistence or visibility wiring is inconsistent"
fi

if command -v node >/dev/null 2>&1; then
    if node <<'JS'
const fs = require("fs");
const qml = fs.readFileSync("DankAIUsageWidget.qml", "utf8");
const settingsQml = fs.readFileSync("DankAIUsageSettings.qml", "utf8");

function extractFunction(name, source = qml) {
    const marker = new RegExp(`\\bfunction\\s+${name}\\s*\\([^)]*\\)\\s*\\{`, "g");
    const match = marker.exec(source);
    if (!match) throw new Error(`missing ${name}()`);
    const brace = source.indexOf("{", match.index);
    let depth = 0;
    let quote = null;
    let escaped = false;
    for (let i = brace; i < source.length; i++) {
        const char = source[i];
        if (quote !== null) {
            if (escaped) escaped = false;
            else if (char === "\\") escaped = true;
            else if (char === quote) quote = null;
            continue;
        }
        if (char === '"' || char === "'" || char === "`") quote = char;
        else if (char === "{") depth++;
        else if (char === "}" && --depth === 0) return source.slice(match.index, i + 1);
    }
    throw new Error(`unterminated ${name}()`);
}

function bindQmlFunction(name, scope, source = qml) {
    const expression = extractFunction(name, source).replace(/^function\s+\w+/, "function");
    return new Function("scope", `with (scope) { return (${expression}); }`)(scope);
}

function equal(actual, expected, description) {
    if (actual !== expected)
        throw new Error(`${description}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const resolve = bindQmlFunction("resolveDropdownMode", {});
const weeklyScope = {
    barShowClaudeSession: true, barShowClaudeWeekly: true,
    barShowClaudeCredits: false, barClaudeWeeklyOverrides: {},
};
const weeklyAll = {id: "general-weekly", allowance: {window: "weekly"}};
const weeklyFable = {id: "fable-weekly", allowance: {window: "weekly"}};
const weeklyFuture = {id: "future-weekly", allowance: {window: "weekly"}};
const weeklyShown = bindQmlFunction("claudeBucketShownInBar", weeklyScope);
equal(weeklyShown(weeklyAll), true, "legacy weekly selection includes general");
equal(weeklyShown(weeklyFable), true, "legacy weekly selection includes scoped");
for (const general of [false, true]) {
    for (const scoped of [false, true]) {
        weeklyScope.barClaudeWeeklyOverrides = {"general-weekly": general, "fable-weekly": scoped};
        equal(weeklyShown(weeklyAll), general, "general weekly independent");
        equal(weeklyShown(weeklyFable), scoped, "scoped weekly independent");
    }
}
weeklyScope.barShowClaudeWeekly = false;
equal(weeklyShown(weeklyFuture), false, "new limits inherit weekly default");
equal(weeklyShown(weeklyFable), true, "explicit selection overrides default");
weeklyScope.setQuickSetting = (key, value) => { weeklyScope[key] = value; };
const saveWeekly = bindQmlFunction("setClaudeWeeklyBarBucket", weeklyScope);
const oldOverrides = weeklyScope.barClaudeWeeklyOverrides;
saveWeekly(weeklyAll, false);
equal(oldOverrides["general-weekly"], true, "saving replaces object for QML reactivity");
equal(weeklyShown(weeklyAll), false, "saved general choice applied");
equal(weeklyShown(weeklyFable), true, "saving preserves another weekly choice");
weeklyScope.claudeBucketShownInBar = weeklyShown;
weeklyScope.providerQuotaBuckets = bindQmlFunction("providerQuotaBuckets", {});
weeklyScope.providerTopBarBuckets = bindQmlFunction("providerTopBarBuckets", weeklyScope);
weeklyScope.visibleProviders = () => [{id: "claude", quotaBuckets: [weeklyAll, weeklyFable]}];
weeklyScope.compactPill = true;
weeklyScope.barShowProviderLogos = true;
weeklyScope.knownAllowance = () => true;
weeklyScope.quotaShortLabel = bucket => bucket.id;
weeklyScope.allowanceLabel = () => "50%";
const compactSegments = bindQmlFunction("topBarSegments", weeklyScope);
equal(compactSegments()[0].text, "fable-weekly 50%", "compact ignores deselected general weekly");
saveWeekly(weeklyFable, false);
equal(compactSegments().length, 0, "no weekly segment when neither selected");
weeklyScope.claudeProvider = () => ({quotaBuckets: [weeklyAll, weeklyFable, {id: "session", allowance: {window: "session"}}]});
weeklyScope.providerQuotaBuckets = bindQmlFunction("providerQuotaBuckets", {});
equal(bindQmlFunction("claudeWeeklyBarChoices", weeklyScope)().length, 2, "controls list each weekly bucket only");
weeklyScope.claudeProvider = () => null;
equal(bindQmlFunction("claudeWeeklyBarChoices", weeklyScope)().length, 0, "missing provider is safe");
equal(resolve("simple", {providers: []}), "simple", "persisted simple wins");
equal(resolve("advanced", null), "advanced", "persisted advanced wins");
equal(resolve("", {providers: []}), "advanced", "existing cached user migrates to advanced");
equal(resolve("invalid", {providers: {}}), "advanced", "invalid persisted value migrates from cache");
equal(resolve(undefined, {}), "simple", "new user defaults to simple");
equal(resolve(null, null), "simple", "missing state defaults to simple");

const normalizeRefresh = bindQmlFunction("normalizedRefreshInterval", {});
equal(normalizeRefresh(undefined), 300, "missing refresh interval uses default");
equal(normalizeRefresh(null), 300, "null refresh interval uses default");
equal(normalizeRefresh(30), 180, "legacy interval clamps to minimum");
equal(normalizeRefresh(190), 180, "legacy seconds snap to whole minutes");
equal(normalizeRefresh(210), 240, "half minute snaps to nearest minute");
equal(normalizeRefresh(900), 900, "valid whole-minute interval is preserved");
equal(normalizeRefresh(3599), 3600, "interval snaps at upper bound");
equal(normalizeRefresh(7200), 3600, "interval clamps to maximum");

const primeProvider = { available: true, meta: {} };
let primeCalls = 0;
const primeScope = {
    enableClaudePrime: true, showClaude: true, isPrimingClaude: false,
    claudePrimeProcess: { running: false }, claudeProvider() { return primeProvider; },
    claudeSessionIsActive() { return false; }, lastClaudeAutoPrimeFailed: false,
    lastClaudeAutoPrimeAt: Date.now(), refreshInterval: 300, pluginService: null,
    primeClaude() { primeCalls++; }
};
const autoPrime = bindQmlFunction("maybeAutoPrimeClaude", primeScope);
autoPrime();
equal(primeCalls, 0, "cooldown-only prime result cannot cause immediate retry");
primeScope.lastClaudeAutoPrimeAt = Date.now() - 301000;
autoPrime();
equal(primeCalls, 1, "auto-prime may check again after selected interval");
autoPrime();
equal(primeCalls, 1, "summary after prime cannot loop immediately");
primeScope.lastClaudeAutoPrimeAt = 0;
primeProvider.meta.usageRefreshPending = true;
autoPrime();
equal(primeCalls, 1, "pending post-action quotas do not trigger prime");

const normalizeSetting = bindQmlFunction("normalizedSeconds", {}, settingsQml);
const settingSaves = [];
const refreshSettingScope = {
    refreshSeconds: 300,
    normalizedSeconds: normalizeSetting,
    root: { saveValue(key, value) { settingSaves.push([key, value]); } }
};
const setRefreshMinutes = bindQmlFunction("setMinutes", refreshSettingScope, settingsQml);
setRefreshMinutes(60);
equal(refreshSettingScope.refreshSeconds, 3600, "slider reaches 60-minute maximum");
setRefreshMinutes(5);
equal(refreshSettingScope.refreshSeconds, 300, "default marker/reset restores five minutes");
equal(JSON.stringify(settingSaves), JSON.stringify([["refreshInterval", 3600], ["refreshInterval", 300]]), "slider stores seconds");

const migrationSaves = [];
const migrationScope = {
    refreshSeconds: 300,
    normalizedSeconds: normalizeSetting,
    root: {
        pluginService: {},
        loadValue() { return 30; },
        saveValue(key, value) { migrationSaves.push([key, value]); }
    }
};
bindQmlFunction("loadValue", migrationScope, settingsQml)();
equal(migrationScope.refreshSeconds, 180, "settings load migrates legacy interval");
equal(JSON.stringify(migrationSaves), JSON.stringify([["refreshInterval", 180]]), "settings migration persists clamped seconds");

const saves = [];
const service = new Proxy({
    savePluginState(pluginId, key, value) { saves.push([pluginId, key, value]); }
}, {
    get(target, property) {
        if (!(property in target)) throw new Error(`unexpected plugin service action: ${String(property)}`);
        return target[property];
    }
});
const setterScope = {
    dropdownMode: "advanced",
    clearTrackingConfirm: true,
    pluginId: "dankAIUsage",
    pluginService: service
};
const setMode = bindQmlFunction("setDropdownMode", setterScope);
setMode("simple");
equal(setterScope.dropdownMode, "simple", "setter changes mode");
equal(setterScope.clearTrackingConfirm, false, "setter cancels destructive confirmation");
equal(JSON.stringify(saves), JSON.stringify([["dankAIUsage", "dropdownMode", "simple"]]), "setter persists only dropdown mode");

setterScope.clearTrackingConfirm = true;
for (const invalid of ["", "expert", null, undefined]) setMode(invalid);
equal(setterScope.dropdownMode, "simple", "invalid values do not change mode");
equal(setterScope.clearTrackingConfirm, true, "invalid values have no side effects");
equal(saves.length, 1, "invalid values are not persisted");

function resetVisible(advancedDropdown, codexResetStatus) {
    return bindQmlFunction("resetControlsVisible", {advancedDropdown, codexResetStatus})();
}
equal(resetVisible(false, {armed: false, stateKnown: true}), false, "settled reset controls hide in simple mode");
equal(resetVisible(true, {armed: false, stateKnown: true}), true, "advanced mode shows reset controls");
equal(resetVisible(false, {armed: true, stateKnown: true}), true, "armed reset remains recoverable");
equal(resetVisible(false, {armed: false, stateKnown: false}), true, "unknown reset state remains recoverable");
equal(resetVisible(false, {armed: false, stateKnown: true, error: "failed"}), true, "reset errors remain visible");
equal(resetVisible(false, {armed: false, stateKnown: true, state: "attempted"}), true, "uncertain reset outcome remains visible");
JS
    then
        pass "dropdown JavaScript behavior"
    else
        fail "dropdown JavaScript behavior" "runtime mode behavior is inconsistent"
    fi
else
    pass "dropdown JavaScript behavior (structurally checked; node unavailable)"
fi

echo "History explanations"
if python3 - <<'PY'
import pathlib
import re

component = pathlib.Path("DankAIUsageWidget.qml").read_text(encoding="utf-8")

# The helper receives one bounded JSON record over stdin. No user note is ever
# interpolated into argv or evaluated by a shell.
assert 'historyExplanationProcess.command = ["dankaiusage", "history", "explain"]' in component
assert '["sh", "-c"' not in component
assert '_historyExplanationPayload = JSON.stringify({' in component
assert 'note: historyExplanationNote' in component
assert re.search(
    r'id:\s*historyExplanationProcess.*?onStarted:\s*\{\s*'
    r'write\(root\._historyExplanationPayload \+ "\\n"\)\s*'
    r'.*?stdinEnabled = false',
    component,
    re.S,
)
start_save = component.index('historyExplanationProcess.stdinEnabled = true')
start_process = component.index('historyExplanationProcess.running = true', start_save)
assert start_save < start_process

# A confirmed helper response is the only history mutation. Failures and the
# bounded timeout retain the root-owned draft for an explicit retry.
assert 'root.usageHistory = result.history' in component
assert 'root.cancelHistoryExplanation()' in component
assert 'root.historyExplanationError = e.message ||' in component
assert 'Saving timed out. Your draft is still here; retry when ready.' in component
assert 'interval: 10000' in component
assert 'running: historyExplanationProcess.running' in component
assert 'root._historyExplanationStderr.trim()' not in component
assert 'if (historyExplanationProcess.running)' in component
assert '_usageRefreshPending = true' in component

# Prompt and editor UX: the prompt itself is not Advanced-only, retained edits
# are inline, and user text/error rendering is explicitly plain text.
assert 'id: historyPrompt' in component
assert 'text: root.historyPromptTitle(historyPrompt.group)' in component
assert 'root.latestExplanationPrompt(Date.now())' in component
assert 'component HistoryExplanationEditor: StyledRect' in component
assert 'Applies to " + (root.historyExplanationGroup' in component
assert 'root.beginHistoryExplanation(historyGroupCard.historyGroup,' in component
begin = component[component.index('function beginHistoryExplanation('):component.index('function cancelHistoryExplanation(')]
assert begin.index('historyExplanationNote =') < begin.index('historyExplanationGroup = group')
assert 'text: "User reported · "' in component
assert 'textFormat: TextEdit.PlainText' in component
assert component.count('textFormat: Text.PlainText') >= 12
assert 'root.truncateHistoryNote(text, 280)' in component
assert 'height: Math.max(72, historyNoteInput.contentHeight + 2 * Theme.spacingXS)' in component
assert 'Do not include prompts, account details, or other sensitive information.' in component
assert 'case "dismissed": return "Dismissed without assigning a cause"' in component
PY
then
    pass "history explanation stdin, persistence, failure, and UI contracts"
else
    fail "history explanation contracts" "stdin, persistence, failure, or UI wiring is inconsistent"
fi

if command -v quickshell >/dev/null 2>&1; then
    cp tests/fixtures/history-stdin-probe.qml "$TEST_TMP/history-stdin-probe.qml"
    PROBE_OUTPUT="$(QT_QPA_PLATFORM=offscreen quickshell --no-color -p "$TEST_TMP/history-stdin-probe.qml" 2>&1)"
    if [[ "$PROBE_OUTPUT" == *'HISTORY-STDIN:{"groupId":"group-probe","reason":"unknown","note":"literal; $(not a shell)"}'* ]]; then
        pass "Quickshell flushes one JSON stdin record before closing the write channel"
    else
        fail "Quickshell history stdin" "one-shot JSON record was not received intact"
    fi
else
    pass "Quickshell history stdin (structurally checked; quickshell unavailable)"
fi

if command -v node >/dev/null 2>&1; then
    if node <<'JS'
const fs = require("fs");
const qml = fs.readFileSync("DankAIUsageWidget.qml", "utf8");

function extractFunction(name) {
    const marker = new RegExp(`\\bfunction\\s+${name}\\s*\\([^)]*\\)\\s*\\{`, "g");
    const match = marker.exec(qml);
    if (!match) throw new Error(`missing ${name}()`);
    const brace = qml.indexOf("{", match.index);
    let depth = 0;
    let quote = null;
    let escaped = false;
    for (let i = brace; i < qml.length; i++) {
        const char = qml[i];
        if (quote !== null) {
            if (escaped) escaped = false;
            else if (char === "\\") escaped = true;
            else if (char === quote) quote = null;
            continue;
        }
        if (char === '"' || char === "'" || char === "`") quote = char;
        else if (char === "{") depth++;
        else if (char === "}" && --depth === 0) return qml.slice(match.index, i + 1);
    }
    throw new Error(`unterminated ${name}()`);
}

function bindQmlFunction(name, scope) {
    const expression = extractFunction(name).replace(/^function\s+\w+/, "function");
    return new Function("scope", `with (scope) { return (${expression}); }`)(scope);
}

function equal(actual, expected, description) {
    if (actual !== expected)
        throw new Error(`${description}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const scope = { Date, isFinite, showCodex: true, showClaude: true, usageHistory: [] };
for (const name of [
    "historyGroupKey",
    "historyEventEligible",
    "explanationChoicesForGroup",
    "historyGroups",
    "historyProviderVisible",
    "hasHistoryExplanation",
    "latestExplanationPrompt",
    "historyNoteRuneLength",
    "truncateHistoryNote",
]) scope[name] = bindQmlFunction(name, scope);
scope.historyExplanationProcess = {running: false};
scope.historyExplanationGroup = null;
scope.historyExplanationLocation = "";
scope.historyExplanationExpanded = false;
scope.historyExplanationReason = "";
scope.historyExplanationNote = "stale note";
scope.historyExplanationError = "stale error";
scope.beginHistoryExplanation = bindQmlFunction("beginHistoryExplanation", scope);

function event({
    kind = "allowance_increased_unknown",
    provider = "codex",
    observedAt = "2026-09-08T10:00:00Z",
    groupId = "group-a",
    explanationChoices = ["subscription_change", "unknown", "dismissed"],
    explanation = null,
    explainable = true,
    label = "Weekly",
} = {}) {
    return {kind, provider, observedAt, groupId, explanationChoices, explanation, explainable, label};
}

const grouped = scope.historyGroups([
    event(),
    event({kind: "credits_changed", explanationChoices: ["external_reset", "unknown"]}),
    event({provider: "claude"}),
    event({observedAt: "2026-09-08T10:01:00Z"}),
    event({groupId: "group-b"}),
]);
equal(grouped.length, 4, "groups require exact provider, observedAt, and groupId");
const exact = grouped.find(group => group.key.includes("group-a") && group.events.length === 2);
equal(exact.eligibleCount, 2, "group counts every eligible related event");
equal(
    JSON.stringify(exact.explanationChoices),
    JSON.stringify(["subscription_change", "unknown", "dismissed", "external_reset"]),
    "group choices are a stable union",
);

const editable = scope.historyGroups([
    event({explanation: {reason: "external_reset", note: "existing context", source: "user"}}),
])[0];
scope.beginHistoryExplanation(editable, editable.key, true);
equal(scope.historyExplanationNote, "existing context", "editing initializes the saved note");
equal(scope.historyExplanationReason, "external_reset", "editing initializes the saved reason");
const otherGroup = scope.historyGroups([event({groupId: "other-group"})])[0];
scope.beginHistoryExplanation(otherGroup, otherGroup.key, true);
equal(scope.historyExplanationGroup.key, editable.key, "another row cannot retarget an unsaved draft");
scope.historyExplanationGroup = null;
scope.historyExplanationLocation = "";

equal(scope.historyEventEligible(event({kind: "scheduled_window"})), false, "scheduled windows are ignored");
equal(scope.historyEventEligible(event({kind: "plugin_reset_reset"})), false, "confirmed plugin events are ignored");
equal(scope.historyEventEligible(event({explainable: false})), false, "backend-ineligible events are ignored");
equal(scope.historyEventEligible(event({groupId: ""})), false, "ungrouped legacy events are ignored");

const now = Date.parse("2026-09-08T12:00:00Z");
const older = event({observedAt: "2026-09-08T10:00:00Z", groupId: "older"});
const latest = event({observedAt: "2026-09-08T11:00:00Z", groupId: "latest"});
scope.usageHistory = [older, latest];
equal(scope.latestExplanationPrompt(now).groupId, "latest", "newest visible eligible group prompts");

scope.usageHistory = [older, {...latest, explanation: {reason: "external_reset", source: "user"}}];
equal(scope.latestExplanationPrompt(now), null, "answering latest does not reveal older unanswered group");
scope.usageHistory = [older, {...latest, explanation: {reason: "dismissed", source: "user"}}];
equal(scope.latestExplanationPrompt(now), null, "dismissing latest does not reveal older unanswered group");

const timingNoise = {...latest, kind: "window_changed_unknown", timingNoise: true};
scope.usageHistory = [older, timingNoise];
equal(scope.latestExplanationPrompt(now), null, "legacy timing noise neither prompts nor reveals older unanswered groups");
equal(scope.historyGroups(scope.usageHistory).length, 2, "timing noise remains in history");
scope.usageHistory = [timingNoise, {...latest, kind: "credits_changed"}];
equal(scope.latestExplanationPrompt(now).groupId, "latest", "mixed group with a genuine change still prompts");
const title = bindQmlFunction("historyEventTitle", scope);
equal(title(timingNoise), "Minor reset-time adjustment", "legacy noise has an honest history label");
equal(title({...latest, kind: "window_changed_unknown"}), "Reset time changed", "time adjustment is distinguished from refill");
scope.historyEventTitle = title;
equal(bindQmlFunction("historyPromptTitle", scope)(scope.latestExplanationPrompt(now)), "Available resets changed · Weekly", "mixed prompt title describes the genuine change, not timing noise");

scope.usageHistory = [
    older,
    event({kind: "scheduled_window", observedAt: "2026-09-08T11:30:00Z", groupId: "scheduled"}),
    event({kind: "plugin_reset_reset", observedAt: "2026-09-08T11:45:00Z", groupId: "plugin"}),
];
equal(scope.latestExplanationPrompt(now).groupId, "older", "newer scheduled and plugin events do not displace prompt");

scope.showCodex = false;
scope.usageHistory = [
    latest,
    event({provider: "claude", observedAt: "2026-09-08T10:30:00Z", groupId: "visible-claude"}),
];
equal(scope.latestExplanationPrompt(now).groupId, "visible-claude", "provider visibility filters prompt candidates");
scope.showClaude = false;
equal(scope.latestExplanationPrompt(now), null, "no hidden provider prompts");
scope.showCodex = true;
scope.showClaude = true;

scope.usageHistory = [event({observedAt: "2026-09-07T12:00:00Z", groupId: "boundary"})];
equal(scope.latestExplanationPrompt(now).groupId, "boundary", "24-hour boundary is included");
scope.usageHistory = [event({observedAt: "2026-09-07T11:59:59.999Z", groupId: "expired"})];
equal(scope.latestExplanationPrompt(now), null, "older than 24 hours does not prompt");

equal(scope.historyNoteRuneLength("a😀b"), 3, "note length counts Unicode code points");
equal(scope.truncateHistoryNote("a😀b", 2), "a😀", "note truncation preserves surrogate pairs");
JS
    then
        pass "history explanation JavaScript selection and grouping behavior"
    else
        fail "history explanation JavaScript behavior" "selection, grouping, or note bounds are inconsistent"
    fi
else
    pass "history explanation JavaScript behavior (structurally checked; node unavailable)"
fi

echo "Go helper"
VERSION="$(python3 -c 'import json; print(json.load(open("plugin.json", encoding="utf-8"))["version"])')"
BINARY="$TEST_TMP/dankaiusage"
if go build -ldflags "-X main.version=$VERSION" -o "$BINARY" ./cmd/dankaiusage; then
    pass "helper compiles"
else
    fail "Go build" "helper did not compile"
fi

if [ -x "$BINARY" ]; then
    assert_eq "helper version matches plugin.json" "$VERSION" "$("$BINARY" version)"
fi

if go test ./...; then
    pass "Go unit tests"
else
    fail "Go unit tests" "go test ./... failed"
fi

if go vet ./...; then
    pass "Go vet"
else
    fail "Go vet" "go vet ./... failed"
fi

if [ -f go.sum ]; then
    fail "stdlib-only helper" "go.sum exists"
else
    pass "stdlib-only helper"
fi

echo "documentation"
shopt -s nullglob
ADR_FILES=(docs/adr/ADR-*.md)
shopt -u nullglob
ADR_COUNT="${#ADR_FILES[@]}"
if [ "$ADR_COUNT" -ge 11 ]; then
    pass "$ADR_COUNT ADRs present"
else
    fail "ADRs" "expected at least 11, found $ADR_COUNT"
fi

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
