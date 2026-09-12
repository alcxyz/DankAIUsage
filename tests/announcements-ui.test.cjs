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

function fakeDate(now) {
    return {parse: Date.parse, now: () => now};
}

function announcement(overrides = {}) {
    return {
        id: "reset-a",
        revision: 1,
        provider: "codex",
        kind: "hard_reset",
        confidence: "verified",
        announcedAt: "2026-09-12T10:00:00Z",
        expectedBy: "2026-09-13T00:00:00Z",
        ...overrides,
    };
}

function makeScope(overrides = {}) {
    const scope = {
        Date,
        isFinite,
        publicResetAnnouncements: true,
        publicAnnouncements: [],
        announcementsStale: false,
        announcementReceipts: {},
        announcementClock: Date.parse("2026-09-12T12:00:00Z"),
        advancedDropdown: false,
        showCodex: true,
        showClaude: true,
        pluginId: "dankAIUsage",
        pluginService: {savePluginState() {}},
        ToastService: {showInfo() {}},
        formatShortDateTime(value) { return value; },
        ...overrides,
    };
    scope.root = scope;
    scope.historyProviderVisible = bindQmlFunction("historyProviderVisible", scope);
    scope.historyEventNeedsNoPrompt = bindQmlFunction("historyEventNeedsNoPrompt", scope);
    scope.announcementUpcoming = bindQmlFunction("announcementUpcoming", scope);
    scope.visibleAnnouncements = bindQmlFunction("visibleAnnouncements", scope);
    scope.notifyUpcomingAnnouncements = bindQmlFunction("notifyUpcomingAnnouncements", scope);
    scope.matchingPublicAnnouncement = bindQmlFunction("matchingPublicAnnouncement", scope);
    return scope;
}

test("multiple distinct nearby public resets do not get an arbitrary local attribution", () => {
    const local = {
        provider: "codex", kind: "allowance_increased_unknown",
        observedAt: "2026-09-12T10:05:00Z", previousObservedAt: "2026-09-12T09:55:00Z",
        before: {usedPercent: 58}, after: {usedPercent: 0},
    };
    const scope = makeScope({publicAnnouncements: [
        announcement({id: "first", expectedBy: null}),
        announcement({id: "second", expectedBy: null}),
    ]});
    assert.equal(scope.matchingPublicAnnouncement({events: [local]}), null);
});

test("upcoming validation rejects past, expired, effective, missing, and malformed dates", () => {
    const scope = makeScope();
    const now = scope.announcementClock;
    assert.equal(scope.announcementUpcoming(announcement(), now), true);
    for (const event of [
        announcement({expectedBy: "2026-09-12T11:59:59Z"}),
        announcement({expectedBy: "not-a-date"}),
        announcement({expectedBy: undefined}),
        announcement({expectedBy: "2026-09-14T12:00:01Z"}),
        announcement({announcedAt: "not-a-date"}),
        announcement({announcedAt: "2026-09-12T12:00:01Z"}),
        announcement({announcedAt: "2026-09-11T11:59:59Z"}),
        announcement({expiresAt: "2026-09-12T12:00:00Z"}),
        announcement({expiresAt: "not-a-date"}),
        announcement({effectiveAt: "2026-09-12T11:00:00Z"}),
        announcement({confidence: "reported"}),
    ]) assert.equal(scope.announcementUpcoming(event, now), false);
});

test("date-only deadlines never manufacture a local notification time", () => {
    const now = Date.parse("2026-09-12T12:00:00Z");
    const formatted = [];
    const toasts = [];
    const exact = announcement({expectedBy: "2026-09-13"});
    const scope = makeScope({
        Date: fakeDate(now),
        publicAnnouncements: [exact],
        formatShortDateTime(value) { formatted.push(value); return `formatted:${value}`; },
        ToastService: {showInfo(title, body) { toasts.push([title, body]); }},
    });
    assert.equal(scope.announcementUpcoming(exact, now), false);
    scope.notifyUpcomingAnnouncements();
    assert.deepEqual(formatted, []);
    assert.deepEqual(toasts, []);
});

test("UTC instants use elapsed time across the DST fallback", () => {
    const scope = makeScope();
    const now = Date.parse("2026-10-23T01:30:00Z");
    const inside = announcement({announcedAt: "2026-10-23T01:30:00Z", expectedBy: "2026-10-25T00:30:00Z"});
    const outside = announcement({announcedAt: "2026-10-23T01:30:00Z", expectedBy: "2026-10-25T02:30:01Z"});
    assert.equal(scope.announcementUpcoming(inside, now), true);
    assert.equal(scope.announcementUpcoming(outside, now), false);
});

test("disabled and stale feeds cannot notify or match", () => {
    for (const overrides of [{publicResetAnnouncements: false}, {announcementsStale: true}]) {
        let saves = 0;
        let toasts = 0;
        const scope = makeScope({
            ...overrides,
            Date: fakeDate(Date.parse("2026-09-12T12:00:00Z")),
            publicAnnouncements: [announcement()],
            pluginService: {savePluginState() { saves++; }},
            ToastService: {showInfo() { toasts++; }},
        });
        scope.notifyUpcomingAnnouncements();
        assert.equal(saves, 0);
        assert.equal(toasts, 0);
        assert.equal(scope.matchingPublicAnnouncement({events: []}), null);
    }
});

test("visible announcements filter hidden providers and cap results", () => {
    const events = [
        announcement({id: "hidden", provider: "claude"}),
        ...Array.from({length: 6}, (_, i) => announcement({id: `shown-${i}`})),
        announcement({id: "expired", expiresAt: "2026-09-12T12:00:00Z"}),
    ];
    const scope = makeScope({showClaude: false, publicAnnouncements: events});
    assert.deepEqual(scope.visibleAnnouncements().map(event => event.id), ["shown-0", "shown-1", "shown-2", "shown-3"]);
    scope.publicResetAnnouncements = false;
    assert.deepEqual(scope.visibleAnnouncements(), []);
});

test("hidden providers never produce alerts", () => {
    let saves = 0;
    let toasts = 0;
    const scope = makeScope({
        Date: fakeDate(Date.parse("2026-09-12T12:00:00Z")),
        showClaude: false,
        publicAnnouncements: [announcement({provider: "claude"})],
        pluginService: {savePluginState() { saves++; }},
        ToastService: {showInfo() { toasts++; }},
    });
    scope.notifyUpcomingAnnouncements();
    assert.equal(saves, 0);
    assert.equal(toasts, 0);
    assert.deepEqual(scope.announcementReceipts, {});
});

test("a fresh feed with a canceled announcement removes display and matching", () => {
    const scope = makeScope({
        publicAnnouncements: [announcement({expectedBy: undefined, announcedAt: "2026-09-12T10:15:00Z"})],
        advancedDropdown: true,
    });
    const group = {events: [{
        provider: "codex",
        kind: "allowance_increased_unknown",
        previousObservedAt: "2026-09-12T10:00:00Z",
        observedAt: "2026-09-12T10:30:00Z",
        before: {usedPercent: 80},
        after: {usedPercent: 10},
    }]};
    assert.equal(scope.visibleAnnouncements().length, 1);
    assert.notEqual(scope.matchingPublicAnnouncement(group), null);

    scope.publicAnnouncements = [];
    assert.deepEqual(scope.visibleAnnouncements(), []);
    assert.equal(scope.matchingPublicAnnouncement(group), null);
});

test("notification receipts deduplicate restarts but permit corrected revisions", () => {
    const now = Date.parse("2026-09-12T12:00:00Z");
    const saves = [];
    const toasts = [];
    const service = {savePluginState(_plugin, _key, value) { saves.push({...value}); }};
    const first = makeScope({
        Date: fakeDate(now), publicAnnouncements: [announcement()], pluginService: service,
        ToastService: {showInfo(_title, body) { toasts.push(body); }},
    });
    first.notifyUpcomingAnnouncements();
    assert.deepEqual(Object.keys(first.announcementReceipts), ["reset-a:1"]);

    const restarted = makeScope({
        Date: fakeDate(now + 1000), announcementReceipts: {...first.announcementReceipts},
        publicAnnouncements: [announcement()], pluginService: service,
        ToastService: {showInfo(_title, body) { toasts.push(body); }},
    });
    restarted.notifyUpcomingAnnouncements();
    assert.equal(toasts.length, 1);
    restarted.publicAnnouncements = [announcement({revision: 2, expectedBy: "2026-09-13T01:00:00Z"})];
    restarted.notifyUpcomingAnnouncements();
    assert.equal(toasts.length, 2);
    assert.deepEqual(Object.keys(restarted.announcementReceipts).sort(), ["reset-a:1", "reset-a:2"]);
    assert.equal(saves.length, 2);
});

test("matching a public reset preserves local history and requires clear, prompt-free evidence", () => {
    const publicEvent = announcement({expectedBy: undefined, announcedAt: "2026-09-12T10:15:00Z"});
    const local = {
        provider: "codex",
        kind: "allowance_increased_unknown",
        previousObservedAt: "2026-09-12T10:00:00Z",
        observedAt: "2026-09-12T10:30:00Z",
        before: {usedPercent: 80},
        after: {usedPercent: 10},
    };
    const group = {events: [local]};
    const before = JSON.stringify(group);
    const scope = makeScope({publicAnnouncements: [publicEvent]});
    assert.equal(scope.matchingPublicAnnouncement(group), publicEvent);
    assert.equal(JSON.stringify(group), before);

    assert.equal(scope.matchingPublicAnnouncement({events: [{...local, kind: "reset_redeemed_inferred"}]}), null);
    assert.equal(scope.matchingPublicAnnouncement({events: [{...local, after: {usedPercent: 90}}]}), null);
    assert.equal(scope.matchingPublicAnnouncement({events: [{...local, observedAt: "2026-09-12T11:00:01Z"}]}), null);
    scope.showCodex = false;
    assert.equal(scope.matchingPublicAnnouncement(group), null);
});
