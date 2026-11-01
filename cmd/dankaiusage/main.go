package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

func buildVersion() string {
	info, _ := debug.ReadBuildInfo()
	return resolveBuildVersion(version, info)
}

func resolveBuildVersion(configured string, info *debug.BuildInfo) string {
	if configured != "dev" {
		return configured
	}
	revision, modified := vcsBuildRevision(info)
	if len(revision) < 12 || !isHex(revision) {
		return configured
	}
	resolved := "dev-" + strings.ToLower(revision[:12])
	if modified {
		resolved += "-dirty"
	}
	return resolved
}

func vcsBuildRevision(info *debug.BuildInfo) (string, bool) {
	if info == nil {
		return "", false
	}
	var revision string
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return revision, modified
}

type PeriodTotals struct {
	Input         int64  `json:"input"`
	Output        int64  `json:"output"`
	Cached        int64  `json:"cached"`
	Reasoning     int64  `json:"reasoning"`
	Tool          int64  `json:"tool"`
	Total         int64  `json:"total"`
	Requests      int64  `json:"requests"`
	Sessions      int64  `json:"sessions"`
	LastTimestamp string `json:"lastTimestamp,omitempty"`
}

type RollingTotals struct {
	SevenDays  PeriodTotals `json:"sevenDays"`
	ThirtyDays PeriodTotals `json:"thirtyDays"`
	NinetyDays PeriodTotals `json:"ninetyDays"`
}

type Allowance struct {
	Known            bool    `json:"known"`
	Window           string  `json:"window"`
	Unit             string  `json:"unit"`
	Source           string  `json:"source,omitempty"`
	Used             int64   `json:"used"`
	Limit            int64   `json:"limit"`
	Remaining        int64   `json:"remaining"`
	PercentUsed      float64 `json:"percentUsed"`
	PercentRemaining float64 `json:"percentRemaining"`
	ResetAt          string  `json:"resetAt,omitempty"`
	WindowMinutes    int64   `json:"windowMinutes,omitempty"`
}

type ExtraLimit struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Weekly *Allowance `json:"weeklyLeft,omitempty"`
}

type QuotaBucket struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Kind       string    `json:"kind"`
	Allowance  Allowance `json:"allowance"`
	ValueLabel string    `json:"valueLabel,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	// Balance is a prepaid balance in major currency units; BalanceHistory
	// lists the observed top-ups the dropdown and bar measure it against.
	Balance        *float64              `json:"balance,omitempty"`
	BalanceHistory *CreditBalanceHistory `json:"balanceHistory,omitempty"`
}

type UsageReset struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	ResetType   string `json:"resetType,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
}

type ModelUsage struct {
	Model    string `json:"model"`
	Input    int64  `json:"input"`
	Output   int64  `json:"output"`
	Cached   int64  `json:"cached"`
	Total    int64  `json:"total"`
	Requests int64  `json:"requests"`
}

type ProviderUsage struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Available         bool           `json:"available"`
	CLIPath           string         `json:"cliPath,omitempty"`
	DataPath          string         `json:"dataPath,omitempty"`
	Error             string         `json:"error,omitempty"`
	Today             PeriodTotals   `json:"today"`
	Week              PeriodTotals   `json:"week"`
	Month             PeriodTotals   `json:"month"`
	Period            PeriodTotals   `json:"period"`
	Rolling           RollingTotals  `json:"rolling"`
	WeeklyLeft        Allowance      `json:"weeklyLeft"`
	ExtraLimits       []ExtraLimit   `json:"extraLimits,omitempty"`
	QuotaBuckets      []QuotaBucket  `json:"quotaBuckets,omitempty"`
	Resets            []UsageReset   `json:"resets,omitempty"`
	Models            []ModelUsage   `json:"models"`
	LastProject       string         `json:"lastProject,omitempty"`
	Meta              map[string]any `json:"meta,omitempty"`
	historyObservedAt time.Time
}

type Summary struct {
	Version      string              `json:"version"`
	GeneratedAt  string              `json:"generatedAt"`
	PeriodDays   int                 `json:"periodDays"`
	Providers    []ProviderUsage     `json:"providers"`
	GrandTotal   PeriodTotals        `json:"grandTotal"`
	History      []UsageHistoryEvent `json:"history"`
	HistoryError string              `json:"historyError,omitempty"`
	Errors       []string            `json:"errors,omitempty"`
	Capabilities map[string]bool     `json:"capabilities"`
	Tracking     TrackingSummary     `json:"tracking"`
}

type tokenEvent struct {
	Provider         string
	Timestamp        time.Time
	Session          string
	Project          string
	Model            string
	Input            int64
	Output           int64
	Cached           int64
	Reasoning        int64
	Tool             int64
	TrackKey         string
	TrackFingerprint string
}

type options struct {
	PeriodDays      int
	RefreshInterval time.Duration
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(buildVersion())
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "codex-reset" {
		runCodexResetCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "claude-statusline" {
		// Removed fallback. Claude Code may still run it as statusLine.command:
		// drain its payload and print nothing, so the status line stays blank
		// instead of showing an error.
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "claude-reset" {
		runClaudeResetCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "history" {
		runUsageHistoryCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "tracking" {
		runTokenTrackingCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "diagnostics" {
		runDiagnosticsCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "announcements" {
		runAnnouncementsCommand(os.Args[2:])
		return
	}

	fs := flag.NewFlagSet("summary", flag.ExitOnError)
	periodDays := fs.Int("period-days", 7, "rolling period length in days")
	refreshInterval := fs.Int("refresh-interval", int(usageRefreshDefaultInterval/time.Second), "provider usage refresh interval in seconds")
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	_ = fs.Parse(dropSummaryArg(os.Args[1:]))
	// A stray word, such as the removed claude-prime command, must not fall
	// through to a full summary.
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "dankaiusage: unknown command %q\n", fs.Arg(0))
		os.Exit(2)
	}

	if *periodDays < 1 {
		*periodDays = 1
	}

	summary := collect(options{
		PeriodDays:      *periodDays,
		RefreshInterval: usageRefreshIntervalSeconds(*refreshInterval),
	})
	var data []byte
	var err error
	if *pretty {
		data, err = json.MarshalIndent(summary, "", "  ")
	} else {
		data, err = json.Marshal(summary)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode summary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func dropSummaryArg(args []string) []string {
	if len(args) > 0 && args[0] == "summary" {
		return args[1:]
	}
	return args
}

func collect(opts options) Summary {
	opts.RefreshInterval = normalizeUsageRefreshInterval(opts.RefreshInterval)
	now := time.Now()
	out := Summary{
		Version:     buildVersion(),
		GeneratedAt: now.Format(time.RFC3339),
		PeriodDays:  opts.PeriodDays,
		History:     []UsageHistoryEvent{},
		Capabilities: map[string]bool{
			"codexCli":  hasCommand("codex"),
			"claudeCli": hasCommand("claude"),
		},
	}

	codex := collectCodex(now, opts)
	claude := collectClaude(now, opts)
	out.Providers = []ProviderUsage{codex, claude}
	out.Tracking = refreshTokenTracking(tokenTrackingPath(), now)

	for _, provider := range out.Providers {
		addTotals(&out.GrandTotal, provider.Period)
		if provider.Error != "" {
			out.Errors = append(out.Errors, provider.Name+": "+provider.Error)
		}
	}
	if history, err := observeUsageHistory(usageHistoryPath(), time.Now(), out.Providers); err != nil {
		out.HistoryError = formatUsageHistoryError(err)
	} else {
		out.History = history
	}

	return out
}

func collectCodex(now time.Time, opts options) ProviderUsage {
	provider := ProviderUsage{
		ID:        "codex",
		Name:      "Codex",
		Available: hasCommand("codex"),
		CLIPath:   commandPath("codex"),
	}
	root := codexHome()
	provider.DataPath = root
	provider.historyObservedAt = time.Now()
	if weeklyLeft, extraLimits, additionalBuckets, resets, meta, observedAt, err := collectCodexSubscriptionLimitsWithClock(now, opts.RefreshInterval, time.Now); err == nil {
		provider.WeeklyLeft = weeklyLeft
		provider.ExtraLimits = extraLimits
		provider.QuotaBuckets = makeQuotaBuckets(weeklyLeft, extraLimits, additionalBuckets)
		provider.Resets = resets
		provider.Meta = meta
		provider.historyObservedAt = observedAt
	} else {
		mergeProviderMeta(&provider, meta)
		setProviderMeta(&provider, "limitError", err.Error())
	}
	setProviderMeta(&provider, "tokenDataSource", "local Codex session transcripts")
	setProviderMeta(&provider, "tokenDataScope", "Codex CLI local history only")
	setProviderMeta(&provider, "tokenDataIncludesWeb", false)

	cutoff := now.AddDate(0, 0, -maxInt(opts.PeriodDays, 90)-1)
	events, stats := collectCodexTranscriptEvents(root, cutoff)
	applyEvents(&provider, events, now, opts)
	available := stats.UsableRecords > 0
	setProviderMeta(&provider, "tokenDataAvailable", available)
	setProviderMeta(&provider, "transcriptFiles", stats.FilesFound)
	setProviderMeta(&provider, "transcriptFilesScanned", stats.FilesScanned)
	setProviderMeta(&provider, "usageEventsScanned", stats.UsageRecords)
	if stats.partial() {
		setProviderMeta(&provider, "tokenDataError", "Codex token history is incomplete")
	}
	if stats.SourcesFound == 0 {
		setProviderMeta(&provider, "tokenDataNote", "Codex transcripts not found")
	} else if !available {
		setProviderMeta(&provider, "tokenDataNote", "No usable local Codex token records")
	} else if provider.Period.Requests == 0 {
		setProviderMeta(&provider, "tokenDataNote", "No local Codex events in selected period")
	}
	return provider
}

func collectClaude(now time.Time, opts options) ProviderUsage {
	provider := ProviderUsage{
		ID:        "claude",
		Name:      "Claude",
		Available: hasCommand("claude"),
		CLIPath:   commandPath("claude"),
	}
	setProviderMeta(&provider, "tokenDataSource", "local Claude Code transcripts")
	setProviderMeta(&provider, "tokenDataScope", "Claude Code local history only")
	setProviderMeta(&provider, "tokenDataIncludesWeb", false)
	setProviderMeta(&provider, "limitDataSource", "Anthropic usage API")
	setProviderMeta(&provider, "limitDataIncludesWeb", true)
	root := claudeHome()
	provider.DataPath = root
	provider.historyObservedAt = time.Now()
	weeklyLeft, extraLimits, additionalBuckets, meta, observedAt, limitErr := collectClaudeSubscriptionLimitsWithClock(now, opts.RefreshInterval, time.Now)
	provider.WeeklyLeft = weeklyLeft
	provider.ExtraLimits = extraLimits
	provider.QuotaBuckets = makeQuotaBuckets(weeklyLeft, extraLimits, additionalBuckets)
	mergeProviderMeta(&provider, meta)
	if !observedAt.IsZero() {
		provider.historyObservedAt = observedAt
	}
	if limitErr != nil {
		setProviderMeta(&provider, "limitError", limitErr.Error())
	}
	if resets, resetMeta := claudeResetsForSummary(now, opts.RefreshInterval); resetMeta != nil {
		provider.Resets = resets
		mergeProviderMeta(&provider, resetMeta)
	}

	projects := filepath.Join(root, "projects")
	if _, err := os.Stat(projects); err != nil {
		setProviderMeta(&provider, "tokenDataError", "Claude projects not found")
		return provider
	}

	cutoff := now.AddDate(0, 0, -maxInt(opts.PeriodDays, 90)-1)
	var events []tokenEvent
	var latestUsage time.Time
	var transcriptFiles int
	var parsedUsageEvents int
	err := filepath.WalkDir(projects, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		transcriptFiles++
		if info, statErr := d.Info(); statErr == nil && info.ModTime().Before(cutoff) {
			return nil
		}
		fileEvents, parseErr := readClaudeJSONL(path)
		if parseErr != nil && provider.Error == "" {
			setProviderMeta(&provider, "tokenDataError", parseErr.Error())
		}
		parsedUsageEvents += len(fileEvents)
		for _, event := range fileEvents {
			if event.Timestamp.After(latestUsage) {
				latestUsage = event.Timestamp
			}
		}
		events = append(events, fileEvents...)
		return nil
	})
	if err != nil {
		setProviderMeta(&provider, "tokenDataError", err.Error())
	}
	events, ambiguousRevisions := normalizeClaudeTokenEvents(events)
	if ambiguousRevisions > 0 {
		setProviderMeta(&provider, "tokenDataError", "Claude token history contains ambiguous message revisions")
	}

	applyEvents(&provider, events, now, opts)
	setProviderMeta(&provider, "transcriptFiles", transcriptFiles)
	setProviderMeta(&provider, "usageEventsScanned", parsedUsageEvents)
	if !latestUsage.IsZero() {
		setProviderMeta(&provider, "lastUsageAt", latestUsage.Format(time.RFC3339))
		periodStart := now.AddDate(0, 0, -opts.PeriodDays)
		if provider.Period.Requests == 0 && latestUsage.Before(periodStart) {
			setProviderMeta(&provider, "tokenDataNote", "No local Claude Code events in selected period")
		}
	}
	return provider
}

func readClaudeJSONL(path string) ([]tokenEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	project := filepath.Base(filepath.Dir(path))
	decoder := json.NewDecoder(f)
	var events []tokenEvent
	for {
		var row map[string]any
		if err := decoder.Decode(&row); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return events, fmt.Errorf("%s: %w", path, err)
		}
		if row["type"] != "assistant" {
			continue
		}
		msg, _ := row["message"].(map[string]any)
		usage, _ := msg["usage"].(map[string]any)
		if len(usage) == 0 {
			continue
		}
		ts, ok := parseTime(row["timestamp"])
		if !ok {
			continue
		}
		session := firstNonEmpty(stringValue(row["sessionId"]), stringValue(row["session_id"]))
		model, _ := msg["model"].(string)
		stableID := firstNonEmpty(
			stringValue(msg["id"]),
			stringValue(msg["requestId"]),
			stringValue(msg["request_id"]),
			stringValue(row["requestId"]),
			stringValue(row["request_id"]),
			stringValue(row["messageId"]),
			stringValue(row["uuid"]),
		)
		input := jsonInt(usage["input_tokens"])
		output := jsonInt(usage["output_tokens"])
		cached := jsonInt(usage["cache_creation_input_tokens"]) + jsonInt(usage["cache_read_input_tokens"])
		trackKey := ""
		trackFingerprint := ""
		if stableID != "" {
			trackKey = tokenIdentityHash("claude-event", stableID)
			trackFingerprint = tokenIdentityHash("claude-usage", stableID,
				strconv.FormatInt(input, 10), strconv.FormatInt(output, 10), strconv.FormatInt(cached, 10))
		}
		events = append(events, tokenEvent{
			Provider:         "claude",
			Timestamp:        ts,
			Session:          session,
			Project:          project,
			Model:            model,
			Input:            input,
			Output:           output,
			Cached:           cached,
			TrackKey:         trackKey,
			TrackFingerprint: trackFingerprint,
		})
	}
	return events, nil
}

// Claude transcripts can repeat progressively updated usage for one assistant
// message. Keep the greatest monotonic revision so rolling and persistent views
// use the same request semantics. Mixed increases and decreases are ambiguous;
// retaining the first observed revision avoids synthesizing an inflated vector.
func normalizeClaudeTokenEvents(events []tokenEvent) ([]tokenEvent, int) {
	out := make([]tokenEvent, 0, len(events))
	byKey := make(map[string]int)
	ambiguous := 0
	for _, event := range events {
		if event.Provider != "claude" || event.TrackKey == "" {
			out = append(out, event)
			continue
		}
		index, exists := byKey[event.TrackKey]
		if !exists {
			byKey[event.TrackKey] = len(out)
			out = append(out, event)
			continue
		}
		existing := out[index]
		switch {
		case tokenEventAtLeast(event, existing):
			if event.Timestamp.Before(existing.Timestamp) {
				event.Timestamp = existing.Timestamp
			}
			if event.Session == "" {
				event.Session = existing.Session
			}
			out[index] = event
		case tokenEventAtLeast(existing, event):
			// Older streamed revision or replay.
		default:
			ambiguous++
		}
	}
	return out, ambiguous
}

func tokenEventAtLeast(left, right tokenEvent) bool {
	return left.Input >= right.Input && left.Output >= right.Output && left.Cached >= right.Cached &&
		left.Reasoning >= right.Reasoning && left.Tool >= right.Tool
}

func applyEvents(provider *ProviderUsage, events []tokenEvent, now time.Time, opts options) {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekdayOffset := int(now.Weekday())
	if weekdayOffset == 0 {
		weekdayOffset = 6
	} else {
		weekdayOffset--
	}
	weekStart := todayStart.AddDate(0, 0, -weekdayOffset)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	periodStart := now.AddDate(0, 0, -opts.PeriodDays)
	sevenDaysStart := now.Add(-7 * 24 * time.Hour)
	thirtyDaysStart := now.Add(-30 * 24 * time.Hour)
	ninetyDaysStart := now.Add(-90 * 24 * time.Hour)

	models := map[string]*ModelUsage{}
	periodSessions := map[string]bool{}
	todaySessions := map[string]bool{}
	weekSessions := map[string]bool{}
	monthSessions := map[string]bool{}
	sevenDaySessions := map[string]bool{}
	thirtyDaySessions := map[string]bool{}
	ninetyDaySessions := map[string]bool{}

	for _, event := range events {
		if atOrAfter(event.Timestamp, sevenDaysStart) {
			addEvent(&provider.Rolling.SevenDays, event)
			if event.Session != "" {
				sevenDaySessions[event.Session] = true
			}
		}
		if atOrAfter(event.Timestamp, thirtyDaysStart) {
			addEvent(&provider.Rolling.ThirtyDays, event)
			if event.Session != "" {
				thirtyDaySessions[event.Session] = true
			}
		}
		if atOrAfter(event.Timestamp, ninetyDaysStart) {
			addEvent(&provider.Rolling.NinetyDays, event)
			if event.Session != "" {
				ninetyDaySessions[event.Session] = true
			}
		}
		if event.Timestamp.After(todayStart) || event.Timestamp.Equal(todayStart) {
			addEvent(&provider.Today, event)
			if event.Session != "" {
				todaySessions[event.Session] = true
			}
		}
		if event.Timestamp.After(weekStart) || event.Timestamp.Equal(weekStart) {
			addEvent(&provider.Week, event)
			if event.Session != "" {
				weekSessions[event.Session] = true
			}
		}
		if event.Timestamp.After(monthStart) || event.Timestamp.Equal(monthStart) {
			addEvent(&provider.Month, event)
			if event.Session != "" {
				monthSessions[event.Session] = true
			}
		}
		if event.Timestamp.After(periodStart) || event.Timestamp.Equal(periodStart) {
			addEvent(&provider.Period, event)
			if event.Session != "" {
				periodSessions[event.Session] = true
			}
			key := firstNonEmpty(event.Model, "unknown")
			if models[key] == nil {
				models[key] = &ModelUsage{Model: key}
			}
			models[key].Input += event.Input
			models[key].Output += event.Output
			models[key].Cached += event.Cached
			models[key].Total += eventTotal(event)
			models[key].Requests++
			if event.Project != "" {
				provider.LastProject = event.Project
			}
		}
	}

	provider.Today.Sessions = int64(len(todaySessions))
	provider.Week.Sessions = int64(len(weekSessions))
	provider.Month.Sessions = int64(len(monthSessions))
	provider.Period.Sessions = int64(len(periodSessions))
	provider.Rolling.SevenDays.Sessions = int64(len(sevenDaySessions))
	provider.Rolling.ThirtyDays.Sessions = int64(len(thirtyDaySessions))
	provider.Rolling.NinetyDays.Sessions = int64(len(ninetyDaySessions))

	provider.Models = []ModelUsage{}
	for _, model := range models {
		provider.Models = append(provider.Models, *model)
	}
	sort.Slice(provider.Models, func(i, j int) bool {
		return provider.Models[i].Total > provider.Models[j].Total
	})

	if !provider.WeeklyLeft.Known && provider.WeeklyLeft.Source == "" {
		provider.WeeklyLeft = makeUnknownAllowance("weekly", weekStart.AddDate(0, 0, 7))
	}
}

func atOrAfter(value, start time.Time) bool {
	return value.After(start) || value.Equal(start)
}

func addEvent(totals *PeriodTotals, event tokenEvent) {
	totals.Input += event.Input
	totals.Output += event.Output
	totals.Cached += event.Cached
	totals.Reasoning += event.Reasoning
	totals.Tool += event.Tool
	totals.Total += eventTotal(event)
	totals.Requests++
	if totals.LastTimestamp == "" || event.Timestamp.Format(time.RFC3339) > totals.LastTimestamp {
		totals.LastTimestamp = event.Timestamp.Format(time.RFC3339)
	}
}

func addTotals(dst *PeriodTotals, src PeriodTotals) {
	dst.Input += src.Input
	dst.Output += src.Output
	dst.Cached += src.Cached
	dst.Reasoning += src.Reasoning
	dst.Tool += src.Tool
	dst.Total += src.Total
	dst.Requests += src.Requests
	dst.Sessions += src.Sessions
	if src.LastTimestamp > dst.LastTimestamp {
		dst.LastTimestamp = src.LastTimestamp
	}
}

func eventTotal(event tokenEvent) int64 {
	if event.Provider == "claude" {
		return event.Input + event.Output + event.Cached
	}
	return event.Input + event.Output
}

func makeAllowance(window string, used int64, limit int64, resetAt time.Time) Allowance {
	allowance := Allowance{
		Known:  limit > 0,
		Window: window,
		Unit:   "tokens",
		Used:   used,
		Limit:  limit,
	}
	if !resetAt.IsZero() {
		allowance.ResetAt = resetAt.Format(time.RFC3339)
	}
	if limit <= 0 {
		return allowance
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	allowance.Remaining = remaining
	allowance.PercentUsed = clampPercent(float64(used) / float64(limit) * 100)
	allowance.PercentRemaining = clampPercent(float64(remaining) / float64(limit) * 100)
	return allowance
}

func makeUnknownAllowance(window string, resetAt time.Time) Allowance {
	allowance := Allowance{
		Window: window,
		Unit:   "subscription",
	}
	if !resetAt.IsZero() {
		allowance.ResetAt = resetAt.Format(time.RFC3339)
	}
	return allowance
}

func makeSubscriptionAllowance(window string, source string, usedPercent float64, resetAt time.Time, windowMinutes int64) Allowance {
	remaining := clampPercent(100 - usedPercent)
	allowance := Allowance{
		Known:            true,
		Window:           window,
		Unit:             "percent",
		Source:           source,
		Used:             int64(clampPercent(usedPercent) + 0.5),
		Limit:            100,
		Remaining:        int64(remaining + 0.5),
		PercentUsed:      clampPercent(usedPercent),
		PercentRemaining: remaining,
		WindowMinutes:    windowMinutes,
	}
	if !resetAt.IsZero() {
		allowance.ResetAt = resetAt.Format(time.RFC3339)
	}
	return allowance
}

func makeQuotaBuckets(weekly Allowance, extras []ExtraLimit, additional []QuotaBucket) []QuotaBucket {
	buckets := make([]QuotaBucket, 0, 1+len(extras)+len(additional))
	if weekly.Known {
		buckets = append(buckets, QuotaBucket{ID: "general-weekly", Label: "Weekly", Kind: "weekly", Allowance: weekly})
	}
	for _, extra := range extras {
		if extra.Weekly != nil && extra.Weekly.Known {
			buckets = append(buckets, QuotaBucket{
				ID:        extra.ID + "-weekly",
				Label:     extra.Label + " · weekly",
				Kind:      "scoped",
				Allowance: *extra.Weekly,
			})
		}
	}
	return append(buckets, additional...)
}

type codexRateLimitResponse struct {
	ID     int `json:"id"`
	Result struct {
		RateLimits            codexRateLimitSnapshot            `json:"rateLimits"`
		RateLimitsByLimitID   map[string]codexRateLimitSnapshot `json:"rateLimitsByLimitId"`
		RateLimitResetCredits codexRateLimitResetCredits        `json:"rateLimitResetCredits"`
	} `json:"result"`
	Error any `json:"error,omitempty"`
}

type codexRateLimitSnapshot struct {
	LimitID              string                `json:"limitId"`
	LimitName            string                `json:"limitName"`
	Primary              *codexRateLimitWindow `json:"primary"`
	Secondary            *codexRateLimitWindow `json:"secondary"`
	PlanType             string                `json:"planType"`
	RateLimitReachedType any                   `json:"rateLimitReachedType"`
	Credits              *codexCreditsSnapshot `json:"credits,omitempty"`
}

// codexCreditsSnapshot is the optional prepaid-credit balance the Codex app
// server attaches to a rate-limit snapshot. The balance is a decimal amount
// in dollars; the server sends it as a string or a number.
type codexCreditsSnapshot struct {
	HasCredits bool            `json:"hasCredits"`
	Unlimited  bool            `json:"unlimited"`
	Balance    json.RawMessage `json:"balance,omitempty"`
}

func (credits *codexCreditsSnapshot) balanceLabel() string {
	value, ok := credits.balanceValue()
	if !ok {
		return ""
	}
	// Codex reports a credit count, not money.
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func (credits *codexCreditsSnapshot) balanceValue() (float64, bool) {
	if credits == nil || len(credits.Balance) == 0 {
		return 0, false
	}
	var text string
	if err := json.Unmarshal(credits.Balance, &text); err != nil {
		var number float64
		if err := json.Unmarshal(credits.Balance, &number); err != nil {
			return 0, false
		}
		text = strconv.FormatFloat(number, 'f', -1, 64)
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "$"))
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func codexCreditsBucket(snapshot codexRateLimitSnapshot) (QuotaBucket, bool) {
	credits := snapshot.Credits
	if credits == nil || (!credits.HasCredits && !credits.Unlimited) {
		return QuotaBucket{}, false
	}
	allowance := makeUnknownAllowance("credits", time.Time{})
	allowance.Unit = "credits"
	allowance.Source = "codex app-server"
	bucket := QuotaBucket{
		ID:        "codex-credits",
		Label:     "Credits",
		Kind:      "credits",
		Allowance: allowance,
	}
	if credits.Unlimited {
		bucket.ValueLabel = "Unlimited"
		bucket.Detail = "Unlimited credits"
		return bucket, true
	}
	balance := credits.balanceLabel()
	if balance == "" {
		bucket.ValueLabel = "Available"
		bucket.Detail = "Prepaid credits available; balance not reported"
		return bucket, true
	}
	if value, ok := credits.balanceValue(); ok {
		bucket.Balance = &value
	}
	bucket.ValueLabel = balance
	bucket.Detail = balance + " prepaid credits"
	return bucket, true
}

type codexRateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

type codexRateLimitResetCredits struct {
	AvailableCount      int                         `json:"availableCount"`
	AvailableCountKnown bool                        `json:"-"`
	Credits             []codexRateLimitResetCredit `json:"credits"`
}

func (credits codexRateLimitResetCredits) MarshalJSON() ([]byte, error) {
	type wireCredits struct {
		AvailableCount *int                        `json:"availableCount,omitempty"`
		Credits        []codexRateLimitResetCredit `json:"credits,omitempty"`
	}
	wire := wireCredits{Credits: credits.Credits}
	if credits.AvailableCountKnown {
		count := credits.AvailableCount
		wire.AvailableCount = &count
	}
	return json.Marshal(wire)
}

func (credits *codexRateLimitResetCredits) UnmarshalJSON(data []byte) error {
	type wireCredits struct {
		AvailableCount *int                        `json:"availableCount"`
		Credits        []codexRateLimitResetCredit `json:"credits"`
	}
	var wire wireCredits
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	credits.AvailableCount = 0
	credits.AvailableCountKnown = false
	credits.Credits = wire.Credits
	credits.AvailableCountKnown = wire.AvailableCount != nil
	if wire.AvailableCount != nil {
		credits.AvailableCount = *wire.AvailableCount
	}
	return nil
}

type codexRateLimitResetCredit struct {
	ID          string `json:"id"`
	ResetType   string `json:"resetType"`
	Status      string `json:"status"`
	ExpiresAt   int64  `json:"expiresAt"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func collectCodexSubscriptionLimitsWithClock(now time.Time, refreshInterval time.Duration, clock usageRefreshClock) (Allowance, []ExtraLimit, []QuotaBucket, []UsageReset, map[string]any, time.Time, error) {
	limits, refresh, err := collectCachedCodexRateLimitsWithClock(codexUsageCachePath(), refreshInterval, false, fetchCodexRateLimits, clock)
	if err != nil {
		return makeUnknownAllowance("weekly", now), nil, nil, nil, usageRefreshMeta(refresh), refresh.FetchedAt, err
	}
	snapshot := limits.RateLimits
	if byID := limits.RateLimitsByLimitID["codex"]; byID.LimitID != "" {
		snapshot = byID
	}
	weekly := codexSnapshotWeeklyAllowance(snapshot, now)
	resets := codexAvailableResets(limits.RateLimitResetCredits, now)
	meta := codexSnapshotMeta(snapshot)
	mergeUsageRefreshMeta(meta, refresh)
	if limits.RateLimitResetCredits.AvailableCountKnown {
		meta["availableResetCount"] = limits.RateLimitResetCredits.AvailableCount
	}
	var additional []QuotaBucket
	if bucket, ok := codexCreditsBucket(snapshot); ok {
		additional = append(additional, bucket)
		meta["creditsUnlimited"] = snapshot.Credits.Unlimited
		if balance := snapshot.Credits.balanceLabel(); balance != "" {
			meta["creditsBalance"] = balance
		}
	} else if snapshot.Credits != nil {
		// An emptied balance hides the bucket; the top-up ledger still
		// needs the zero so a later refill counts as a top-up.
		meta[emptyCreditBucketMeta] = "codex-credits"
	}
	return weekly, codexSnapshotExtraLimits(limits.RateLimitsByLimitID, snapshot.LimitID, now), additional, resets, meta, refresh.FetchedAt, nil
}

func fetchCodexRateLimits() (codexRateLimitsResult, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return codexRateLimitsResult{}, errors.New("Codex CLI is unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://", "--analytics-default-enabled")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return codexRateLimitsResult{}, errors.New("Codex usage protocol is unavailable")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return codexRateLimitsResult{}, errors.New("Codex usage protocol is unavailable")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return codexRateLimitsResult{}, errors.New("Codex usage protocol is unavailable")
	}

	_, _ = io.WriteString(stdin, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"dankaiusage","title":"DankAIUsage","version":"`+buildVersion()+`"},"capabilities":null}}`+"\n")
	_, _ = io.WriteString(stdin, `{"id":2,"method":"account/rateLimits/read","params":null}`+"\n")
	defer stdin.Close()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var envelope struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil || envelope.ID != 2 {
			continue
		}
		var response codexRateLimitResponse
		if err := json.Unmarshal(line, &response); err != nil {
			_ = stdin.Close()
			cancel()
			_ = cmd.Wait()
			return codexRateLimitsResult{}, errors.New("Codex usage response could not be read")
		}
		if response.Error != nil {
			_ = stdin.Close()
			cancel()
			_ = cmd.Wait()
			return codexRateLimitsResult{}, errors.New("Codex usage request was rejected")
		}
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		return codexRateLimitsResult{
			RateLimits:            response.Result.RateLimits,
			RateLimitsByLimitID:   response.Result.RateLimitsByLimitID,
			RateLimitResetCredits: response.Result.RateLimitResetCredits,
		}, nil
	}
	if err := scanner.Err(); err != nil {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		return codexRateLimitsResult{}, errors.New("Codex usage response could not be read")
	}
	_ = stdin.Close()
	err = cmd.Wait()
	return codexRateLimitsResult{}, errors.New("Codex rate limits are unavailable")
}

// codexSnapshotWeeklyAllowance returns the snapshot's weekly window. Codex
// sends it in either slot, so each window is classified by its duration: a
// window without a usable duration counts as weekly, and one of a known
// length outside a week (the former five-hour window) is ignored.
func codexSnapshotWeeklyAllowance(snapshot codexRateLimitSnapshot, now time.Time) Allowance {
	weekly := makeUnknownAllowance("weekly", time.Time{})
	weekly.Source = "codex app-server"
	for _, window := range []*codexRateLimitWindow{snapshot.Primary, snapshot.Secondary} {
		if window == nil || !codexWeeklyWindow(window) {
			continue
		}
		weekly = makeSubscriptionAllowance("weekly", "codex app-server", window.UsedPercent, codexResetTime(window, time.Time{}), codexWindowMinutes(window))
	}
	return weekly
}

// codexWeeklyWindow accepts six to eight days so a slightly different
// rounding of the 10,080-minute window still counts as a week.
func codexWeeklyWindow(window *codexRateLimitWindow) bool {
	minutes := codexWindowMinutes(window)
	return minutes <= 0 || (minutes >= 6*24*60 && minutes <= 8*24*60)
}

func codexSnapshotExtraLimits(snapshots map[string]codexRateLimitSnapshot, primaryLimitID string, now time.Time) []ExtraLimit {
	if len(snapshots) == 0 {
		return nil
	}
	keys := make([]string, 0, len(snapshots))
	for key := range snapshots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ExtraLimit, 0, len(keys))
	for _, key := range keys {
		snapshot := snapshots[key]
		id := firstNonEmpty(snapshot.LimitID, key)
		if id == "" || id == "codex" || id == primaryLimitID {
			continue
		}
		weekly := codexSnapshotWeeklyAllowance(snapshot, now)
		if !weekly.Known {
			continue
		}
		out = append(out, ExtraLimit{
			ID:     id,
			Label:  firstNonEmpty(snapshot.LimitName, id),
			Weekly: &weekly,
		})
	}
	return out
}

func codexAvailableResets(credits codexRateLimitResetCredits, now time.Time) []UsageReset {
	resets := make([]UsageReset, 0, len(credits.Credits))
	for _, credit := range credits.Credits {
		if credit.Status != "available" || (credit.ExpiresAt > 0 && !time.Unix(credit.ExpiresAt, 0).After(now)) {
			continue
		}
		reset := UsageReset{
			ID:          credit.ID,
			Title:       firstNonEmpty(credit.Title, "Usage reset"),
			Description: credit.Description,
			ResetType:   credit.ResetType,
		}
		if credit.ExpiresAt > 0 {
			reset.ExpiresAt = time.Unix(credit.ExpiresAt, 0).Format(time.RFC3339)
		}
		resets = append(resets, reset)
	}
	return resets
}

func codexSnapshotMeta(snapshot codexRateLimitSnapshot) map[string]any {
	meta := map[string]any{
		"limitId":  snapshot.LimitID,
		"planType": snapshot.PlanType,
	}
	if snapshot.LimitName != "" {
		meta["limitName"] = snapshot.LimitName
	}
	if snapshot.RateLimitReachedType != nil {
		meta["rateLimitReachedType"] = snapshot.RateLimitReachedType
	}
	return meta
}

func codexResetTime(window *codexRateLimitWindow, fallback time.Time) time.Time {
	if window == nil || window.ResetsAt == nil || *window.ResetsAt <= 0 {
		return fallback
	}
	return time.Unix(*window.ResetsAt, 0).Local()
}

func codexWindowMinutes(window *codexRateLimitWindow) int64 {
	if window == nil || window.WindowDurationMins == nil {
		return 0
	}
	return *window.WindowDurationMins
}

// The quota poll asks for windows and spend only; granted limit resets come
// from the separate reset check in claude_reset.go (ADR-0023).
const claudeOAuthUsageURL = "https://api.anthropic.com/api/oauth/usage"
const claudeOAuthUsageSource = "claude usage api"
const claudeOAuthUsageStaleTTL = 30 * time.Minute
const claudeFallbackCodeVersion = "2.1.185"

type claudeOAuthUsageCache struct {
	FetchedAt                 string          `json:"fetchedAt,omitempty"`
	Body                      json.RawMessage `json:"body,omitempty"`
	NextAttemptAt             string          `json:"nextAttemptAt,omitempty"`
	LastError                 string          `json:"lastError,omitempty"`
	DiagnosticCategory        string          `json:"diagnosticCategory,omitempty"`
	DiagnosticHTTPStatus      int             `json:"diagnosticHttpStatus,omitempty"`
	DiagnosticCooldownSeconds int64           `json:"diagnosticCooldownSeconds,omitempty"`
	ClaudeVersion             string          `json:"claudeVersion,omitempty"`
	Invalidated               bool            `json:"invalidated,omitempty"`
	OrganizationID            string          `json:"organizationId,omitempty"`
	// RateLimitedSince marks the first of the consecutive HTTP 429 answers a
	// reset check has received and RateLimitStreak counts them; the quota
	// cache leaves both empty (ADR-0023).
	RateLimitedSince string `json:"rateLimitedSince,omitempty"`
	RateLimitStreak  int    `json:"rateLimitStreak,omitempty"`
	// LastSuccessAt is the reset check's last successful fetch. Unlike
	// FetchedAt it survives the expiry after a used reset (ADR-0023).
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
}

func claudeOAuthUsageCachePath() string {
	return filepath.Join(pluginStateDir(), "claude-oauth-usage.json")
}

func loadClaudeOAuthUsageCache(path string) claudeOAuthUsageCache {
	cache, _ := loadClaudeOAuthUsageCacheStrict(path)
	return cache
}

func loadClaudeOAuthUsageCacheStrict(path string) (claudeOAuthUsageCache, error) {
	var cache claudeOAuthUsageCache
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil {
		return cache, err
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return claudeOAuthUsageCache{}, fmt.Errorf("%w: Claude cache unreadable", errUsageRefreshState)
	}
	if cache.FetchedAt == "" && cache.NextAttemptAt == "" && len(cache.Body) == 0 && cache.LastError == "" && cache.ClaudeVersion == "" && !cache.Invalidated {
		return claudeOAuthUsageCache{}, fmt.Errorf("%w: Claude cache incomplete", errUsageRefreshState)
	}
	return cache, nil
}

func saveClaudeOAuthUsageCache(path string, cache claudeOAuthUsageCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return atomicWriteUsageRefreshFile(path, data)
}

func readClaudeOAuthToken(now time.Time) (string, string, error) {
	data, err := os.ReadFile(filepath.Join(claudeHome(), ".credentials.json"))
	if err != nil {
		return "", "", errors.New("Claude credentials not found; sign in with Claude Code once")
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken      string `json:"accessToken"`
			ExpiresAt        int64  `json:"expiresAt"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", "", errors.New("Claude credentials could not be parsed")
	}
	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", "", errors.New("Claude credentials have no OAuth token")
	}
	if creds.ClaudeAiOauth.ExpiresAt > 0 && time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt).Before(now) {
		return "", "", errors.New("Claude OAuth token expired; run any Claude Code session to refresh it")
	}
	return creds.ClaudeAiOauth.AccessToken, creds.ClaudeAiOauth.SubscriptionType, nil
}

func claudeCodeVersionString(cached string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err == nil {
		fields := strings.Fields(strings.TrimSpace(string(out)))
		if len(fields) > 0 && fields[0] != "" {
			return fields[0]
		}
	}
	if cached != "" {
		return cached
	}
	return claudeFallbackCodeVersion
}

// claudeQuotaUserAgent identifies the quota poll as Claude Code. Anonymous
// user agents land in an aggressively rate-limited bucket with persistent
// 429s (ADR-0001); this identity has polled cleanly since July 2026.
func claudeQuotaUserAgent(version string) string {
	return "claude-code/" + version
}

// claudeResetClientUserAgent is the User-Agent of Claude Code's own OAuth API
// client. The usage endpoint reports limit resets only for that surface, but
// from 2026-10-03 it answers 429 when the identity is polled at quota cadence,
// so only the low-cadence reset check and the claim request use it (ADR-0023).
func claudeResetClientUserAgent(version string) string {
	return "claude-cli/" + version + " (external, cli)"
}

func setClaudeOAuthHeaders(req *http.Request, token string, userAgent string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
}

func fetchClaudeOAuthUsage(token string, version string) ([]byte, time.Duration, error) {
	return fetchClaudeOAuthJSON(claudeOAuthUsageURL, token, claudeQuotaUserAgent(version))
}

// fetchClaudeOAuthJSON performs one authenticated GET against the OAuth usage
// API and maps the response to the shared backoff rules.
func fetchClaudeOAuthJSON(endpoint string, token string, userAgent string) ([]byte, time.Duration, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 5 * time.Minute, err
	}
	setClaudeOAuthHeaders(req, token, userAgent)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 2 * time.Minute, fmt.Errorf("Claude usage API unreachable: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 2 * time.Minute, err
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		retryAfter := claudeRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now())
		return nil, max(claudeUsageRetryMinimum, retryAfter), &claudeRateLimitError{retryAfter: retryAfter}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, 5 * time.Minute, fmt.Errorf("Claude usage API auth failed (HTTP %d); run any Claude Code session to refresh sign-in", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, 5 * time.Minute, fmt.Errorf("Claude usage API returned HTTP %d", resp.StatusCode)
	}
	return body, 0, nil
}

// claudeUsageRetryMinimum is the shortest backoff after an HTTP 429 from the
// usage API, whatever Retry-After says (ADR-0001).
const claudeUsageRetryMinimum = 15 * time.Minute

// claudeRateLimitError is an HTTP 429 from the usage API with the wait the
// server asked for; zero when it sent none, or told the client not to wait.
type claudeRateLimitError struct {
	retryAfter time.Duration
}

func (e *claudeRateLimitError) Error() string {
	return "Claude usage API rate limited (HTTP 429)"
}

// claudeRetryAfterHeader parses a Retry-After header into a wait, zero when
// the header is missing, malformed, or already in the past.
func claudeRetryAfterHeader(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		const longest = time.Duration(1<<63 - 1)
		if seconds > int64(longest/time.Second) {
			return longest
		}
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return max(0, deadline.Sub(now))
	}
	return 0
}

func claudeUsageRetryDelay(value string, now time.Time) time.Duration {
	return max(claudeUsageRetryMinimum, claudeRetryAfterHeader(value, now))
}

func parseClaudeOAuthUsage(data []byte, now time.Time) (Allowance, []ExtraLimit, []QuotaBucket, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return makeUnknownAllowance("weekly", now), nil, nil, err
	}
	weekly := parseClaudeLimitWindow("weekly", claudeOAuthUsageSource, firstMap(root, "seven_day", "sevenDay"), now)
	if weekly.Known && weekly.WindowMinutes == 0 {
		weekly.WindowMinutes = 10080
	}
	extras := parseClaudeScopedLimits(root, now)
	extras = parseClaudeTopLevelScopedLimits(root, now, extras)
	additional := parseClaudeSpendBuckets(root)
	if !weekly.Known {
		return weekly, extras, additional, errors.New("Claude usage API returned no weekly window")
	}
	return weekly, extras, additional, nil
}

// parseClaudeScopedLimits extracts model- or surface-scoped weekly windows
// from the structured limits array (e.g. the Fable-only weekly cap). The
// weekly_all entry duplicates seven_day and is skipped, as are entries
// naming a five-hour or session window, which no longer exist.
func parseClaudeScopedLimits(root map[string]any, now time.Time) []ExtraLimit {
	items, ok := root["limits"].([]any)
	if !ok {
		return nil
	}
	var out []ExtraLimit
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind := stringValue(entry["kind"])
		kindLower := strings.ToLower(kind)
		if kindLower == "" || kindLower == "weekly" || kindLower == "weekly_all" || kindLower == "seven_day" {
			continue
		}
		if claudeShortWindowLimit(kind, stringValue(entry["group"])) {
			continue
		}
		used, ok := firstFloat(entry, "percent", "utilization", "used_percentage", "usedPercent", "used_percent", "percentage", "percent_used", "percentUsed")
		if !ok {
			continue
		}
		label := firstNonEmpty(stringValue(entry["label"]), stringValue(entry["display_name"]), stringValue(entry["displayName"]), stringValue(entry["name"]), humanizeScopedLimitLabel(kind))
		id := firstNonEmpty(stringValue(entry["id"]), kind)
		if scope := firstMap(entry, "scope"); scope != nil {
			if model := firstMap(scope, "model"); model != nil {
				label = firstNonEmpty(stringValue(model["display_name"]), stringValue(model["id"]), label)
				id = firstNonEmpty(stringValue(model["id"]), id)
			}
		}
		allowance := makeSubscriptionAllowance("weekly", claudeOAuthUsageSource, used, parseReset(entry, time.Time{}), 10080)
		out = upsertExtraLimit(out, id, label, allowance)
	}
	return out
}

func parseClaudeTopLevelScopedLimits(root map[string]any, now time.Time, out []ExtraLimit) []ExtraLimit {
	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values, ok := root[key].(map[string]any)
		if !ok {
			continue
		}
		suffix, ok := claudeTopLevelScopedLimitKey(key)
		if !ok {
			continue
		}
		allowance := parseClaudeLimitWindow("weekly", claudeOAuthUsageSource, values, now)
		if !allowance.Known {
			continue
		}
		if allowance.WindowMinutes == 0 {
			allowance.WindowMinutes = 10080
		}
		out = upsertExtraLimit(out, key, humanizeScopedLimitLabel(suffix), allowance)
	}
	return out
}

func claudeTopLevelScopedLimitKey(key string) (string, bool) {
	switch {
	case strings.HasPrefix(key, "seven_day_"):
		return strings.TrimPrefix(key, "seven_day_"), true
	case strings.HasPrefix(key, "sevenDay"):
		suffix := strings.TrimPrefix(key, "sevenDay")
		return suffix, suffix != ""
	default:
		return "", false
	}
}

// claudeShortWindowLimit reports whether a limits-array entry names a
// five-hour or session window, so it is not mistaken for a weekly one.
func claudeShortWindowLimit(kind string, group string) bool {
	text := strings.ToLower(kind + " " + group)
	return strings.Contains(text, "session") || strings.Contains(text, "five_hour") || strings.Contains(text, "fivehour")
}

func upsertExtraLimit(limits []ExtraLimit, id string, label string, allowance Allowance) []ExtraLimit {
	id = firstNonEmpty(id, label)
	label = firstNonEmpty(label, id)
	for i := range limits {
		if limits[i].ID != id && (label == "" || limits[i].Label != label) {
			continue
		}
		limits[i].Weekly = &allowance
		return limits
	}
	return append(limits, ExtraLimit{ID: id, Label: label, Weekly: &allowance})
}

func humanizeScopedLimitLabel(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		lower := strings.ToLower(part)
		switch lower {
		case "", "weekly", "seven", "day", "model", "only", "all":
			continue
		case "gpt", "api":
			out = append(out, strings.ToUpper(lower))
		case "oauth":
			out = append(out, "OAuth")
		default:
			out = append(out, strings.ToUpper(lower[:1])+lower[1:])
		}
	}
	if len(out) == 0 {
		return value
	}
	return strings.Join(out, " ")
}

func parseClaudeSpendBuckets(root map[string]any) []QuotaBucket {
	if bucket, ok := parseClaudeSpendBucket(firstMap(root, "spend")); ok {
		return []QuotaBucket{bucket}
	}
	if bucket, ok := parseClaudeExtraUsageBucket(firstMap(root, "extra_usage", "extraUsage")); ok {
		return []QuotaBucket{bucket}
	}
	return nil
}

func parseClaudeSpendBucket(spend map[string]any) (QuotaBucket, bool) {
	if spend == nil || !mapBool(spend, "enabled") {
		return QuotaBucket{}, false
	}
	usedMap := firstMap(spend, "used")
	limitMap := firstMap(spend, "limit")
	balanceMap := firstMap(spend, "balance")
	used, usedOK := firstFloat(usedMap, "amount_minor", "amountMinor")
	limit, limitOK := firstFloat(limitMap, "amount_minor", "amountMinor")
	balance, balanceOK := firstFloat(balanceMap, "amount_minor", "amountMinor")
	currency := firstNonEmpty(stringValue(usedMap["currency"]), stringValue(limitMap["currency"]), stringValue(balanceMap["currency"]), "USD")
	exponent := float64(2)
	if value, ok := firstFloat(usedMap, "exponent"); ok {
		exponent = value
	} else if value, ok := firstFloat(limitMap, "exponent"); ok {
		exponent = value
	} else if value, ok := firstFloat(balanceMap, "exponent"); ok {
		exponent = value
	}
	if usedOK && limitOK && limit > 0 {
		bucket := makeClaudeSpendBucket(int64(used), int64(limit), currency, int(exponent))
		if balanceOK && balance >= 0 {
			bucket.Detail += " · " + formatMinorMoney(int64(balance), currency, int(exponent)) + " prepaid balance"
		}
		return bucket, true
	}
	if !balanceOK || balance < 0 {
		return QuotaBucket{}, false
	}
	// Prepaid credits without a monthly spend limit: a balance, not a window.
	allowance := makeUnknownAllowance("credits", time.Time{})
	allowance.Unit = "currency"
	allowance.Source = claudeOAuthUsageSource
	label := formatMinorMoney(int64(balance), currency, int(exponent))
	major := minorToMajor(int64(balance), int(exponent))
	return QuotaBucket{
		ID:         "claude-extra-usage",
		Label:      "Extra usage credits",
		Kind:       "credits",
		Allowance:  allowance,
		ValueLabel: label,
		Detail:     label + " prepaid balance",
		Balance:    &major,
	}, true
}

func parseClaudeExtraUsageBucket(extra map[string]any) (QuotaBucket, bool) {
	if extra == nil || !mapBool(extra, "is_enabled", "isEnabled") {
		return QuotaBucket{}, false
	}
	used, usedOK := firstFloat(extra, "used_credits", "usedCredits")
	limit, limitOK := firstFloat(extra, "monthly_limit", "monthlyLimit")
	if !usedOK || !limitOK || limit <= 0 {
		return QuotaBucket{}, false
	}
	currency := firstNonEmpty(stringValue(extra["currency"]), "USD")
	exponent := float64(2)
	if value, ok := firstFloat(extra, "decimal_places", "decimalPlaces"); ok {
		exponent = value
	}
	return makeClaudeSpendBucket(int64(used), int64(limit), currency, int(exponent)), true
}

func makeClaudeSpendBucket(used int64, limit int64, currency string, exponent int) QuotaBucket {
	allowance := makeAllowance("monthly", used, limit, time.Time{})
	allowance.Unit = "currency"
	allowance.Source = claudeOAuthUsageSource
	return QuotaBucket{
		ID:         "claude-extra-usage",
		Label:      "Extra usage credits",
		Kind:       "credits",
		Allowance:  allowance,
		ValueLabel: formatMinorMoney(used, currency, exponent) + " / " + formatMinorMoney(limit, currency, exponent),
		Detail:     formatMinorMoney(allowance.Remaining, currency, exponent) + " remaining",
	}
}

func minorToMajor(amount int64, exponent int) float64 {
	if exponent < 0 || exponent > 6 {
		exponent = 2
	}
	return float64(amount) / math.Pow(10, float64(exponent))
}

func formatMinorMoney(amount int64, currency string, exponent int) string {
	if exponent < 0 || exponent > 6 {
		exponent = 2
	}
	factor := int64(1)
	for range exponent {
		factor *= 10
	}
	prefix := currency + " "
	if currency == "USD" {
		prefix = "$"
	}
	if exponent == 0 {
		return fmt.Sprintf("%s%d", prefix, amount)
	}
	return fmt.Sprintf("%s%d.%0*d", prefix, amount/factor, exponent, amount%factor)
}

func mapBool(values map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := values[key].(bool); ok {
			return value
		}
	}
	return false
}

func claudeOAuthUsageMeta(path string, cache claudeOAuthUsageCache) map[string]any {
	return map[string]any{
		"source":          claudeOAuthUsageSource,
		"limitDataSource": "Anthropic usage API",
		"usageCache":      path,
		"usageFetchedAt":  cache.FetchedAt,
	}
}

func claudeOAuthAllowancesFromCache(path string, cache claudeOAuthUsageCache, now time.Time, stale bool) (Allowance, []ExtraLimit, []QuotaBucket, map[string]any, error) {
	weekly, extras, additional, err := parseClaudeOAuthUsage(cache.Body, now)
	if err != nil {
		return weekly, extras, additional, nil, err
	}
	meta := claudeOAuthUsageMeta(path, cache)
	if stale {
		meta["usageDataStale"] = true
	}
	return weekly, extras, additional, meta, nil
}

func collectClaudeOAuthLimitsWithClock(clock usageRefreshClock, interval time.Duration, requireFresh bool) (Allowance, []ExtraLimit, []QuotaBucket, map[string]any, usageRefreshInfo, error) {
	interval = normalizeUsageRefreshInterval(interval)
	path := claudeOAuthUsageCachePath()
	var weekly Allowance
	var extras []ExtraLimit
	var additional []QuotaBucket
	var meta map[string]any
	var info usageRefreshInfo
	var actionErr error
	var recovered bool
	err := withUsageRefreshLock(path, func() error {
		// A concurrent refresh may be newer than this summary's start time.
		// Sample under the lock before validating or reserving shared state.
		now := clock()
		cache, err := loadClaudeOAuthUsageCacheStrict(path)
		if err != nil {
			return err
		}
		fetchedAt, err := parseOptionalRefreshTime(cache.FetchedAt)
		if err != nil {
			return err
		}
		nextAttemptAt, err := parseOptionalRefreshTime(cache.NextAttemptAt)
		if err != nil {
			return err
		}
		configuredNext := time.Time{}
		if !fetchedAt.IsZero() {
			configuredNext = fetchedAt.Add(interval)
		}
		if configuredNext.After(nextAttemptAt) {
			nextAttemptAt = configuredNext
		}
		if fetchedAt.After(now) {
			return fmt.Errorf("%w: %w: Claude cache future timestamp", errUsageRefreshState, errUsageRefreshTimestamp)
		}
		info = usageRefreshInfo{
			FetchedAt: fetchedAt, NextAttemptAt: nextAttemptAt, LastError: cache.LastError,
			DiagnosticCategory: cache.DiagnosticCategory, DiagnosticHTTPStatus: cache.DiagnosticHTTPStatus,
			DiagnosticCooldownSeconds: cache.DiagnosticCooldownSeconds,
		}
		if info.DiagnosticCategory == "" && cache.LastError != "" {
			info.DiagnosticCategory = "provider_unavailable"
		}
		cacheUsable := len(cache.Body) > 0 && !fetchedAt.IsZero() && !cache.Invalidated
		staleTTL := max(claudeOAuthUsageStaleTTL, 2*interval)
		staleOK := cacheUsable && now.Sub(fetchedAt) < staleTTL
		if now.Before(nextAttemptAt) {
			if cacheUsable && (cache.LastError == "" || staleOK) && !requireFresh {
				weekly, extras, additional, meta, actionErr = claudeOAuthAllowancesFromCache(path, cache, now, cache.LastError != "")
				if actionErr != nil {
					return fmt.Errorf("%w: Claude cache response unsupported", errUsageRefreshState)
				}
				info.Cached = true
				info.Stale = cache.LastError != ""
				mergeUsageRefreshMeta(meta, info)
				return nil
			}
			info.Pending = cache.Invalidated
			actionErr = errUsageRefreshCoolingDown
			return nil
		}

		// Reserve before any provider request so a failed final cache write does
		// not permit another process to immediately repeat the call.
		cache.NextAttemptAt = now.Add(interval).UTC().Format(time.RFC3339Nano)
		if err := saveClaudeOAuthUsageCache(path, cache); err != nil {
			return errors.New("could not reserve Claude usage refresh")
		}

		token, subscription, err := readClaudeOAuthToken(now)
		if err != nil {
			cache.LastError = err.Error()
			cache.DiagnosticCategory, cache.DiagnosticHTTPStatus = classifyDiagnosticError("claude", err)
			cache.DiagnosticCooldownSeconds = int64(interval / time.Second)
			info.DiagnosticCategory, info.DiagnosticHTTPStatus, info.DiagnosticCooldownSeconds = cache.DiagnosticCategory, cache.DiagnosticHTTPStatus, cache.DiagnosticCooldownSeconds
			if saveErr := saveClaudeOAuthUsageCache(path, cache); saveErr != nil {
				return errors.New("could not save Claude usage refresh failure")
			}
			if staleOK && !requireFresh {
				weekly, extras, additional, meta, actionErr = claudeOAuthAllowancesFromCache(path, cache, now, true)
				if actionErr != nil {
					return fmt.Errorf("%w: Claude cache response unsupported", errUsageRefreshState)
				}
				info.Cached, info.Stale, info.LastError = true, true, err.Error()
				mergeUsageRefreshMeta(meta, info)
				return nil
			}
			actionErr = err
			return nil
		}
		// Read alongside the token, before the request, so an account switch
		// during the fetch cannot tag this body with the next sign-in.
		organization, _ := claudeOrganizationID()
		cache.ClaudeVersion = claudeCodeVersionString(cache.ClaudeVersion)
		body, backoff, err := fetchClaudeOAuthUsage(token, cache.ClaudeVersion)
		if err != nil {
			cache.LastError = err.Error()
			retryAt := now.Add(max(interval, backoff))
			cache.DiagnosticCategory, cache.DiagnosticHTTPStatus = classifyDiagnosticError("claude", err)
			cache.DiagnosticCooldownSeconds = min(int64(max(interval, backoff)/time.Second), int64(diagnosticMaxCooldown/time.Second))
			info.DiagnosticCategory, info.DiagnosticHTTPStatus, info.DiagnosticCooldownSeconds = cache.DiagnosticCategory, cache.DiagnosticHTTPStatus, cache.DiagnosticCooldownSeconds
			cache.NextAttemptAt = retryAt.UTC().Format(time.RFC3339Nano)
			info.NextAttemptAt = retryAt
			if saveErr := saveClaudeOAuthUsageCache(path, cache); saveErr != nil {
				return errors.New("could not save Claude usage refresh failure")
			}
			if staleOK && !requireFresh {
				weekly, extras, additional, meta, actionErr = claudeOAuthAllowancesFromCache(path, cache, now, true)
				if actionErr != nil {
					return fmt.Errorf("%w: Claude cache response unsupported", errUsageRefreshState)
				}
				info.Cached, info.Stale, info.LastError = true, true, err.Error()
				mergeUsageRefreshMeta(meta, info)
				return nil
			}
			actionErr = err
			return nil
		}
		weekly, extras, additional, err = parseClaudeOAuthUsage(body, now)
		if err != nil {
			cache.LastError = "Claude usage API returned an unsupported response"
			cache.DiagnosticCategory = "invalid_response"
			cache.DiagnosticHTTPStatus = 0
			cache.DiagnosticCooldownSeconds = int64(max(interval, 5*time.Minute) / time.Second)
			info.DiagnosticCategory, info.DiagnosticHTTPStatus, info.DiagnosticCooldownSeconds = cache.DiagnosticCategory, cache.DiagnosticHTTPStatus, cache.DiagnosticCooldownSeconds
			cache.NextAttemptAt = now.Add(max(interval, 5*time.Minute)).UTC().Format(time.RFC3339Nano)
			if saveErr := saveClaudeOAuthUsageCache(path, cache); saveErr != nil {
				return errors.New("could not save Claude usage refresh failure")
			}
			if staleOK && !requireFresh {
				weekly, extras, additional, meta, actionErr = claudeOAuthAllowancesFromCache(path, cache, now, true)
				if actionErr != nil {
					return fmt.Errorf("%w: Claude cache response unsupported", errUsageRefreshState)
				}
				info.Cached, info.Stale, info.LastError = true, true, cache.LastError
				mergeUsageRefreshMeta(meta, info)
				return nil
			}
			actionErr = errors.New(cache.LastError)
			return nil
		}
		recovered = cache.LastError != "" || cache.DiagnosticCategory != ""
		cache.Body = body
		// Remember whose usage this is, so limit resets listed from the cache
		// are never redeemed under a different sign-in (ADR-0022).
		cache.OrganizationID = organization
		cache.FetchedAt = now.UTC().Format(time.RFC3339Nano)
		cache.NextAttemptAt = now.Add(interval).UTC().Format(time.RFC3339Nano)
		cache.LastError = ""
		cache.DiagnosticCategory = ""
		cache.DiagnosticHTTPStatus = 0
		cache.DiagnosticCooldownSeconds = 0
		cache.Invalidated = false
		if err := saveClaudeOAuthUsageCache(path, cache); err != nil {
			return errors.New("could not save refreshed Claude usage")
		}
		info = usageRefreshInfo{FetchedAt: now, NextAttemptAt: now.Add(interval)}
		meta = claudeOAuthUsageMeta(path, cache)
		mergeUsageRefreshMeta(meta, info)
		if subscription != "" {
			meta["subscriptionType"] = subscription
		}
		return nil
	})
	if err != nil {
		emitUsageRefreshDiagnostics("claude", info, err, nil, false)
		now := clock()
		return makeUnknownAllowance("weekly", now), nil, nil, nil, usageRefreshInfo{}, err
	}
	if actionErr != nil && errors.Is(actionErr, errUsageRefreshCoolingDown) {
		meta = usageRefreshMeta(info)
	}
	emitUsageRefreshDiagnostics("claude", info, nil, actionErr, recovered)
	return weekly, extras, additional, meta, info, actionErr
}

// collectClaudeSubscriptionLimitsWithClock reads Claude's limits from the
// OAuth usage API; when that fails the limits are unavailable and the error
// is returned for the dropdown to show.
func collectClaudeSubscriptionLimitsWithClock(now time.Time, refreshInterval time.Duration, clock usageRefreshClock) (Allowance, []ExtraLimit, []QuotaBucket, map[string]any, time.Time, error) {
	weekly, extras, additional, meta, info, err := collectClaudeOAuthLimitsWithClock(clock, refreshInterval, false)
	if err == nil {
		return weekly, extras, additional, meta, info.FetchedAt, nil
	}
	if meta == nil {
		meta = map[string]any{}
	}
	mergeUsageRefreshMeta(meta, info)
	return makeUnknownAllowance("weekly", now), nil, nil, meta, time.Time{}, err
}

func parseClaudeLimitWindow(window string, source string, values map[string]any, now time.Time) Allowance {
	_ = now
	if values == nil {
		return makeUnknownAllowance(window, time.Time{})
	}
	used, ok := firstFloat(values, "used_percentage", "utilization", "usedPercent", "used_percent", "percentage", "percent_used", "percentUsed")
	if !ok {
		return makeUnknownAllowance(window, parseReset(values, time.Time{}))
	}
	windowMinutes, _ := firstFloat(values, "window_duration_mins", "windowDurationMins", "window_minutes", "windowMinutes")
	return makeSubscriptionAllowance(window, source, used, parseReset(values, time.Time{}), int64(windowMinutes))
}

func parseReset(values map[string]any, fallback time.Time) time.Time {
	for _, key := range []string{"resets_at", "resetsAt", "reset_at", "resetAt"} {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case float64:
			if v > 1_000_000_000_000 {
				return time.UnixMilli(int64(v)).Local()
			}
			if v > 0 {
				return time.Unix(int64(v), 0).Local()
			}
		case string:
			if ts, err := time.Parse(time.RFC3339, v); err == nil {
				return ts.Local()
			}
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				if n > 1_000_000_000_000 {
					return time.UnixMilli(n).Local()
				}
				return time.Unix(n, 0).Local()
			}
		}
	}
	return fallback
}

func firstMap(values map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if child, ok := values[key].(map[string]any); ok {
			return child
		}
	}
	return nil
}

func firstFloat(values map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch v := values[key].(type) {
		case float64:
			return v, true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			if n, err := v.Float64(); err == nil {
				return n, true
			}
		case string:
			if n, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func setProviderMeta(provider *ProviderUsage, key string, value any) {
	if provider.Meta == nil {
		provider.Meta = map[string]any{}
	}
	provider.Meta[key] = value
}

func mergeProviderMeta(provider *ProviderUsage, meta map[string]any) {
	for key, value := range meta {
		setProviderMeta(provider, key, value)
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func parseLogFields(body string) map[string]string {
	fields := map[string]string{}
	for _, part := range strings.Fields(body) {
		idx := strings.Index(part, "=")
		if idx <= 0 {
			continue
		}
		key := part[:idx]
		value := strings.Trim(part[idx+1:], `"`)
		fields[key] = value
	}
	return fields
}

func intField(fields map[string]string, key string) int64 {
	value, _ := strconv.ParseInt(fields[key], 10, 64)
	return value
}

func jsonInt(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	default:
		return 0
	}
}

func parseTime(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok || text == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, false
	}
	return t.Local(), true
}

func codexHome() string {
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return value
	}
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		if exists(filepath.Join(value, "codex")) {
			return filepath.Join(value, "codex")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func claudeHome() string {
	if value := os.Getenv("CLAUDE_CONFIG_DIR"); value != "" {
		return value
	}
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		if exists(filepath.Join(value, "claude")) {
			return filepath.Join(value, "claude")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func commandPath(name string) string {
	path, _ := exec.LookPath(name)
	return path
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
