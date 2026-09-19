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
        showUsed: false,
        showCodex: true,
        showClaude: true,
        historyShowOther: true,
        historyShowScheduledShort: false,
        historyShowScheduledWeekly: false,
        usageHistory: [],
        formatShortDateTime: value => value,
    };
    scope.root = scope;
    for (const name of [
        "historyGroupKey", "historyEventEligible", "explanationChoicesForGroup", "historyGroups",
        "historyProviderVisible", "hasHistoryExplanation", "historyEventNeedsNoPrompt", "latestExplanationPrompt",
        "historyMatchesFilters", "visibleHistory",
        "mergePluginResetHistoryGroups", "visibleHistoryGroups", "historyTimeOnlyChange", "historyEventTitle",
        "historyPercent", "historyEventDetail",
    ]) scope[name] = bindQmlFunction(name, scope);
    return scope;
}

const actionAt = "2026-09-19T14:24:00Z";
const observedAt = "2026-09-19T14:29:00Z";

function action(overrides = {}) {
    return {
        provider: "codex", kind: "plugin_reset_reset", observedAt: actionAt,
        source: "plugin_reset", confidence: "confirmed", explainable: false,
        message: "The plugin received a confirmed reset outcome.", ...overrides,
    };
}

function linked(kind = "allowance_increased_unknown", overrides = {}) {
    return {
        provider: "codex", kind, observedAt, previousObservedAt: actionAt,
        groupId: "observation", confidence: "inferred", explainable: true,
        pluginResetAt: actionAt,
        before: {usedPercent: 98, availableCredits: 1},
        after: {usedPercent: 0, availableCredits: 0},
        message: "Possible provider reset; manual reset or account change cannot be ruled out.",
        ...overrides,
    };
}

test("linked observations use factual plugin-reset titles and details", () => {
    const scope = makeScope();
    const refill = linked();
    assert.equal(scope.historyEventTitle(refill), "Refill observed after plugin reset");
    const detail = scope.historyEventDetail(refill);
    assert.match(detail, /Observed 2026-09-19T14:29:00Z/);
    assert.match(detail, /2% left → 100% left/);
    assert.match(detail, /Available resets: 1 → 0/);
    assert.match(detail, /allowance refill was observed after the plugin applied a reset/);
    assert.match(detail, /Plugin reset applied 2026-09-19T14:24:00Z/);
    assert.doesNotMatch(detail, /Possible provider reset/);

    const credits = linked("credits_changed");
    assert.equal(scope.historyEventTitle(credits), "Reset credit decrease after plugin reset");
    assert.match(scope.historyEventDetail(credits), /available reset count changed after the plugin applied a reset/);
    assert.equal(scope.historyEventNeedsNoPrompt(credits), true);
});

test("linked refill and inferred redemption never prompt, without revealing an older prompt", () => {
    const scope = makeScope();
    assert.equal(scope.historyEventNeedsNoPrompt(linked()), true);
    assert.equal(scope.historyEventNeedsNoPrompt(linked("reset_redeemed_inferred")), true);
    const malformed = linked("credits_changed", {pluginResetAt: ""});
    assert.equal(scope.historyEventNeedsNoPrompt(malformed), false);

    scope.usageHistory = [
        linked("credits_changed"),
        linked("credits_changed", {
            observedAt: "2026-09-19T13:00:00Z", groupId: "older-unanswered", pluginResetAt: "",
        }),
    ];
    assert.equal(scope.latestExplanationPrompt(Date.parse("2026-09-19T14:30:00Z")), null);
});

test("display grouping pulls an exact confirmed action from outside the latest eight", () => {
    const scope = makeScope();
    const explanation = {reason: "not_sure", note: "Kept on observation"};
    const observation = linked("allowance_increased_unknown", {explanation});
    const relatedUnlinked = {
        provider: "codex", kind: "window_changed_unknown", observedAt,
        groupId: "observation", confidence: "observed", explainable: true,
        before: {resetAt: "2026-09-20T00:00:00Z"},
        after: {resetAt: "2026-09-21T00:00:00Z"},
    };
    const expiryExplainedCredit = linked("credits_changed", {
        groupId: undefined, explainable: false, expiryExplained: true,
    });
    const fillers = Array.from({length: 7}, (_, index) => ({
        provider: "codex", kind: "credits_changed", explainable: true,
        groupId: `filler-${index}`,
        observedAt: new Date(Date.parse("2026-09-19T14:28:30Z") - index * 30000).toISOString(),
    }));
    const confirmedAction = action();
    scope.usageHistory = [observation, relatedUnlinked, expiryExplainedCredit, ...fillers, confirmedAction];
    assert.equal(scope.visibleHistory().includes(confirmedAction), false);

    const groups = scope.visibleHistoryGroups();
    const combined = groups.find(group => group.key.includes("observation"));
    assert.ok(combined);
    assert.deepEqual(combined.events.map(event => event.kind),
        ["plugin_reset_reset", "allowance_increased_unknown", "window_changed_unknown", "credits_changed"]);
    assert.equal(combined.groupId, "observation");
    assert.equal(combined.explanation, explanation);
    assert.equal(groups.some(group => group.events.length === 1
        && group.events[0] === confirmedAction), false);
    assert.equal(groups.some(group => group.events.includes(relatedUnlinked)), true);
    assert.equal(groups.some(group => group.events.length === 1
        && group.events[0] === expiryExplainedCredit), false);
});

test("a realistic no-groupId action in the latest eight loses only its standalone card", () => {
    const scope = makeScope();
    const confirmedAction = action();
    const unrelated = linked("window_changed_unknown", {
        pluginResetAt: undefined, groupId: "observation", explanation: {reason: "account_change"},
    });
    scope.usageHistory = [linked(), unrelated, confirmedAction];

    const groups = scope.visibleHistoryGroups();
    assert.equal(groups.length, 1);
    assert.deepEqual(groups[0].events.map(event => event.kind),
        ["plugin_reset_reset", "allowance_increased_unknown", "window_changed_unknown"]);
    assert.equal(groups[0].explanation.reason, "account_change");
});

test("display grouping refuses ambiguous or unrelated action matches", () => {
    const scope = makeScope();
    const observation = linked();
    const secondObservation = linked("credits_changed", {groupId: "observation-2"});
    let recent = scope.historyGroups([observation, action()]);
    let retained = scope.historyGroups([observation, secondObservation, action()]);
    assert.deepEqual(scope.mergePluginResetHistoryGroups(recent, retained), recent);

    const wrongAction = action({observedAt: "2026-09-19T14:23:59Z"});
    recent = scope.historyGroups([observation, wrongAction]);
    retained = scope.historyGroups([observation, wrongAction]);
    const result = scope.mergePluginResetHistoryGroups(recent, retained);
    assert.equal(result.length, 2);
    assert.equal(result.some(group => group.events.length > 1), false);

    for (const unconfirmed of [
        action({source: "usage_api"}),
        action({confidence: "observed"}),
        action({provider: "claude"}),
    ]) {
        recent = scope.historyGroups([observation, unconfirmed]);
        retained = scope.historyGroups([observation, unconfirmed]);
        assert.equal(scope.mergePluginResetHistoryGroups(recent, retained).length, 2);
    }
});
