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

function makeScope() {
    const scope = {Date, isFinite, Math, Infinity, resetClock: now,
        formatShortDateTime: value => `<${value}>`};
    scope.root = scope;
    for (const name of ["providerResets", "providerResetsByExpiry", "providerResetCommonTitle",
            "resetExpiryLine", "resetDuration", "providerResetSummary", "providerResetDetail"])
        scope[name] = bindQmlFunction(name, scope);
    return scope;
}

function reset(hours, title) {
    return {title, expiresAt: new Date(now + hours * 3600000).toISOString()};
}

test("resets are listed soonest expiry first with unreported expiries last", () => {
    const scope = makeScope();
    const provider = {resets: [reset(50, "B"), {title: "C"}, reset(2, "A"), {title: "D", expiresAt: "bad"}]};
    const order = scope.providerResetsByExpiry(provider).map(item => item.title);
    assert.deepEqual(order.slice(0, 2), ["A", "B"]);
    assert.deepEqual(order.slice(2).sort(), ["C", "D"]);
    assert.deepEqual(provider.resets.map(item => item.title), ["B", "C", "A", "D"], "source order is not mutated");
    assert.deepEqual(scope.providerResetsByExpiry({resets: null}), []);
});

test("expiry lines use the shared duration format", () => {
    const scope = makeScope();
    const soon = reset(24.5, "Full reset");
    assert.equal(scope.resetExpiryLine(soon, false), `Expires <${soon.expiresAt}> · in 1d 0h 30m`);
    assert.equal(scope.resetExpiryLine(soon, true), `Expires <${soon.expiresAt}> · in 1d 0h 30m · Full reset`);
    const past = reset(-1, "Full reset");
    assert.equal(scope.resetExpiryLine(past, false), `Expires <${past.expiresAt}> · expired`);
    assert.equal(scope.resetExpiryLine({title: "Full reset"}, true), "Expiry not reported · Full reset");
    assert.equal(scope.resetExpiryLine({}, true), "Expiry not reported");
});

test("a title shared by every reset is reported once", () => {
    const scope = makeScope();
    assert.equal(scope.providerResetCommonTitle({resets: [reset(1, "Full"), reset(2, "Full")]}), "Full");
    assert.equal(scope.providerResetCommonTitle({resets: [reset(1, "Full"), reset(2, "Half")]}), "");
    assert.equal(scope.providerResetCommonTitle({resets: [reset(1, "Full"), reset(2)]}), "");
    assert.equal(scope.providerResetCommonTitle({resets: []}), "");
});

test("a single reset keeps the existing summary and next-expiry detail", () => {
    const scope = makeScope();
    const only = reset(5, "Full reset");
    assert.equal(scope.providerResetSummary({resets: [only]}), "Full reset available");
    assert.equal(scope.providerResetDetail({resets: [only]}), `Next expiry · <${only.expiresAt}>`);
    assert.equal(scope.providerResetSummary({resets: [only, reset(6, "Full reset")]}), "2 usage resets available");
});

test("the expiry list is Advanced-only and replaces the single detail for several resets", () => {
    assert.match(qml, /visible: root\.advancedDropdown && root\.providerResets\(modelData\)\.length > 1/);
    assert.match(qml, /model: root\.advancedDropdown \? root\.providerResetsByExpiry\(modelData\) : \[\]/);
    assert.match(qml, /text: root\.resetExpiryLine\(modelData, resetExpiryList\.showTitles\)/);
});
