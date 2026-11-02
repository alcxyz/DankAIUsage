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
        claudePrimeEnabled: true, showClaude: true,
        claudePrimeProcess: {running: false},
        lastClaudeAutoPrimeAt: 0, refreshInterval: 300,
        providers: [claude()],
        primeCalls: [], claudePrimeLast: {},
        claudePrimeActiveFrom: 0, claudePrimeActiveUntil: 0, claudePrimeMinWeeklyLeft: 10, claudePrimeDailyCap: 5,
        formatShortDateTime: value => value ? "at " + value : "",
        ...overrides,
    };
    scope.primeClaude = automatic => { scope.primeCalls.push(automatic); };
    scope.claudeProviderData = bindQmlFunction("claudeProviderData", scope);
    scope.claudePrimeActiveNow = bindQmlFunction("claudePrimeActiveNow", scope);
    scope.claudePrimeIdleReason = bindQmlFunction("claudePrimeIdleReason", scope);
    scope.maybeAutoPrimeClaude = bindQmlFunction("maybeAutoPrimeClaude", scope);
    scope.claudePrimeStatusLine = bindQmlFunction("claudePrimeStatusLine", scope);
    scope.reconcileClaudePrimeLast = bindQmlFunction("reconcileClaudePrimeLast", scope);
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
    assert.deepEqual(skipped({claudePrimeEnabled: false}), [], "disabled never primes");
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
    const scope = makePrimeScope({claudePrimeEnabled: false});
    assert.equal(scope.claudePrimeStatusLine(), "", "prime off shows nothing");
    scope.claudePrimeEnabled = true;
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
    scope.reconcileClaudePrimeLast = () => {};
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

test("an unconfirmed prime is reported before the owner's limits, as in the helper", () => {
    const hour = new Date(now).getHours();
    const scope = makePrimeScope({claudePrimeActiveFrom: (hour + 1) % 24, claudePrimeActiveUntil: (hour + 2) % 24,
        claudePrimeLast: {reason: "prime_unconfirmed", primedAt: minutesFromNow(-60)}});
    assert.match(scope.claudePrimeIdleReason(claude()), /^the last prime started no window/);
});

test("the owner's prime limits keep the widget from asking, as in the helper", () => {
    const hour = new Date(now).getHours();
    const scope = makePrimeScope({claudePrimeActiveFrom: (hour + 1) % 24, claudePrimeActiveUntil: (hour + 2) % 24});
    assert.match(scope.claudePrimeIdleReason(claude()), /^outside active hours/);
    scope.maybeAutoPrimeClaude();
    assert.deepEqual(scope.primeCalls, [], "no helper call outside active hours");
    scope.claudePrimeActiveFrom = hour;
    scope.claudePrimeActiveUntil = (hour + 1) % 24;
    assert.equal(scope.claudePrimeIdleReason(claude()), "", "inside active hours");
    scope.claudePrimeActiveFrom = (hour + 23) % 24;
    scope.claudePrimeActiveUntil = (hour + 23) % 24;
    assert.equal(scope.claudePrimeActiveNow(), true, "equal hours mean all day");

    assert.equal(scope.claudePrimeIdleReason(claude({weeklyLeft: {known: true, percentRemaining: 5}})),
        "less than 10% of the weekly limit left");
    assert.equal(scope.claudePrimeIdleReason(claude({weeklyLeft: {known: true, percentRemaining: 50}})), "");
    scope.claudePrimeMinWeeklyLeft = 0;
    assert.equal(scope.claudePrimeIdleReason(claude({weeklyLeft: {known: true, percentRemaining: 5}})), "", "0 turns the guard off");

    scope.claudePrimeLast = {reason: "daily_cap", checkedAt: new Date(now).toISOString(), dailyCap: 5};
    assert.equal(scope.claudePrimeIdleReason(claude()), "daily prime limit reached");
    scope.claudePrimeDailyCap = 8;
    assert.equal(scope.claudePrimeIdleReason(claude()), "", "a raised cap lets the widget ask the helper again");
    scope.claudePrimeDailyCap = 5;
    scope.claudePrimeLast = {reason: "daily_cap", checkedAt: new Date(now - 2 * 86400000).toISOString()};
    assert.equal(scope.claudePrimeIdleReason(claude()), "", "a cap from an earlier day has expired");
});

test("prime's opt-in uses a new key, so a 1.x enableClaudePrime value is ignored", () => {
    const root = path.join(__dirname, "..");
    const settingsQml = fs.readFileSync(path.join(root, "DankAIUsageSettings.qml"), "utf8");
    const manifest = JSON.parse(fs.readFileSync(path.join(root, "plugin.json"), "utf8"));
    for (const [name, text] of [["widget", qml], ["settings", settingsQml], ["plugin.json", JSON.stringify(manifest)]])
        assert.equal(text.includes("enableClaudePrime"), false, `${name} must not read the 1.x key`);
    assert.match(qml, /claudePrimeEnabled = pluginService\.loadPluginData\(pluginId, "claudePrimeEnabled", false\) === true/);
    assert.match(settingsQml, /settingKey: "claudePrimeEnabled"[\s\S]{0,400}defaultValue: false/);
    assert.deepEqual(manifest.settings_schema.claudePrimeEnabled, {type: "boolean", default: false});
});

test("only a live summary reconciles a remembered prime answer", () => {
    const scope = makeSummaryScope();
    let reconciled = 0;
    scope.reconcileClaudePrimeLast = () => { reconciled++; };
    scope.applySummary({providers: []}, false);
    assert.equal(reconciled, 0, "a cached summary never reconciles");
    scope.applySummary({providers: []}, true);
    assert.equal(reconciled, 1);
});

test("a running window clears a remembered unconfirmed prime, and a missing CLI is named", () => {
    const saved = [];
    const running = claude({sessionLeft: {known: true, resetAt: minutesFromNow(120)}});
    const scope = makePrimeScope({
        providers: [running], pluginId: "dankAIUsage",
        pluginService: {savePluginState: (id, key, value) => saved.push([key, value])},
        claudePrimeLast: {reason: "prime_unconfirmed", primedAt: minutesFromNow(-60), message: "No usage response showed..."},
    });
    scope.reconcileClaudePrimeLast();
    assert.equal(scope.claudePrimeLast.reason, "window_running");
    assert.equal(saved.length, 1, "the reconciled answer is saved");
    // After a limit reset clears that window, prime is no longer held back.
    scope.providers = [claude()];
    assert.equal(scope.claudePrimeIdleReason(scope.providers[0]), "");

    // Nothing to reconcile without a running window.
    scope.claudePrimeLast = {reason: "prime_unconfirmed", primedAt: minutesFromNow(-60)};
    scope.reconcileClaudePrimeLast();
    assert.equal(scope.claudePrimeLast.reason, "prime_unconfirmed");

    // A stale or errored summary does not reconcile.
    scope.providers = [claude({sessionLeft: {known: true, resetAt: minutesFromNow(120)}, meta: {usageDataStale: true}})];
    scope.reconcileClaudePrimeLast();
    assert.equal(scope.claudePrimeLast.reason, "prime_unconfirmed", "stale usage keeps the answer");
    scope.providers = [claude()];

    scope.claudePrimeLast = {reason: "cli_missing", message: "Claude CLI not found"};
    assert.match(scope.claudePrimeStatusLine(), /claude CLI was not found/);
    assert.equal(scope.claudePrimeIdleReason(scope.providers[0]), "", "a missing CLI does not stop later checks");
    scope.capabilities = {claudeCli: false};
    scope.reconcileClaudePrimeLast();
    assert.equal(scope.claudePrimeLast.reason, "cli_missing");
    scope.capabilities = {claudeCli: true};
    scope.reconcileClaudePrimeLast();
    assert.equal(scope.claudePrimeLast.reason, "", "a summary that finds the CLI clears the notice");
    assert.doesNotMatch(scope.claudePrimeStatusLine(), /not found/);
});
