const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const qml = fs.readFileSync(path.join(__dirname, "..", "DankAIUsageWidget.qml"), "utf8");

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


const now = Date.parse("2026-09-23T18:00:00Z");
const gradientStops = new Function(`return ${qml.match(/readonly property var usageColorStops: (\[[\s\S]*?\n    \])/)[1]}`)();
const Theme = {isLightMode: false, primary: "primary", warning: "warning", error: "error", surfaceVariantText: "muted"};

function makeScope(overrides = {}) {
    const scope = Object.assign({Date, isFinite, Math, Theme, resetClock: now, showUsed: false,
        brandLogoColors: false, barUsageColors: false, usageColorStops: gradientStops,
        Qt: {rgba: (r, g, b) => [r, g, b].map(v => Math.round(v * 100) / 100).join(",")},
        hasError: false, barShowClaudeSession: true, barShowClaudeWeekly: true,
        barShowClaudeCredits: false, barClaudeWeeklyOverrides: {}, providers: []}, overrides);
    scope.root = scope;
    for (const name of ["knownAllowance", "displayPercent", "allowanceLabel", "allowanceSeverity",
            "allowanceColor", "resetTiming", "barFillColor", "providerQuotaBuckets", "claudeBucketShownInBar",
            "providerBarBuckets", "barProviderGroups", "barLabelKind", "barLabelText", "barWindowTag",
            "barQuotaTags", "barLabelTemplate", "barTimeLabel", "usageGradientColor", "providerLogoColor",
            "providerBrandColor"])
        scope[name] = bindQmlFunction(name, scope);
    scope.visibleProviders = () => scope.providers;
    return scope;
}

function bucket(kind, label, windowMinutes, window, percentRemaining = 90, extra = {}) {
    return Object.assign({id: `${label}-${window}`, kind, label, allowance: {known: true, window, windowMinutes,
        percentRemaining, percentUsed: 100 - percentRemaining}}, extra);
}

const session = bucket("short", "5-hour", 300, "session");
const weekly = bucket("weekly", "Weekly", 10080, "weekly");
const fableWeekly = bucket("scoped", "Fable · weekly", 10080, "weekly");
const credits = {id: "credits", kind: "credits", label: "Credits", allowance: {known: true, percentRemaining: 50}};

test("quota bars skip credits and keep Claude's per-quota choices", () => {
    const scope = makeScope();
    const claude = {id: "claude", quotaBuckets: [session, weekly, fableWeekly, credits]};
    const codex = {id: "codex", quotaBuckets: [session, weekly, credits]};
    assert.deepEqual(scope.providerBarBuckets(codex), [session, weekly]);
    scope.barShowClaudeCredits = true;
    scope.barShowCodexCredits = true;
    assert.deepEqual(scope.providerBarBuckets(codex), [session, weekly], "Codex credit preference is for text mode only");
    assert.deepEqual(scope.providerBarBuckets(claude), [session, weekly, fableWeekly]);
    scope.barShowClaudeSession = false;
    scope.barClaudeWeeklyOverrides = {[weekly.id]: false};
    assert.deepEqual(scope.providerBarBuckets(claude), [fableWeekly]);
    scope.providers = [codex, {id: "claude", quotaBuckets: [credits]}];
    const groups = scope.barProviderGroups();
    assert.equal(groups.length, 1, "providers without bar quotas are omitted");
    assert.deepEqual(groups[0].tags, ["5h", "w"]);
});

test("provider names identify quota-bar groups when logos are disabled", () => {
    const label = qml.match(/StyledText\s*\{\s*id: barProviderName\b([^}]+)\}/);
    assert.ok(label, "quota-bar provider name exists");
    const text = label[1].match(/\btext:\s*([^\n]+)/)[1];
    const visible = label[1].match(/\bvisible:\s*([^\n]+)/)[1];
    const evaluate = new Function("root", "barGroup", `return {text: (${text}), visible: (${visible})}`);
    for (const name of ["Claude", "Codex"]) {
        const group = {groupProvider: {name}};
        assert.deepEqual(evaluate({barShowProviderLogos: false}, group), {text: name, visible: true});
        assert.deepEqual(evaluate({barShowProviderLogos: true}, group), {text: name, visible: false});
    }
});

test("bar fill uses the ADR-0018 severity colors", () => {
    const scope = makeScope();
    assert.equal(scope.barFillColor(bucket("short", "5-hour", 300, "session", 80)), "primary");
    assert.equal(scope.barFillColor(bucket("short", "5-hour", 300, "session", 25)), "warning");
    assert.equal(scope.barFillColor(bucket("short", "5-hour", 300, "session", 10)), "error");
    assert.equal(scope.barFillColor({allowance: {known: false}}), "muted");
    assert.equal(scope.barFillColor(null), "muted");
    scope.hasError = true;
    assert.equal(scope.barFillColor(bucket("short", "5-hour", 300, "session", 80)), "error");
});

test("tags come from the window length and scope, growing only on clashes", () => {
    const scope = makeScope();
    assert.equal(scope.barWindowTag({windowMinutes: 300}), "5h");
    assert.equal(scope.barWindowTag({windowMinutes: 60}), "1h");
    assert.equal(scope.barWindowTag({windowMinutes: 1440}), "1d");
    assert.equal(scope.barWindowTag({windowMinutes: 90}), "90m");
    assert.equal(scope.barWindowTag({windowMinutes: 10080}), "w");
    assert.equal(scope.barWindowTag({window: "weekly"}), "w");
    assert.equal(scope.barWindowTag({window: "session"}), "s");
    assert.equal(scope.barWindowTag(null), "?");
    const tags = buckets => scope.barQuotaTags(buckets);
    assert.deepEqual(tags([session, weekly, fableWeekly]), ["5h", "w", "f"]);
    assert.deepEqual(tags([session, weekly, bucket("scoped", "Fable · 5-hour", 300, "session"), fableWeekly]),
        ["5h", "w", "f5h", "f"]);
    assert.deepEqual(tags([weekly, fableWeekly, bucket("scoped", "Foo · weekly", 10080, "weekly")]), ["w", "fa", "fo"]);
    assert.deepEqual(tags([weekly, bucket("scoped", "Wizard · weekly", 10080, "weekly")]), ["w", "wi"]);
    assert.deepEqual(tags([bucket("scoped", "Fable · weekly", 10080, "weekly"), bucket("scoped", "Fable · weekly", 10080, "weekly")]),
        ["fable", "fable"], "identical labels terminate");
    assert.deepEqual(tags([bucket("scoped", "GPT-5.3-Codex-Spark · weekly", 10080, "weekly")]), ["g"]);
});

test("time labels count down, show elapsed time with Used, and never guess", () => {
    const scope = makeScope();
    const at = (minutes, windowMinutes) => ({resetAt: new Date(now + minutes * 60000).toISOString(), windowMinutes});
    assert.equal(scope.barTimeLabel(at(0.5, 300)), "1m");
    assert.equal(scope.barTimeLabel(at(45, 300)), "45m");
    assert.equal(scope.barTimeLabel(at(125, 300)), "2h05");
    assert.equal(scope.barTimeLabel(at(4 * 1440 + 23 * 60, 10080)), "4d23h");
    assert.equal(scope.barTimeLabel(at(2 * 1440, 10080)), "2d");
    assert.equal(scope.barTimeLabel(at(-5, 300)), "now");
    assert.equal(scope.barTimeLabel({}), "");
    scope.showUsed = true;
    assert.equal(scope.barTimeLabel(at(61, 300)), "3h59");
    assert.equal(scope.barTimeLabel(at(400, 300)), "", "reset beyond the window has no elapsed time");
    assert.equal(scope.barTimeLabel(at(61)), "", "unknown window length has no elapsed time");
});

test("labels and their column widths", () => {
    const scope = makeScope();
    assert.equal(scope.barLabelKind("percent"), "percent");
    assert.equal(scope.barLabelKind("bogus"), "none");
    assert.equal(scope.barLabelKind(undefined), "none");
    assert.equal(scope.barLabelText("tag", session, "5h"), "5h");
    assert.equal(scope.barLabelText("percent", session, ""), "90%");
    assert.equal(scope.barLabelText("none", session, "5h"), "");
    assert.equal(scope.barLabelText("percent", null, ""), "");
    const timer = {kind: "short", allowance: {source: "claude-prime local usage"}};
    assert.equal(scope.barLabelTemplate("percent", [session, timer], []), "Timer");
    assert.equal(scope.barLabelTemplate("percent", [session], []), "100%");
    assert.equal(scope.barLabelTemplate("time", [session], []), "00h00");
    assert.equal(scope.barLabelTemplate("tag", [session, weekly], ["5h", "w"]), "5h");
    assert.equal(scope.barLabelTemplate("tag", [session, fableWeekly], ["5h", "f5h"]), "f5h");
});

test("bar mode is off by default and leaves the text pill unchanged", () => {
    assert.match(qml, /property bool barQuotaBars: false/);
    assert.match(qml, /model: root\.barQuotaBars \? \[\] : root\.topBarSegments\(\)/);
    assert.match(qml, /model: root\.barQuotaBars \? root\.barProviderGroups\(\) : \[\]/);
    assert.match(qml, /color: root\.barFillColor\(modelData\)/);
    assert.match(qml, /elide: Text\.ElideNone/);
    const vertical = qml.split("verticalBarPill: Component {", 2)[1].split("popoutContent: Component {", 1)[0];
    assert.doesNotMatch(vertical, /barQuotaBars/, "vertical pill is unchanged");
});

test("brand logo colors and the usage gradient are off by default", () => {
    const scope = makeScope();
    assert.match(qml, /property bool brandLogoColors: false/);
    assert.match(qml, /property bool barUsageColors: false/);
    assert.equal(scope.providerLogoColor({id: "claude", available: true}), "primary");
    assert.equal(scope.barFillColor(bucket("short", "5-hour", 300, "session", 80)), "primary");
});

test("brand logo colors apply only to healthy providers", () => {
    const scope = makeScope({brandLogoColors: true});
    assert.equal(scope.providerLogoColor({id: "claude", available: true}), "#D97757");
    assert.equal(scope.providerLogoColor({id: "codex", available: true}), "#FFFFFF");
    Theme.isLightMode = true;
    try {
        assert.equal(scope.providerLogoColor({id: "codex", available: true}), "#000000");
        assert.equal(scope.providerLogoColor({id: "claude", available: true}), "#D97757");
    } finally {
        Theme.isLightMode = false;
    }
    assert.equal(scope.providerLogoColor({id: "claude", available: false}), "error");
    assert.equal(scope.providerLogoColor({id: "codex", available: true, error: "x"}), "error");
});

test("usage gradient runs green, yellow, orange, dark red over percent used", () => {
    const scope = makeScope({barUsageColors: true});
    const used = percentUsed => scope.usageGradientColor({known: true, percentUsed});
    assert.equal(used(0), "0.26,0.63,0.28");
    assert.equal(used(50), "0.99,0.85,0.21");
    assert.equal(used(75), "0.98,0.55,0");
    assert.equal(used(100), "0.55,0,0");
    assert.equal(used(25), "0.63,0.74,0.25", "interpolates between stops");
    assert.equal(used(-10), used(0), "clamps below 0");
    assert.equal(used(150), used(100), "clamps above 100");
    assert.equal(used(undefined), used(0));
    assert.equal(scope.usageGradientColor({known: false}), "muted");
    assert.equal(scope.usageGradientColor(null), "muted");
    assert.equal(scope.barFillColor(bucket("short", "5-hour", 300, "session", 25)), used(75),
        "enabled gradient replaces severity colors");
    scope.hasError = true;
    assert.equal(scope.barFillColor(bucket("short", "5-hour", 300, "session", 80)), "error");
});
