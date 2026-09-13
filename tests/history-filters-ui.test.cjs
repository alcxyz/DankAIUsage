const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const qml = fs.readFileSync(`${__dirname}/../DankAIUsageWidget.qml`, 'utf8');

function scope() {
    const s = { historyShowOther: true, historyShowScheduledShort: false, historyShowScheduledWeekly: false,
        showCodex: true, showClaude: true, usageHistory: [], pluginId: 'test', saved: [] };
    s.root = s;
    s.pluginService = { savePluginState: (...args) => s.saved.push(args) };
    for (const name of ['historyMatchesFilters', 'visibleHistory', 'setHistoryFilter']) {
        const source = qml.match(new RegExp(`    function ${name}\\([^]*?\\n    }`))[0];
        s[name] = new Function('scope', `with(scope) { return (${source.trim()}); }`)(s);
    }
    return s;
}

test('independent checkboxes hide only known scheduled windows', () => {
    const s = scope();
    const event = (bucket, kind = 'scheduled_window') => ({bucket, kind});
    assert.equal(s.historyMatchesFilters(event('general-5-hour')), false);
    assert.equal(s.historyMatchesFilters(event('weekly_scoped-weekly')), false);
    assert.equal(s.historyMatchesFilters(event('new-monthly')), true);
    assert.equal(s.historyMatchesFilters(event('general-weekly', 'allowance_increased_unknown')), true);
    assert.equal(s.historyMatchesFilters(event('general-5-hour', 'reset_redeemed_inferred')), true);
    s.setHistoryFilter('historyShowScheduledShort', true);
    assert.equal(s.historyMatchesFilters(event('general-5-hour')), true);
    assert.equal(s.historyMatchesFilters(event('general-weekly')), false);
    s.setHistoryFilter('historyShowScheduledWeekly', true);
    s.setHistoryFilter('historyShowScheduledShort', false);
    assert.equal(s.historyMatchesFilters(event('general-weekly')), true);
    assert.equal(s.historyMatchesFilters(event('general-5-hour')), false);
    assert.deepEqual(s.saved[0], ['test', 'historyShowScheduledShort', true]);
    s.setHistoryFilter('showClaude', false);
    assert.equal(s.showClaude, true);
});

test('filtering precedes the eight-event limit and never mutates retained history', () => {
    const s = scope();
    s.usageHistory = Array.from({length: 12}, (_, i) => ({provider: 'claude',
        bucket: 'general-5-hour', kind: i < 9 ? 'scheduled_window' : 'allowance_increased_unknown',
        observedAt: new Date(Date.UTC(2026, 8, 13, 12, 59-i)).toISOString()}));
    const before = JSON.stringify(s.usageHistory);
    assert.equal(s.visibleHistory().length, 3);
    s.setHistoryFilter('historyShowScheduledShort', true);
    assert.equal(s.visibleHistory().length, 8);
    s.showClaude = false;
    assert.equal(s.visibleHistory().length, 0);
    assert.equal(JSON.stringify(s.usageHistory), before);
});

test('Other is enabled by default and all categories can be independently hidden', () => {
    const s = scope();
    s.usageHistory = ['allowance_increased_unknown', 'window_changed_unknown', 'scheduled_window'].map(kind =>
        ({provider: 'claude', kind, bucket: 'general-weekly', observedAt: '2026-09-13T12:00:00Z'}));
    assert.equal(s.visibleHistory().length, 2);
    s.setHistoryFilter('historyShowOther', false);
    assert.equal(s.visibleHistory().length, 0);
    assert.equal(s.historyMatchesFilters({kind:'scheduled_window', bucket:'unknown-window'}), false);
    s.setHistoryFilter('historyShowScheduledWeekly', true);
    assert.equal(s.visibleHistory().length, 1);
    assert.equal(s.visibleHistory()[0].kind, 'scheduled_window');
    assert.deepEqual(s.saved[0], ['test', 'historyShowOther', false]);
    assert.match(qml, /loadPluginState\(pluginId, "historyShowOther", true\) !== false/);
});

test('history uses checkbox controls and group hydration respects the filters', () => {
    assert.match(qml, /component HistoryCheckbox: Controls.CheckBox/);
    assert.match(qml, /historyProviderVisible\(event\) && historyMatchesFilters\(event\)/);
    assert.doesNotMatch(qml, /title: "Bar controls"|quickControlsOpen/);
});

test('settings preserve individual weekly choices including temporarily absent limits', () => {
    const settings = fs.readFileSync(`${__dirname}/../DankAIUsageSettings.qml`, 'utf8');
    const all = {id: 'general-weekly', allowance: {window: 'weekly'}};
    const fable = {id: 'fable-weekly', allowance: {window: 'weekly'}};
    const s = { barClaudeWeeklyOverrides: {'absent-weekly': false},
        barShowClaudeWeeklyValue: true, saved: [],
        summary: {providers: [{id: 'claude', quotaBuckets: [all, fable, all,
            {id: 'general-5-hour', allowance: {window: 'session'}}]}]} };
    s.root = s;
    s.loadState = () => s.summary;
    s.saveValue = (...args) => s.saved.push(args);
    for (const name of ['claudeWeeklyBarChoices', 'claudeBucketShownInBar', 'setClaudeWeeklyBarBucket']) {
        const source = settings.match(new RegExp(`    function ${name}\\([^]*?\\n    }`))[0];
        s[name] = new Function('scope', `with(scope) { return (${source.trim()}); }`)(s);
    }
    assert.equal(s.claudeWeeklyBarChoices().length, 2);
    const original = s.barClaudeWeeklyOverrides;
    s.setClaudeWeeklyBarBucket(all, false);
    assert.notEqual(s.barClaudeWeeklyOverrides, original);
    assert.equal(s.barClaudeWeeklyOverrides['absent-weekly'], false);
    assert.equal(s.claudeBucketShownInBar(all), false);
    assert.equal(s.claudeBucketShownInBar(fable), true);
    s.barShowClaudeWeeklyValue = false;
    s.setClaudeWeeklyBarBucket(fable, true);
    assert.equal(s.claudeBucketShownInBar(fable), true);
    assert.equal(s.saved[0][0], 'barClaudeWeeklyOverrides');
    s.summary = null;
    assert.equal(s.claudeWeeklyBarChoices().length, 0);
    assert.equal(s.barClaudeWeeklyOverrides['absent-weekly'], false);
});
