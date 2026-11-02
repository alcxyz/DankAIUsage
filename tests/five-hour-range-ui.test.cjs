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

// Fixed clock for Date.now(); new Date(...) keeps working.
class FixedDate extends Date {
    static now() { return now; }
}

const withFiveHours = {id: "claude", rolling: {fiveHours: {total: 1200}, sevenDays: {total: 9000}}};
const withoutFiveHours = {id: "codex", rolling: {sevenDays: {total: 9000}}};

function makeScope(overrides = {}) {
    const saves = [];
    const scope = {
        Date: FixedDate, Qt: {locale: () => undefined}, Locale: {ShortFormat: 2},
        pluginId: "dankAIUsage", periodDays: 7,
        tokenHistoryRanges: [{key: "5h", label: "5h"}, {key: "7d", label: "7d"}, {key: "30d", label: "30d"},
            {key: "90d", label: "90d"}, {key: "tracked", label: "Tracked"}],
        providers: [], tokenHistoryRange: "7d", preferredTokenHistoryRange: "7d", clearTrackingConfirm: true,
        resetClock: 0, capabilities: {}, usageHistory: [], historyError: "", trackingStatus: {},
        grandTotal: {}, hasError: false, errorText: "", lastUpdated: "",
        pluginService: {
            savePluginState: (pluginId, key, value) => { saves.push([key, value]); },
        },
        ...overrides,
    };
    scope.maybeAutoPrimeClaude = () => {};
    scope.fiveHourTokensAvailable = bindQmlFunction("fiveHourTokensAvailable", scope);
    scope.isTokenHistoryRange = bindQmlFunction("isTokenHistoryRange", scope);
    scope.tokenHistoryRangeChoices = bindQmlFunction("tokenHistoryRangeChoices", scope);
    scope.selectTokenHistoryRange = bindQmlFunction("selectTokenHistoryRange", scope);
    scope.applySummary = bindQmlFunction("applySummary", scope);
    return {scope, saves};
}

const keys = choices => choices.map(choice => choice.key);

test("the 5h token range is offered only while a provider reports a five-hour window", () => {
    const {scope} = makeScope({providers: [withoutFiveHours]});
    assert.deepEqual(keys(scope.tokenHistoryRangeChoices()), ["7d", "30d", "90d", "tracked"]);
    scope.providers = [withoutFiveHours, withFiveHours];
    assert.deepEqual(keys(scope.tokenHistoryRangeChoices()), ["5h", "7d", "30d", "90d", "tracked"]);
});

test("selecting 5h is refused without a five-hour window and saved with one", () => {
    const {scope, saves} = makeScope({providers: [withoutFiveHours]});
    scope.selectTokenHistoryRange("5h");
    assert.equal(scope.tokenHistoryRange, "7d", "the range does not change");
    assert.deepEqual(saves, [], "nothing is saved");
    scope.selectTokenHistoryRange("bogus");
    assert.equal(scope.tokenHistoryRange, "7d", "unknown ranges are refused");

    scope.providers = [withFiveHours];
    scope.selectTokenHistoryRange("5h");
    assert.equal(scope.tokenHistoryRange, "5h");
    assert.deepEqual(saves, [["tokenHistoryRange", "5h"]]);
});

test("a chosen 5h range shows 7d without a five-hour window and returns with it", () => {
    const {scope, saves} = makeScope({providers: [withFiveHours], tokenHistoryRange: "5h", preferredTokenHistoryRange: "5h"});
    scope.applySummary({providers: [withoutFiveHours]});
    assert.equal(scope.tokenHistoryRange, "7d", "a summary without a five-hour window shows 7d");
    assert.equal(scope.preferredTokenHistoryRange, "5h", "the choice is kept");

    scope.applySummary({providers: [withFiveHours]});
    assert.equal(scope.tokenHistoryRange, "5h", "the window's return restores 5h in the same session");
    assert.deepEqual(saves, [], "neither step rewrites the saved choice");

    const other = makeScope({providers: [withFiveHours], tokenHistoryRange: "30d", preferredTokenHistoryRange: "30d"}).scope;
    other.applySummary({providers: [withFiveHours]});
    assert.equal(other.tokenHistoryRange, "30d", "other choices are untouched");
});
