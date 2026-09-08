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
assert '["dankaiusage", "codex-reset", action]' in component_text
assert 'runCodexReset("status")' in component_text
assert 'root.showCodex ? "check" : "status"' in component_text
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

schema = plugin["settings_schema"]
for key in schema:
    assert f'"{key}"' in component_text, f"component does not load {key}"
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
assert completed.group(1).index("loadCache()") < completed.group(1).index("refreshUsage()")

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
    'visible: root.advancedDropdown && (root.showCodex || root.showClaude)',
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

const resolve = bindQmlFunction("resolveDropdownMode", {});
equal(resolve("simple", {providers: []}), "simple", "persisted simple wins");
equal(resolve("advanced", null), "advanced", "persisted advanced wins");
equal(resolve("", {providers: []}), "advanced", "existing cached user migrates to advanced");
equal(resolve("invalid", {providers: {}}), "advanced", "invalid persisted value migrates from cache");
equal(resolve(undefined, {}), "simple", "new user defaults to simple");
equal(resolve(null, null), "simple", "missing state defaults to simple");

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
