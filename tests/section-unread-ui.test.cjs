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

function makeScope(overrides = {}) {
    const saves = [];
    const scope = {
        pluginId: "dankAIUsage",
        historyReadKeys: [],
        announcementsReadKeys: [],
        announcementReceipts: {},
        pluginService: {
            savePluginState(plugin, key, value) {
                saves.push([plugin, key, structuredClone(value)]);
            },
        },
        ...overrides,
    };
    scope.root = scope;
    scope.saves = saves;
    for (const name of [
        "normalizedReadKeys",
        "sectionReadKeys",
        "unreadSectionCount",
        "markSectionRead",
    ]) scope[name] = bindQmlFunction(name, scope);
    return scope;
}

function historyEvent(overrides = {}) {
    return {
        provider: "codex",
        observedAt: "2026-09-19T10:00:00Z",
        bucket: "general-5-hour",
        kind: "scheduled_window",
        source: "usage_api",
        groupId: "observation-1",
        label: "5-hour limit",
        explanation: null,
        ...overrides,
    };
}

function announcement(overrides = {}) {
    return {
        id: "reset-a",
        revision: 1,
        provider: "codex",
        summary: "Reset expected soon",
        ...overrides,
    };
}

test("new history and announcements are initially unread", () => {
    const scope = makeScope();
    const historyKeys = scope.sectionReadKeys("history", [historyEvent(), historyEvent({groupId: "observation-2"})]);
    const announcementKeys = scope.sectionReadKeys("announcements", [announcement()]);

    assert.equal(scope.unreadSectionCount(historyKeys, scope.historyReadKeys), 2);
    assert.equal(scope.unreadSectionCount(announcementKeys, scope.announcementsReadKeys), 1);
});

test("viewing a section clears its count, persists once, and repeated views do not write", () => {
    const scope = makeScope();
    const keys = scope.sectionReadKeys("history", [historyEvent(), historyEvent({groupId: "observation-2"})]);

    scope.markSectionRead("history", keys);
    assert.equal(scope.unreadSectionCount(keys, scope.historyReadKeys), 0);
    assert.deepEqual(scope.saves, [["dankAIUsage", "historyReadKeys", keys]]);

    scope.markSectionRead("history", keys);
    assert.equal(scope.saves.length, 1);
});

test("saved read arrays restore the cleared state after reconstructing the widget scope", () => {
    const persistentState = {};
    const first = makeScope({
        pluginService: {
            savePluginState(_plugin, key, value) {
                persistentState[key] = structuredClone(value);
            },
        },
    });
    const events = [historyEvent(), historyEvent({groupId: "observation-2"})];
    const firstKeys = first.sectionReadKeys("history", events);
    first.markSectionRead("history", firstKeys);

    const restarted = makeScope();
    restarted.historyReadKeys = restarted.normalizedReadKeys(persistentState.historyReadKeys);
    const restartedKeys = restarted.sectionReadKeys("history", events);
    assert.equal(restarted.unreadSectionCount(restartedKeys, restarted.historyReadKeys), 0);
});

test("a new history event increments the unread count after existing events were read", () => {
    const scope = makeScope();
    const oldEvents = [historyEvent()];
    scope.markSectionRead("history", scope.sectionReadKeys("history", oldEvents));

    const currentKeys = scope.sectionReadKeys("history", [
        ...oldEvents,
        historyEvent({observedAt: "2026-09-19T11:00:00Z", groupId: "observation-2"}),
    ]);
    assert.equal(scope.unreadSectionCount(currentKeys, scope.historyReadKeys), 1);
});

test("a corrected announcement revision is unread while the prior revision stays read", () => {
    const scope = makeScope();
    const original = announcement();
    scope.markSectionRead("announcements", scope.sectionReadKeys("announcements", [original]));

    const correctedKeys = scope.sectionReadKeys("announcements", [announcement({revision: 2})]);
    assert.equal(scope.unreadSectionCount(correctedKeys, scope.announcementsReadKeys), 1);
    assert.notEqual(correctedKeys[0], scope.announcementsReadKeys[0]);
});

test("filtered-out events remain unread until they enter the included item set", () => {
    const scope = makeScope();
    const shown = historyEvent();
    const hidden = historyEvent({provider: "claude", groupId: "observation-hidden"});

    const shownKeys = scope.sectionReadKeys("history", [shown]);
    scope.markSectionRead("history", shownKeys);
    assert.equal(scope.unreadSectionCount(shownKeys, scope.historyReadKeys), 0);

    const newlyIncludedKeys = scope.sectionReadKeys("history", [shown, hidden]);
    assert.equal(scope.unreadSectionCount(newlyIncludedKeys, scope.historyReadKeys), 1);
});

test("history explanation and display-label edits do not make an event unread again", () => {
    const scope = makeScope();
    const original = historyEvent();
    const originalKeys = scope.sectionReadKeys("history", [original]);
    scope.markSectionRead("history", originalKeys);

    const edited = {
        ...original,
        label: "Updated provider label",
        explanation: {reason: "manual_reset", note: "Added after reading"},
        explanationChoices: ["manual_reset"],
    };
    const editedKeys = scope.sectionReadKeys("history", [edited]);
    assert.deepEqual(editedKeys, originalKeys);
    assert.equal(scope.unreadSectionCount(editedKeys, scope.historyReadKeys), 0);
});

test("invalid saved read state is sanitized and bounded to the latest 200 keys", () => {
    const scope = makeScope();
    assert.deepEqual(scope.normalizedReadKeys(null), []);
    assert.deepEqual(scope.normalizedReadKeys({0: "key"}), []);

    const valid = Array.from({length: 205}, (_, index) => `key-${index}`);
    const normalized = scope.normalizedReadKeys([
        "", null, 42, "x".repeat(2049), ...valid,
    ]);
    assert.equal(normalized.length, 200);
    assert.deepEqual(normalized, valid.slice(-200));
});

test("reading announcement rows does not consume notification-toast receipts", () => {
    const notificationReceipts = {"reset-a:1": "2026-09-19T09:00:00Z"};
    const scope = makeScope({announcementReceipts: notificationReceipts});
    const keys = scope.sectionReadKeys("announcements", [announcement()]);

    scope.markSectionRead("announcements", keys);
    assert.strictEqual(scope.announcementReceipts, notificationReceipts);
    assert.deepEqual(scope.announcementReceipts, {"reset-a:1": "2026-09-19T09:00:00Z"});
    assert.deepEqual(scope.saves, [["dankAIUsage", "announcementsReadKeys", keys]]);
});

test("QML marks expanded sections read only while the popout is open", t => {
    const {spawnSync} = require("node:child_process");
    const probe = spawnSync("quickshell", ["--version"], {encoding: "utf8"});
    if (probe.error?.code === "ENOENT") return t.skip("quickshell unavailable");
    const directory = fs.mkdtempSync(path.join(require("node:os").tmpdir(), "unread-qml-"));
    try {
        // Exercise the actual section bindings and handlers without loading providers.
        const section = id => qml.match(new RegExp(`id: ${id}[^]*?\\n\\s+width: parent.width`))[0]
            .replace(/\n\s+width: parent.width$/, "");
        const helpers = ["normalizedReadKeys", "sectionReadKeys", "unreadSectionCount", "markSectionRead"]
            .map(extractFunction).join("\n");
        const visibility = qml.match(/readonly property bool popoutVisible:[^\n]+/)[0];
        const fixture = `import QtQuick
import Quickshell
Item {
    id: root
    property string pluginId: "test"
    property var pluginService: null
    property var historyReadKeys: []
    property var announcementsReadKeys: []
    property var events: [{provider: "codex", observedAt: "first", kind: "reset"}]
    property var announcements: [{id: "first", revision: 1}]
    property int step: 0
    property bool failed: false
    function historyGroupsForDisplay() { return [{events: events}] }
    function visibleAnnouncements() { return announcements }
    ${helpers}
    Item {
        id: popoutRoot
        property var parentPopout: QtObject { property bool shouldBeVisible: false }
        ${visibility}
        Column { ${section("historySection")}
            visible: false }
        Column { ${section("announcementsSection")}
            visible: false }
    }
    function expect(value, wanted, message) {
        if (value !== wanted) { failed = true; console.error("UNREAD-FAIL: " + message) }
    }
    Timer {
        interval: 20; running: true; repeat: true
        onTriggered: {
            switch (root.step++) {
            case 0:
                expect(historySection.unreadCount, 1, "initial history unread")
                expect(announcementsSection.unreadCount, 1, "initial announcement unread")
                popoutRoot.parentPopout.shouldBeVisible = true
                break
            case 1:
                expect(historySection.unreadCount, 1, "collapsed history unread")
                expect(announcementsSection.unreadCount, 1, "collapsed announcements unread")
                historySection.visible = true
                announcementsSection.visible = true
                break
            case 2:
                expect(historySection.unreadCount, 0, "expanded history read")
                expect(announcementsSection.unreadCount, 0, "expanded announcements read")
                popoutRoot.parentPopout.shouldBeVisible = false
                root.events = root.events.concat([{provider: "codex", observedAt: "second", kind: "reset"}])
                root.announcements = [{id: "first", revision: 2}]
                break
            case 3:
                expect(historySection.unreadCount, 1, "closed refresh remains unread")
                expect(announcementsSection.unreadCount, 1, "closed revision remains unread")
                popoutRoot.parentPopout.shouldBeVisible = true
                break
            case 4:
                expect(historySection.unreadCount, 0, "reopening expanded history reads it")
                expect(announcementsSection.unreadCount, 0, "reopening expanded announcements reads them")
                root.events = root.events.concat([{provider: "codex", observedAt: "third", kind: "reset"}])
                root.announcements = [{id: "first", revision: 3}]
                break
            case 5:
                expect(historySection.unreadCount, 0, "visible incoming history read")
                expect(announcementsSection.unreadCount, 0, "visible incoming revision read")
                if (!root.failed) console.info("UNREAD-LIFECYCLE-PASS")
                Qt.quit()
            }
        }
    }
}`;
        const file = path.join(directory, "shell.qml");
        fs.writeFileSync(file, fixture);
        const result = spawnSync("quickshell", ["--no-color", "-p", file], {
            encoding: "utf8", timeout: 10000,
            env: {...process.env, QT_QPA_PLATFORM: "offscreen"},
        });
        const output = result.stdout + result.stderr;
        assert.equal(result.status, 0, output);
        assert.match(output, /UNREAD-LIFECYCLE-PASS/);
        assert.doesNotMatch(output, /UNREAD-FAIL|ReferenceError|TypeError|Binding loop/);
    } finally {
        fs.rmSync(directory, {recursive: true, force: true});
    }
});
