const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const qml = fs.readFileSync(`${__dirname}/../DankAIUsageWidget.qml`, 'utf8');

function scope() {
    const s = { historyShowOther: true, historyShowScheduledShort: false, historyShowScheduledWeekly: false,
        showCodex: true, showClaude: true, usageHistory: [], pluginId: 'test', saved: [],
        sectionPageSize: 4, historyVisibleLimit: 4, announcementsVisibleLimit: 4 };
    s.root = s;
    s.pluginService = { savePluginState: (...args) => s.saved.push(args) };
    for (const name of ['historyMatchesFilters', 'matchingHistory', 'visibleHistory', 'setHistoryFilter',
        'showMoreSection', 'resetSectionLimits', 'showMoreLabel']) {
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

test('filtering precedes the paged display limit and never mutates retained history', () => {
    const s = scope();
    s.usageHistory = Array.from({length: 12}, (_, i) => ({provider: 'claude',
        bucket: 'general-5-hour', kind: i < 9 ? 'scheduled_window' : 'allowance_increased_unknown',
        observedAt: new Date(Date.UTC(2026, 8, 13, 12, 59-i)).toISOString()}));
    const before = JSON.stringify(s.usageHistory);
    assert.equal(s.visibleHistory().length, 3);
    s.setHistoryFilter('historyShowScheduledShort', true);
    assert.equal(s.matchingHistory().length, 12);
    assert.equal(s.visibleHistory().length, 4);
    s.showClaude = false;
    assert.equal(s.visibleHistory().length, 0);
    assert.equal(JSON.stringify(s.usageHistory), before);
});

test('show more reveals the next page in order and closing the dropdown returns to the first page', () => {
    const s = scope();
    s.usageHistory = Array.from({length: 10}, (_, i) => ({provider: 'codex',
        bucket: 'general-weekly', kind: 'allowance_increased_unknown',
        observedAt: new Date(Date.UTC(2026, 8, 13, 12, 59-i)).toISOString()}));
    assert.equal(s.visibleHistory().length, 4);
    assert.equal(s.showMoreLabel(s.matchingHistory().length - s.visibleHistory().length), 'Show 4 more (6 hidden)');
    s.showMoreSection('history');
    assert.equal(s.visibleHistory().length, 8);
    assert.deepEqual(s.visibleHistory(), s.matchingHistory().slice(0, 8));
    assert.equal(s.showMoreLabel(s.matchingHistory().length - s.visibleHistory().length), 'Show 2 more (2 hidden)');
    s.showMoreSection('history');
    assert.equal(s.visibleHistory().length, 10);
    s.showMoreSection('announcements');
    assert.equal(s.announcementsVisibleLimit, 8);
    s.resetSectionLimits();
    assert.equal(s.historyVisibleLimit, 4);
    assert.equal(s.announcementsVisibleLimit, 4);
    assert.equal(s.visibleHistory().length, 4);
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

test('time-only changes get a distinct status without an unchanged percentage arrow', () => {
    const s = {showUsed: false, formatShortDateTime: value => value};
    for (const name of ['historyTimeOnlyChange', 'historyEventTitle', 'historyPercent', 'historyEventDetail']) {
        const source = qml.match(new RegExp(`    function ${name}\\([^]*?\\n    }`))[0];
        s[name] = new Function('scope', `with(scope) { return (${source.trim()}); }`)(s);
    }
    const event = {kind:'window_changed_unknown', provider:'claude',
        before:{usedPercent:9, resetAt:'2026-09-01T12:30:00Z'},
        after:{usedPercent:9, resetAt:'2026-09-01T16:50:00Z'}};
    assert.equal(s.historyEventTitle(event), 'Reset time changed · usage unchanged');
    assert.match(s.historyEventDetail(event), /Usage unchanged: 91% left/);
    assert.doesNotMatch(s.historyEventDetail(event), /91% left → 91% left/);
    assert.match(s.historyEventDetail(event), /12:30:00Z → 2026-09-01T16:50:00Z/);
    s.showUsed = true;
    assert.match(s.historyEventDetail(event), /Usage unchanged: 9% used/);
    assert.equal(s.historyTimeOnlyChange({...event, timingNoise:true}), false);
    assert.equal(s.historyTimeOnlyChange({...event, after:{...event.after, usedPercent:8}}), false);
    assert.equal(s.historyTimeOnlyChange({...event, after:{...event.after, resetAt:'invalid'}}), false);
    assert.equal(s.historyTimeOnlyChange({...event, kind:'scheduled_window'}), false);
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
