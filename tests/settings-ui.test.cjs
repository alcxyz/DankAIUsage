const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const repo = path.join(__dirname, "..");
const qml = fs.readFileSync(path.join(repo, "DankAIUsageSettings.qml"), "utf8");
const schema = JSON.parse(fs.readFileSync(path.join(repo, "plugin.json"), "utf8")).settings_schema;

// Every QML object (`Type {` … `}`) with its brace span and nesting depth.
// Function bodies, handlers and JS object literals are skipped because their
// opening brace is not preceded by a capitalised type name.
function objectSpans(source) {
    const spans = [];
    const stack = [];
    let depth = 0;
    let quote = null;
    let escaped = false;
    let lineComment = false;
    for (let i = 0; i < source.length; i++) {
        const char = source[i];
        if (lineComment) {
            if (char === "\n") lineComment = false;
            continue;
        }
        if (quote !== null) {
            if (escaped) escaped = false;
            else if (char === "\\") escaped = true;
            else if (char === quote) quote = null;
            continue;
        }
        if (char === "/" && source[i + 1] === "/") { lineComment = true; continue; }
        if (char === '"' || char === "'" || char === "`") { quote = char; continue; }
        if (char === "{") {
            depth++;
            const lineStart = source.lastIndexOf("\n", i) + 1;
            const before = source.slice(lineStart, i);
            const typed = before.match(/(?:^|[:\s])([A-Z][\w.]*)\s*$/);
            const span = typed ? {type: typed[1], start: i, end: -1, depth, parent: stack.length ? stack[stack.length - 1] : null} : null;
            if (span) spans.push(span);
            stack.push(span);
        } else if (char === "}") {
            const span = stack.pop();
            if (span) span.end = i;
            depth--;
        }
    }
    assert.equal(depth, 0, "balanced braces");
    return spans;
}

const spans = objectSpans(qml);
const rootSpan = spans.find(span => span.type === "PluginSettings");
assert.ok(rootSpan, "settings root is a PluginSettings");
const groupComponent = spans.find(span => span.type === "Column" && /component SettingsGroup:\s*Column\s*$/.test(qml.slice(qml.lastIndexOf("\n", span.start) + 1, span.start)));
assert.ok(groupComponent, "SettingsGroup inline component exists");

function enclosing(offset) {
    const chain = [];
    let innermost = null;
    for (const span of spans) {
        if (span.start < offset && span.end > offset && (!innermost || span.start > innermost.start)) innermost = span;
    }
    for (let span = innermost; span; span = span.parent) chain.push(span);
    return chain;
}

function isGroup(span) {
    return span.type === "SettingsGroup";
}

function spanText(span) {
    return qml.slice(span.start, span.end + 1);
}

function extractFunction(name, source) {
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

function bindQmlFunction(name, scope, source) {
    const expression = extractFunction(name, source).replace(/^function\s+\w+/, "function");
    return new Function("scope", `with (scope) { return (${expression}); }`)(scope);
}

const settingControls = [...qml.matchAll(/settingKey: "([^"]+)"/g)].map(match => ({
    key: match[1], offset: match.index, chain: enclosing(match.index),
}));

test("every persisted setting is exposed exactly once and only through groups", () => {
    const keys = settingControls.map(control => control.key);
    for (const key of Object.keys(schema)) {
        if (key === "barClaudeWeeklyOverrides") {
            assert.ok(qml.includes('root.saveValue("barClaudeWeeklyOverrides", overrides)'), "dynamic weekly choices save by bucket id");
            continue;
        }
        assert.equal(keys.filter(k => k === key).length, 1, `${key} is exposed once`);
    }
    for (const key of keys) assert.ok(key in schema, `${key} is a declared setting`);
    for (const control of settingControls) {
        const [self, ...ancestors] = control.chain;
        const containers = ancestors.filter(span => span !== rootSpan);
        assert.ok(containers.length >= 1, `${control.key} sits inside a group`);
        for (const container of containers)
            assert.ok(isGroup(container), `${control.key} is only nested in SettingsGroups, found ${container.type}`);
        assert.ok(ancestors.includes(rootSpan), `${control.key} belongs to the settings page`);
        assert.ok(self.type !== "SettingsGroup", `${control.key} is a control`);
    }
});

test("groups forward loadValue to nested controls without acting as a settings root", () => {
    const component = spanText(groupComponent);
    assert.doesNotMatch(component, /saveValue/, "a group must never look like the settings root to findSettings");
    assert.match(component, /property var loadValue: function\(\) \{ group\.reloadContentValues\(\) \}/);
    assert.match(component, /default property alias content: groupContent\.data/);
    assert.match(component, /visible: !group\.collapsible \|\| group\.expanded/, "collapsed content is hidden, not destroyed");
    assert.doesNotMatch(component, /Loader|Component\.onDestruction|destroy\(/, "collapsing never re-creates controls");
    assert.match(component, /width: parent\.width/, "groups keep the page width");

    const calls = [];
    const children = [
        {loadValue: () => calls.push("toggle")},
        {},
        null,
        {loadValue: () => calls.push("nested-group")},
    ];
    const reload = bindQmlFunction("reloadContentValues", {group: {}, groupContent: {children}}, component);
    reload();
    assert.deepEqual(calls, ["toggle", "nested-group"], "reload reaches every child that can load and skips the rest");
    assert.doesNotMatch(component, /\.visible\b[^\n]*loadValue|expanded[^\n]*loadValue/, "reload does not depend on visibility");
});

test("direct children of the page are groups or already reloadable", () => {
    const direct = spans.filter(span => span.parent === rootSpan && span !== groupComponent);
    for (const span of direct) {
        if (["Process", "Timer", "Connections", "SplitParser"].includes(span.type)) continue;
        assert.ok(isGroup(span), `${span.type} at the top level must be a SettingsGroup`);
    }
    const nestedLoaders = ["weeklyQuotaSettings", "refreshIntervalSetting"];
    for (const id of nestedLoaders) {
        const offset = qml.indexOf(`id: ${id}`);
        assert.ok(offset > 0, `${id} exists`);
        const [self, ...ancestors] = enclosing(offset);
        assert.match(spanText(self), /loadValue/, `${id} exposes loadValue for the group forwarder`);
        assert.ok(ancestors.filter(span => span !== rootSpan).every(isGroup), `${id} is nested only in groups`);
    }
});

test("no new persisted writes: hidden or collapsed groups never save, reset, or default", () => {
    const saves = [...qml.matchAll(/saveValue\(/g)].length;
    assert.equal(saves, 3, "only the weekly overrides and the two refresh-interval writes save values");
    assert.equal([...qml.matchAll(/savePluginState\(/g)].length, 1, "only the reset revision writes plugin state");
    assert.doesNotMatch(qml, /saveState\(/);
    assert.doesNotMatch(qml, /onExpandedChanged|onVisibleChanged:[^\n]*save/);
    assert.equal([...qml.matchAll(/onVisibleChanged/g)].length, 1, "only the root visibility handler exists");
    assert.match(qml, /onVisibleChanged: if \(visible && !codexResetProcess\.running\) root\.runCodexReset\("status"\)/);
    assert.doesNotMatch(qml, /expanded[^\n]*(saveValue|loadValue|settingKey)/, "expanded state is view-only");
    for (const control of settingControls) {
        const text = spanText(control.chain[0]);
        assert.doesNotMatch(text, /onVisibleChanged|onEnabledChanged|\bvalue\s*=/, `${control.key} does not react to visibility`);
    }
});

test("quota bar options follow the bar mode toggle without touching values", () => {
    const barToggle = settingControls.find(control => control.key === "barQuotaBars");
    assert.match(spanText(barToggle.chain[0]), /id: barQuotaBarsToggle/);
    const compact = settingControls.find(control => control.key === "compactPill");
    assert.match(spanText(compact.chain[0]), /visible: !barQuotaBarsToggle\.value/, "compact pill applies to the text pill only");
    const barOptions = ["barQuotaBarWidth", "barUsageColors", "barLabelLeft", "barLabelRight", "barPaceMarker"];
    const optionGroups = new Set();
    for (const key of barOptions) {
        const control = settingControls.find(c => c.key === key);
        const group = control.chain[1];
        assert.ok(isGroup(group) && group.parent && isGroup(group.parent), `${key} lives in the nested bar-option group`);
        optionGroups.add(group);
    }
    assert.equal(optionGroups.size, 1, "bar options share one nested group");
    const [group] = optionGroups;
    const header = qml.slice(group.start, qml.indexOf("SliderSetting", group.start));
    assert.match(header, /visible: barQuotaBarsToggle\.value/);
    assert.match(header, /nested: true/);
    assert.doesNotMatch(header, /collapsible: true/);
    for (const key of ["barShowClaudeCredits", "barShowCodexCredits", "barShowClaudeSession", "barShowClaudeWeekly"]) {
        const control = settingControls.find(c => c.key === key);
        assert.doesNotMatch(spanText(control.chain[0]), /visible:/, `${key} keeps its saved value visible in both bar modes`);
    }
});

test("armed Codex reset stays discoverable and the collapsed group holds only compatibility options", () => {
    const resetOffset = qml.indexOf("id: codexAutoResetToggle");
    assert.ok(resetOffset > 0);
    for (const span of enclosing(resetOffset)) {
        if (!isGroup(span)) continue;
        const header = qml.slice(span.start, qml.indexOf("Column", span.start));
        assert.doesNotMatch(header, /collapsible: true|expanded: false|visible:/, "the reset control is never collapsed or hidden");
    }
    assert.match(qml, /running: root\.visible/, "status polling still follows page visibility");
    assert.match(qml, /Cancel auto reset/);
    assert.match(qml, /root\.codexResetStatus\.armed === true \? Theme\.warning/);
    assert.match(qml, /The feed host sees your IP address/);

    const collapsed = spans.filter(span => isGroup(span) && /expanded: false/.test(qml.slice(span.start, span.end)));
    assert.equal(collapsed.length, 1, "exactly one group starts collapsed");
    assert.match(qml.slice(collapsed[0].start, collapsed[0].end), /collapsible: true/);
    const collapsedKeys = settingControls.filter(c => c.chain.includes(collapsed[0])).map(c => c.key).sort();
    assert.deepEqual(collapsedKeys, ["includeCachedTokens", "periodDays"]);
});

test("test.sh's refresh migration extraction keeps finding the refresh-interval loadValue first", () => {
    const first = qml.search(/\bfunction\s+loadValue\s*\(/);
    assert.ok(first > 0, "a named loadValue exists");
    const [self] = enclosing(first);
    assert.match(spanText(self), /id: refreshIntervalSetting/);
    const migrationSaves = [];
    const scope = {
        refreshSeconds: 300,
        normalizedSeconds: bindQmlFunction("normalizedSeconds", {}, qml),
        root: {pluginService: {}, loadValue() { return 30; }, saveValue(key, value) { migrationSaves.push([key, value]); }},
    };
    bindQmlFunction("loadValue", scope, qml.slice(qml.indexOf("id: refreshIntervalSetting")))();
    assert.equal(scope.refreshSeconds, 180);
    assert.deepEqual(migrationSaves, [["refreshInterval", 180]]);
});

test("defaults and ranges are unchanged from the manifest", () => {
    for (const control of settingControls) {
        const text = spanText(control.chain[0]);
        const declared = schema[control.key];
        const defaultMatch = text.match(/defaultValue: ("[^"]*"|true|false|-?\d+)/);
        if (control.key === "refreshInterval") {
            assert.match(text, /minimum: 3\b/);
            assert.match(text, /maximum: 60\b/);
            assert.match(text, /Math\.max\(180, Math\.min\(3600/);
            continue;
        }
        assert.ok(defaultMatch, `${control.key} declares a default`);
        assert.equal(JSON.parse(defaultMatch[1]), declared.default, `${control.key} default matches plugin.json`);
        if (declared.type === "integer") {
            assert.match(text, new RegExp(`minimum: ${declared.minimum}\\b`));
            assert.match(text, new RegExp(`maximum: ${declared.maximum}\\b`));
        }
        if (declared.enum) {
            for (const value of declared.enum) assert.match(qml, new RegExp(`value: "${value}"`));
        }
    }
});
