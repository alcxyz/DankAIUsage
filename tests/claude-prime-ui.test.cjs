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
const minutesFromNow = minutes => new Date(now + minutes * 60000).toISOString();

// Fixed clock for Date.now() and Date.parse(); new Date(...) keeps working.
class FixedDate extends Date {
    static now() { return now; }
}

function claude(overrides = {}) {
    return {
        id: "claude", available: true, meta: {},
        weeklyLeft: {known: true},
        sessionLeft: {known: true, resetAt: minutesFromNow(-60)},
        ...overrides,
    };
}

function makePrimeScope(overrides = {}) {
    const scope = {
        Date: FixedDate, isFinite,
        enableClaudePrime: true, showClaude: true,
        claudePrimeProcess: {running: false},
        lastClaudeAutoPrimeAt: 0, refreshInterval: 300,
        providers: [claude()],
        primeCalls: [], claudePrimeLast: {},
        formatShortDateTime: value => value ? "at " + value : "",
        ...overrides,
    };
    scope.primeClaude = automatic => { scope.primeCalls.push(automatic); };
    scope.claudeProviderData = bindQmlFunction("claudeProviderData", scope);
    scope.claudePrimeIdleReason = bindQmlFunction("claudePrimeIdleReason", scope);
    scope.maybeAutoPrimeClaude = bindQmlFunction("maybeAutoPrimeClaude", scope);
    scope.claudePrimeStatusLine = bindQmlFunction("claudePrimeStatusLine", scope);
    return scope;
}

test("claude prime idle reason explains why the five-hour window cannot be started", () => {
    const scope = makePrimeScope();
    const reason = provider => scope.claudePrimeIdleReason(provider);
    assert.equal(reason(null), "Claude usage unavailable");
    assert.equal(reason(claude({available: false})), "Claude usage unavailable");
    for (const meta of [{limitError: "rate limited"}, {usageDataStale: true}, {usageStale: true}, {usageRefreshPending: true}])
        assert.equal(reason(claude({meta})), "Claude usage unavailable or out of date", JSON.stringify(meta));
    // The helper needs only the five-hour window, and so does the widget.
    assert.equal(reason(claude({weeklyLeft: undefined})), "", "an idle five-hour window without a weekly one is idle");
    assert.equal(reason(claude({weeklyLeft: {known: false}, sessionLeft: {known: false}})), "this account has no five-hour window");
    // The 2.0.0 failure mode: an account without a five-hour window never primes.
    assert.equal(reason(claude({sessionLeft: undefined})), "this account has no five-hour window");
    assert.equal(reason(claude({sessionLeft: {known: false}})), "this account has no five-hour window");
    assert.equal(reason(claude({sessionLeft: {known: true, resetAt: minutesFromNow(5)}})), "five-hour window running");
    assert.equal(reason(claude({sessionLeft: {known: true, resetAt: minutesFromNow(0.5)}})), "five-hour window running",
        "a window resetting in seconds still runs");
    assert.equal(reason(claude({sessionLeft: {known: true}})), "", "known window without a reset time is idle");
    assert.equal(reason(claude({sessionLeft: {known: true, resetAt: minutesFromNow(-5)}})), "", "a past reset is idle");
    assert.equal(reason(claude({sessionLeft: {known: true, resetAt: new Date(now - 500).toISOString()}})), "five-hour window running",
        "a reset kept to whole seconds still runs for the rest of that second");

    // After a prime no usage response showed starting a window, the helper
    // waits five hours; the widget says so and does not ask.
    scope.claudePrimeLast = {reason: "prime_unconfirmed", primedAt: minutesFromNow(-60)};
    assert.match(reason(claude()), /^the last prime started no window; retrying /);
    scope.maybeAutoPrimeClaude();
    assert.deepEqual(scope.primeCalls, [], "no helper call while it would refuse");
    scope.claudePrimeLast = {reason: "prime_unconfirmed", primedAt: minutesFromNow(-301)};
    assert.equal(reason(claude()), "", "five hours later prime may try again");
    scope.claudePrimeLast = {};
});

test("automatic prime is requested only when enabled, shown, idle and due", () => {
    const scope = makePrimeScope();
    scope.maybeAutoPrimeClaude();
    assert.deepEqual(scope.primeCalls, [true], "enabled, shown and idle primes automatically");

    const skipped = (overrides) => {
        const other = makePrimeScope(overrides);
        other.maybeAutoPrimeClaude();
        return other.primeCalls;
    };
    assert.deepEqual(skipped({enableClaudePrime: false}), [], "disabled never primes");
    assert.deepEqual(skipped({showClaude: false}), [], "a hidden Claude card never primes");
    assert.deepEqual(skipped({claudePrimeProcess: {running: true}}), [], "a running prime is not repeated");
    assert.deepEqual(skipped({providers: []}), [], "no Claude provider never primes");
    assert.deepEqual(skipped({providers: [claude({sessionLeft: {known: false}})]}), [], "no five-hour window never primes");
    assert.deepEqual(skipped({providers: [claude({sessionLeft: {known: true, resetAt: minutesFromNow(5)}})]}), [],
        "a running window never primes");
    assert.deepEqual(skipped({providers: [claude({meta: {usageStale: true}})]}), [], "stale usage never primes");
    assert.deepEqual(skipped({lastClaudeAutoPrimeAt: now - 299000}), [], "inside the refresh interval never primes");
    assert.deepEqual(skipped({lastClaudeAutoPrimeAt: now - 300000}), [true], "exactly one refresh interval later is due");
});

test("the prime status line names an account without a five-hour window", () => {
    const scope = makePrimeScope({enableClaudePrime: false});
    assert.equal(scope.claudePrimeStatusLine(), "", "prime off shows nothing");
    scope.enableClaudePrime = true;
    scope.providers = [claude({sessionLeft: {known: false}})];
    assert.equal(scope.claudePrimeStatusLine(), "Claude prime is on · idle: this account has no five-hour window");
    scope.providers = [claude({sessionLeft: {known: true, resetAt: minutesFromNow(5)}})];
    assert.equal(scope.claudePrimeStatusLine(), "Claude prime is on · idle: five-hour window running");
    scope.providers = [claude()];
    assert.equal(scope.claudePrimeStatusLine(), "Claude prime is on · starts the five-hour window when it is idle");
    scope.claudePrimeProcess.running = true;
    assert.equal(scope.claudePrimeStatusLine(), "Claude prime: starting the five-hour window…");
});

function makeSummaryScope() {
    const scope = {
        Date: FixedDate, Qt: {locale: () => undefined}, Locale: {ShortFormat: 2},
        resetClock: 0, capabilities: {}, providers: [], usageHistory: [], historyError: "",
        trackingStatus: {}, grandTotal: {}, hasError: false, errorText: "", lastUpdated: "",
        tokenHistoryRange: "7d", preferredTokenHistoryRange: "7d", autoPrimes: 0,
    };
    scope.maybeAutoPrimeClaude = () => { scope.autoPrimes++; };
    scope.fiveHourTokensAvailable = () => false;
    scope.applySummary = bindQmlFunction("applySummary", scope);
    return scope;
}

test("only a live summary may trigger automatic prime", () => {
    const scope = makeSummaryScope();
    const summary = {providers: [claude()]};
    scope.applySummary(summary, false);
    assert.equal(scope.autoPrimes, 0, "a cached summary never primes");
    scope.applySummary(summary);
    assert.equal(scope.autoPrimes, 0, "a summary without the flag never primes");
    scope.applySummary(summary, true);
    assert.equal(scope.autoPrimes, 1, "a live summary may prime");
});

function makeLoadScope(stored) {
    const scope = {
        pluginId: "dankAIUsage", periodDays: 7,
        tokenHistoryRanges: [{key: "5h"}, {key: "7d"}, {key: "30d"}, {key: "90d"}, {key: "tracked"}],
        historyReadKeys: [], announcementsReadKeys: [], dropdownMode: "simple", tokenHistoryRange: "7d",
        lastClaudeAutoPrimeAt: 0, claudePrimeLast: {},
        pluginService: {
            loadPluginState: (id, key, fallback) => Object.prototype.hasOwnProperty.call(stored, key) ? stored[key] : fallback,
            savePluginState: () => {},
        },
    };
    scope.normalizedReadKeys = bindQmlFunction("normalizedReadKeys", scope);
    scope.resolveDropdownMode = bindQmlFunction("resolveDropdownMode", scope);
    scope.isTokenHistoryRange = bindQmlFunction("isTokenHistoryRange", scope);
    scope.migratedPeriodRange = bindQmlFunction("migratedPeriodRange", scope);
    scope.loadCache = bindQmlFunction("loadCache", scope);
    return scope;
}

test("a cached summary loads without priming", () => {
    const cached = {providers: [claude()]};
    const scope = makeLoadScope({lastSummary: cached});
    const applied = [];
    scope.applySummary = (summary, allowAutoPrime) => { applied.push([summary, allowAutoPrime]); };
    scope.loadCache();
    assert.equal(applied.length, 1, "the cached summary is applied once");
    assert.equal(applied[0][0], cached);
    assert.equal(applied[0][1], false, "loadCache passes allowAutoPrime as false");
});
