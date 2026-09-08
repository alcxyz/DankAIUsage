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
assert 'id: providerContent' in component_text
assert 'height: providerContent.implicitHeight' in component_text

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
ADR_COUNT="$(rg --files docs/adr -g 'ADR-*.md' | wc -l | tr -d '[:space:]')"
if [ "$ADR_COUNT" -ge 10 ]; then
    pass "$ADR_COUNT ADRs present"
else
    fail "ADRs" "expected at least 10, found $ADR_COUNT"
fi

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
