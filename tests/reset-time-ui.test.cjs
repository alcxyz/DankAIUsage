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
    const scope = {Date, isFinite, Math};
    scope.root = scope;
    scope.resetTiming = bindQmlFunction("resetTiming", scope);
    scope.resetDuration = bindQmlFunction("resetDuration", scope);
    scope.resetCountdown = bindQmlFunction("resetCountdown", scope);
    scope.resetTimeProgress = bindQmlFunction("resetTimeProgress", scope);
    return scope;
}

function allowance(resetAt, windowMinutes) {
    return {resetAt, windowMinutes};
}

const now = Date.parse("2026-09-12T12:00:00Z");

test("invalid reset timestamps have no timing, countdown, or progress", () => {
    const scope = makeScope();
    for (const value of [null, {}, {resetAt: ""}, {resetAt: "not-a-date"}]) {
        assert.equal(scope.resetTiming(value, now), null);
        assert.equal(scope.resetCountdown(value, now, false), "");
        assert.equal(scope.resetTimeProgress(value, now, false), -1);
    }
});

test("duration formatting rounds up minutes and keeps useful day and hour units", () => {
    const scope = makeScope();
    for (const [milliseconds, expected] of [
        [0, "<1m"],
        [59_999, "<1m"],
        [60_000, "1m"],
        [60_001, "2m"],
        [60 * 60_000, "1h"],
        [61 * 60_000, "1h 1m"],
        [24 * 60 * 60_000, "1d"],
        [(24 * 60 + 61) * 60_000, "1d 1h"],
    ]) assert.equal(scope.resetDuration(milliseconds), expected);
});

test("a future reset remains useful when its window duration is unknown", () => {
    const scope = makeScope();
    for (const windowMinutes of [undefined, null, 0, -5, "60", Infinity, NaN]) {
        const value = allowance("2026-09-12T12:30:00Z", windowMinutes);
        const timing = scope.resetTiming(value, now);
        assert.equal(timing.remainingMs, 30 * 60_000);
        assert.equal(timing.progressKnown, false);
        assert.equal(scope.resetCountdown(value, now, true), "Resets in 30m");
        assert.equal(scope.resetTimeProgress(value, now, false), -1);
        assert.equal(scope.resetTimeProgress(value, now, true), -1);
    }
});

test("times before the modeled window do not invent progress", () => {
    const scope = makeScope();
    const value = allowance("2026-09-12T14:00:01Z", 120);
    const timing = scope.resetTiming(value, now);
    assert.equal(timing.progressKnown, false);
    assert.equal(scope.resetCountdown(value, now, true), "Resets in 2h 1m");
    assert.equal(scope.resetTimeProgress(value, now, false), -1);
});

test("known windows report complementary time-left and time-used progress", () => {
    const scope = makeScope();
    const value = allowance("2026-09-12T13:30:00Z", 180);
    assert.equal(scope.resetCountdown(value, now, false), "Resets in 1h 30m");
    assert.equal(scope.resetCountdown(value, now, true), "Window elapsed: 1h 30m");
    assert.equal(scope.resetTimeProgress(value, now, false), 0.5);
    assert.equal(scope.resetTimeProgress(value, now, true), 0.5);

    const later = now + 30 * 60_000;
    assert.ok(Math.abs(scope.resetTimeProgress(value, later, false) - 1 / 3) < Number.EPSILON);
    assert.ok(Math.abs(scope.resetTimeProgress(value, later, true) - 2 / 3) < Number.EPSILON);
});

test("expired resets await a refreshed allowance instead of showing negative time", () => {
    const scope = makeScope();
    const value = allowance("2026-09-12T11:59:59Z", 60);
    for (const used of [false, true]) {
        assert.equal(scope.resetCountdown(value, now, used), "Reset due · awaiting update");
        assert.equal(scope.resetTimeProgress(value, now, used), -1);
    }
});

test("UTC instants use elapsed duration across the DST fallback", () => {
    const scope = makeScope();
    const value = allowance("2026-10-25T03:30:00+01:00", 240);
    const beforeFallback = Date.parse("2026-10-25T02:30:00+02:00");
    assert.equal(scope.resetCountdown(value, beforeFallback, false), "Resets in 2h");
    assert.equal(scope.resetCountdown(value, beforeFallback, true), "Window elapsed: 2h");
    assert.equal(scope.resetTimeProgress(value, beforeFallback, false), 0.5);
    assert.equal(scope.resetTimeProgress(value, beforeFallback, true), 0.5);
});
