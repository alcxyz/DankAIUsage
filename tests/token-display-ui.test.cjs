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

function makeScope() {
    const scope = {
        Math,
        includeCachedTokens: false,
        providers: [],
        tokenHistoryRange: "7d",
        trackingStatus: {errors: []},
    };
    scope.root = scope;
    scope.visibleProviders = () => scope.providers;
    scope.tokenHistoryAvailable = provider => provider.available === true;
    scope.tokenHistoryTotals = provider => provider.totals;
    for (const name of [
        "formatTokens",
        "displayTotal",
        "displayInput",
        "displayCached",
        "displayOutput",
        "filteredGrandInput",
        "filteredGrandCached",
        "filteredGrandOutput",
        "inputTokenLabel",
        "cachedTokenLabel",
        "outputTokenLabel",
        "filteredGrandTokenBreakdown",
        "providerTokenBreakdown",
    ]) scope[name] = bindQmlFunction(name, scope);
    return scope;
}

test("provider-native totals split fresh input, cached input, and output", () => {
    const scope = makeScope();
    const codex = {total: 100, input: 80, cached: 60, output: 20};
    const claude = {total: 160, input: 80, cached: 60, output: 20};

    assert.deepEqual(
        [scope.displayInput(codex), scope.displayCached(codex), scope.displayOutput(codex)],
        [20, 60, 20],
    );
    assert.deepEqual(
        [scope.displayInput(claude), scope.displayCached(claude), scope.displayOutput(claude)],
        [80, 60, 20],
    );
    assert.equal(scope.providerTokenBreakdown({available: true, totals: codex}),
        "20 in / 60 cached / 20 out");
});

test("include-cached setting changes totals but never the component split", () => {
    const scope = makeScope();
    const totals = {total: 160, input: 80, cached: 60, output: 20};

    assert.equal(scope.displayTotal(totals), 100);
    const split = [scope.displayInput(totals), scope.displayCached(totals), scope.displayOutput(totals)];
    scope.includeCachedTokens = true;
    assert.equal(scope.displayTotal(totals), 160);
    assert.deepEqual(
        [scope.displayInput(totals), scope.displayCached(totals), scope.displayOutput(totals)],
        split,
    );
});

test("mixed and partial provider summaries sum every component independently", () => {
    const scope = makeScope();
    scope.providers = [
        {available: true, totals: {total: 100, cached: 60, output: 20}},
        {available: true, totals: {total: 160, cached: 60, output: 20}},
        {available: false, meta: {tokenDataError: "unknown provider data"}},
    ];

    assert.equal(scope.filteredGrandInput(), 100);
    assert.equal(scope.filteredGrandCached(), 120);
    assert.equal(scope.filteredGrandOutput(), 40);
    assert.equal(scope.filteredGrandTokenBreakdown(), "100 in / 120 cached / 40 out (partial)");
});

test("missing totals and provider names do not fabricate token components", () => {
    const scope = makeScope();
    assert.equal(scope.displayInput(null), 0);
    assert.equal(scope.displayCached(null), 0);
    assert.equal(scope.displayOutput(null), 0);

    const unknownProvider = {available: true, totals: {total: 45, cached: 15, output: 10}};
    assert.equal(scope.providerTokenBreakdown(unknownProvider), "20 in / 15 cached / 10 out");
});

test("date labels delegate ordering and clock conventions to the Qt locale", () => {
    const shortDateTime = extractFunction("formatShortDateTime");
    const reset = extractFunction("formatReset");

    assert.match(shortDateTime,
        /d\.toLocaleString\(Qt\.locale\(\),\s*Locale\.ShortFormat\)/);
    assert.match(reset,
        /d\.toLocaleString\(Qt\.locale\(\),\s*Locale\.ShortFormat\)/);
    assert.match(reset,
        /d\.toLocaleTimeString\(Qt\.locale\(\),\s*Locale\.ShortFormat\)/);
    for (const source of [shortDateTime, reset]) {
        assert.doesNotMatch(source, /getMonth|getDate|\["Sun",\s*"Mon"/);
    }
});

function makeQuotaScope(overrides = {}) {
    const scope = {Math, showUsed: false, resetClock: 0, ...overrides};
    scope.root = scope;
    scope.resetCountdown = () => "";
    for (const name of [
        "knownAllowance", "displayPercent", "allowanceLabel", "allowanceDetail",
        "balanceOnlyBucket", "quotaValue", "bucketBarValue", "quotaDetail",
    ]) scope[name] = bindQmlFunction(name, scope);
    return scope;
}

test("a prepaid credit balance renders its amount instead of a percentage", () => {
    const scope = makeQuotaScope();
    const balance = {kind: "credits", allowance: {known: false, unit: "currency"}, valueLabel: "$12.50", detail: "$12.50 prepaid balance"};
    assert.equal(scope.balanceOnlyBucket(balance), true);
    assert.equal(scope.quotaValue(balance), "$12.50");
    assert.equal(scope.bucketBarValue(balance), "$12.50");
    assert.equal(scope.quotaDetail(balance), "$12.50 prepaid balance");
    scope.showUsed = true;
    assert.equal(scope.quotaDetail(balance), "$12.50 prepaid balance");

    const limited = {kind: "credits", allowance: {known: true, percentUsed: 41, percentRemaining: 59}, valueLabel: "$41.00 / $100.00", detail: "$59.00 remaining"};
    assert.equal(scope.balanceOnlyBucket(limited), false);
    assert.equal(scope.quotaValue(limited), "41% used");
    assert.equal(scope.quotaDetail(limited), "$41.00 / $100.00 used");
    scope.showUsed = false;
    assert.equal(scope.bucketBarValue(limited), "59%");

    const unknown = {kind: "weekly", allowance: {known: false}};
    assert.equal(scope.balanceOnlyBucket(unknown), false);
    assert.equal(scope.quotaValue(unknown), "--");
});
