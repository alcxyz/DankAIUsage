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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

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
	ID      string     `json:"id"`
	Label   string     `json:"label"`
	Session *Allowance `json:"sessionLeft,omitempty"`
	Weekly  *Allowance `json:"weeklyLeft,omitempty"`
}

type QuotaBucket struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Kind       string    `json:"kind"`
	Allowance  Allowance `json:"allowance"`
	ValueLabel string    `json:"valueLabel,omitempty"`
	Detail     string    `json:"detail,omitempty"`
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
	Session           PeriodTotals   `json:"session"`
	Week              PeriodTotals   `json:"week"`
	Month             PeriodTotals   `json:"month"`
	Period            PeriodTotals   `json:"period"`
	SessionLeft       Allowance      `json:"sessionLeft"`
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
}

type tokenEvent struct {
	Provider  string
	Timestamp time.Time
	Session   string
	Project   string
	Model     string
	Input     int64
	Output    int64
	Cached    int64
	Reasoning int64
	Tool      int64
}

type options struct {
	PeriodDays   int
	SessionHours int
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "claude-statusline" {
		if err := captureClaudeStatusline(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "claude statusline: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "claude-prime" {
		runClaudePrimeCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "codex-reset" {
		runCodexResetCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "history" {
		runUsageHistoryCommand(os.Args[2:])
		return
	}

	fs := flag.NewFlagSet("summary", flag.ExitOnError)
	periodDays := fs.Int("period-days", 7, "rolling period length in days")
	sessionHours := fs.Int("session-hours", 5, "rolling session window length in hours")
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	_ = fs.Parse(dropSummaryArg(os.Args[1:]))

	if *periodDays < 1 {
		*periodDays = 1
	}
	if *sessionHours < 1 {
		*sessionHours = 1
	}

	summary := collect(options{
		PeriodDays:   *periodDays,
		SessionHours: *sessionHours,
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
	now := time.Now()
	out := Summary{
		Version:     version,
		GeneratedAt: now.Format(time.RFC3339),
		PeriodDays:  opts.PeriodDays,
		History:     []UsageHistoryEvent{},
		Capabilities: map[string]bool{
			"codexCli":  hasCommand("codex"),
			"claudeCli": hasCommand("claude"),
			"sqlite3":   hasCommand("sqlite3"),
		},
	}

	codex := collectCodex(now, opts)
	claude := collectClaude(now, opts)
	out.Providers = []ProviderUsage{codex, claude}

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
	if sessionLeft, weeklyLeft, extraLimits, resets, meta, err := collectCodexSubscriptionLimits(now); err == nil {
		provider.SessionLeft = sessionLeft
		provider.WeeklyLeft = weeklyLeft
		provider.ExtraLimits = extraLimits
		provider.QuotaBuckets = makeQuotaBuckets(sessionLeft, weeklyLeft, extraLimits, nil)
		provider.Resets = resets
		provider.Meta = meta
	} else {
		mergeProviderMeta(&provider, meta)
		setProviderMeta(&provider, "limitError", err.Error())
	}

	if _, err := exec.LookPath("sqlite3"); err != nil {
		setProviderMeta(&provider, "tokenDataError", "sqlite3 not found")
		return provider
	}

	db := filepath.Join(root, "logs_2.sqlite")
	if _, err := os.Stat(db); err != nil {
		setProviderMeta(&provider, "tokenDataError", "Codex logs not found")
		return provider
	}

	start := now.AddDate(0, 0, -maxInt(opts.PeriodDays, 31)-1).Unix()
	query := fmt.Sprintf(`select ts, coalesce(thread_id,''), feedback_log_body from logs where target='codex_otel.trace_safe' and feedback_log_body like '%%event.name="codex.sse_event"%%' and feedback_log_body like '%%event.kind=response.completed%%' and ts >= %d order by ts asc;`, start)
	cmd := exec.Command("sqlite3", "-separator", "\t", db, query)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		setProviderMeta(&provider, "tokenDataError", msg)
		return provider
	}

	var events []tokenEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		ts, _ := strconv.ParseInt(parts[0], 10, 64)
		fields := parseLogFields(parts[2])
		events = append(events, tokenEvent{
			Provider:  "codex",
			Timestamp: time.Unix(ts, 0),
			Session:   firstNonEmpty(fields["conversation.id"], parts[1]),
			Model:     fields["model"],
			Input:     intField(fields, "input_token_count"),
			Output:    intField(fields, "output_token_count"),
			Cached:    intField(fields, "cached_token_count"),
			Reasoning: intField(fields, "reasoning_token_count"),
			Tool:      intField(fields, "tool_token_count"),
		})
	}
	if err := scanner.Err(); err != nil {
		setProviderMeta(&provider, "tokenDataError", err.Error())
	}

	applyEvents(&provider, events, now, opts)
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
	setProviderMeta(&provider, "limitDataSource", "Claude Code statusline")
	setProviderMeta(&provider, "limitDataIncludesWeb", true)
	root := claudeHome()
	provider.DataPath = root
	provider.historyObservedAt = time.Now()
	sessionLeft, weeklyLeft, extraLimits, additionalBuckets, meta, limitErr := collectClaudeSubscriptionLimits(now)
	provider.SessionLeft = sessionLeft
	provider.WeeklyLeft = weeklyLeft
	provider.ExtraLimits = extraLimits
	provider.QuotaBuckets = makeQuotaBuckets(sessionLeft, weeklyLeft, extraLimits, additionalBuckets)
	mergeProviderMeta(&provider, meta)
	if limitErr != nil {
		setProviderMeta(&provider, "limitError", limitErr.Error())
	}

	projects := filepath.Join(root, "projects")
	if _, err := os.Stat(projects); err != nil {
		setProviderMeta(&provider, "tokenDataError", "Claude projects not found")
		return provider
	}

	cutoff := now.AddDate(0, 0, -maxInt(opts.PeriodDays, 31)-1)
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
		session, _ := row["sessionId"].(string)
		model, _ := msg["model"].(string)
		events = append(events, tokenEvent{
			Provider:  "claude",
			Timestamp: ts,
			Session:   session,
			Project:   project,
			Model:     model,
			Input:     jsonInt(usage["input_tokens"]),
			Output:    jsonInt(usage["output_tokens"]),
			Cached:    jsonInt(usage["cache_creation_input_tokens"]) + jsonInt(usage["cache_read_input_tokens"]),
		})
	}
	return events, nil
}

func applyEvents(provider *ProviderUsage, events []tokenEvent, now time.Time, opts options) {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	sessionStart := now.Add(-time.Duration(opts.SessionHours) * time.Hour)
	weekdayOffset := int(now.Weekday())
	if weekdayOffset == 0 {
		weekdayOffset = 6
	} else {
		weekdayOffset--
	}
	weekStart := todayStart.AddDate(0, 0, -weekdayOffset)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	periodStart := now.AddDate(0, 0, -opts.PeriodDays)

	models := map[string]*ModelUsage{}
	periodSessions := map[string]bool{}
	todaySessions := map[string]bool{}
	sessionSessions := map[string]bool{}
	weekSessions := map[string]bool{}
	monthSessions := map[string]bool{}
	var sessionOldest *time.Time

	for _, event := range events {
		if event.Timestamp.After(todayStart) || event.Timestamp.Equal(todayStart) {
			addEvent(&provider.Today, event)
			if event.Session != "" {
				todaySessions[event.Session] = true
			}
		}
		if event.Timestamp.After(sessionStart) || event.Timestamp.Equal(sessionStart) {
			addEvent(&provider.Session, event)
			if event.Session != "" {
				sessionSessions[event.Session] = true
			}
			if sessionOldest == nil || event.Timestamp.Before(*sessionOldest) {
				ts := event.Timestamp
				sessionOldest = &ts
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
	provider.Session.Sessions = int64(len(sessionSessions))
	provider.Week.Sessions = int64(len(weekSessions))
	provider.Month.Sessions = int64(len(monthSessions))
	provider.Period.Sessions = int64(len(periodSessions))

	provider.Models = []ModelUsage{}
	for _, model := range models {
		provider.Models = append(provider.Models, *model)
	}
	sort.Slice(provider.Models, func(i, j int) bool {
		return provider.Models[i].Total > provider.Models[j].Total
	})

	sessionReset := now.Add(time.Duration(opts.SessionHours) * time.Hour)
	if sessionOldest != nil {
		sessionReset = sessionOldest.Add(time.Duration(opts.SessionHours) * time.Hour)
	}
	if !provider.SessionLeft.Known && provider.SessionLeft.Source == "" {
		provider.SessionLeft = makeUnknownAllowance("session", sessionReset)
	}
	if !provider.WeeklyLeft.Known && provider.WeeklyLeft.Source == "" {
		provider.WeeklyLeft = makeUnknownAllowance("weekly", weekStart.AddDate(0, 0, 7))
	}
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

func makeQuotaBuckets(session Allowance, weekly Allowance, extras []ExtraLimit, additional []QuotaBucket) []QuotaBucket {
	buckets := make([]QuotaBucket, 0, 2+len(extras)*2+len(additional))
	if session.Known {
		buckets = append(buckets, QuotaBucket{ID: "general-5-hour", Label: "5-hour", Kind: "short", Allowance: session})
	}
	if weekly.Known {
		buckets = append(buckets, QuotaBucket{ID: "general-weekly", Label: "Weekly", Kind: "weekly", Allowance: weekly})
	}
	for _, extra := range extras {
		if extra.Session != nil && extra.Session.Known {
			buckets = append(buckets, QuotaBucket{
				ID:        extra.ID + "-5-hour",
				Label:     extra.Label + " · 5-hour",
				Kind:      "scoped",
				Allowance: *extra.Session,
			})
		}
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

func collectCodexSubscriptionLimits(now time.Time) (Allowance, Allowance, []ExtraLimit, []UsageReset, map[string]any, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://", "--analytics-default-enabled")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
	}

	_, _ = io.WriteString(stdin, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"dankaiusage","title":"DankAIUsage","version":"`+version+`"},"capabilities":null}}`+"\n")
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
			return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
		}
		if response.Error != nil {
			_ = stdin.Close()
			cancel()
			_ = cmd.Wait()
			return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, fmt.Errorf("%v", response.Error)
		}
		snapshot := response.Result.RateLimits
		if byID := response.Result.RateLimitsByLimitID["codex"]; byID.LimitID != "" {
			snapshot = byID
		}
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		session, weekly := codexSnapshotWindowAllowances(snapshot, now)
		resets := codexAvailableResets(response.Result.RateLimitResetCredits, now)
		meta := codexSnapshotMeta(snapshot)
		if response.Result.RateLimitResetCredits.AvailableCountKnown {
			meta["availableResetCount"] = response.Result.RateLimitResetCredits.AvailableCount
		}
		return session, weekly, codexSnapshotExtraLimits(response.Result.RateLimitsByLimitID, snapshot.LimitID, now), resets, meta, nil
	}
	if err := scanner.Err(); err != nil {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
	}
	_ = stdin.Close()
	err = cmd.Wait()
	msg := strings.TrimSpace(stderr.String())
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if msg == "" {
		msg = "Codex rate limits unavailable"
	}
	return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, errors.New(msg)
}

func codexSnapshotAllowances(snapshot codexRateLimitSnapshot, now time.Time) Allowance {
	session, _ := codexSnapshotWindowAllowances(snapshot, now)
	return session
}

func codexSnapshotWeeklyAllowance(snapshot codexRateLimitSnapshot, now time.Time) Allowance {
	_, weekly := codexSnapshotWindowAllowances(snapshot, now)
	return weekly
}

// Codex no longer guarantees that primary means a short window and secondary
// means a weekly window. Classify each returned window by its duration so a
// weekly-only response is not rendered as a fake session quota.
func codexSnapshotWindowAllowances(snapshot codexRateLimitSnapshot, now time.Time) (Allowance, Allowance) {
	session := makeUnknownAllowance("session", time.Time{})
	weekly := makeUnknownAllowance("weekly", time.Time{})
	session.Source = "codex app-server"
	weekly.Source = "codex app-server"
	assign := func(window *codexRateLimitWindow, fallback string) {
		if window == nil {
			return
		}
		kind := codexWindowKind(window, fallback)
		allowance := makeSubscriptionAllowance(kind, "codex app-server", window.UsedPercent, codexResetTime(window, time.Time{}), codexWindowMinutes(window))
		if kind == "weekly" {
			weekly = allowance
		} else {
			session = allowance
		}
	}
	assign(snapshot.Primary, "session")
	assign(snapshot.Secondary, "weekly")
	return session, weekly
}

func codexWindowKind(window *codexRateLimitWindow, fallback string) string {
	minutes := codexWindowMinutes(window)
	if minutes >= 6*24*60 {
		return "weekly"
	}
	if minutes > 0 && minutes <= 24*60 {
		return "session"
	}
	return fallback
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
		session, weekly := codexSnapshotWindowAllowances(snapshot, now)
		if !session.Known && !weekly.Known {
			continue
		}
		extra := ExtraLimit{
			ID:    id,
			Label: firstNonEmpty(snapshot.LimitName, id),
		}
		if session.Known {
			extra.Session = &session
		}
		if weekly.Known {
			extra.Weekly = &weekly
		}
		out = append(out, extra)
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

func captureClaudeStatusline(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		_, _ = fmt.Fprintln(w, "Claude --")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(claudeStatuslineCachePath()), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(claudeStatuslineCachePath(), data, 0o600); err != nil {
		return err
	}

	status, err := parseClaudeStatusline(data, time.Now())
	if err != nil {
		_, _ = fmt.Fprintln(w, "Claude --")
		return nil
	}
	_, _ = fmt.Fprintf(w, "Claude S %s W %s\n", status.sessionLabel(), status.weeklyLabel())
	return nil
}

type claudeStatuslineLimits struct {
	Session Allowance
	Weekly  Allowance
	Model   string
	Version string
}

type claudePrimeResult struct {
	OK              bool      `json:"ok"`
	Message         string    `json:"message"`
	CachePath       string    `json:"cachePath"`
	CacheUpdated    bool      `json:"cacheUpdated"`
	SessionLeft     Allowance `json:"sessionLeft"`
	WeeklyLeft      Allowance `json:"weeklyLeft"`
	ClaudeSession   string    `json:"claudeSession,omitempty"`
	TotalCostUSD    float64   `json:"totalCostUsd,omitempty"`
	StatuslineError string    `json:"statuslineError,omitempty"`
}

type claudePrimeOptions struct {
	Prompt  string
	Model   string
	Timeout time.Duration
}

const claudePrimeSessionSource = "claude-prime local usage"
const defaultClaudePrimeModel = "sonnet"

// Floor between real prime requests. Protects against a burn loop if the
// usage API briefly keeps reporting the window as inactive after a prime.
const claudePrimeMinInterval = 15 * time.Minute

type claudePrimeCache struct {
	StartedAt string `json:"startedAt"`
	ResetAt   string `json:"resetAt"`
	UsageAt   string `json:"usageAt,omitempty"`
	Source    string `json:"source"`
}

func (limits claudeStatuslineLimits) sessionLabel() string {
	if !limits.Session.Known {
		return "--"
	}
	return fmt.Sprintf("%.0f%%", limits.Session.PercentRemaining)
}

func (limits claudeStatuslineLimits) weeklyLabel() string {
	if !limits.Weekly.Known {
		return "--"
	}
	return fmt.Sprintf("%.0f%%", limits.Weekly.PercentRemaining)
}

func runClaudePrimeCommand(args []string) {
	fs := flag.NewFlagSet("claude-prime", flag.ExitOnError)
	prompt := fs.String("prompt", "OK", "small prompt used to refresh Claude Code account limits")
	model := fs.String("model", defaultClaudePrimeModel, "Claude model alias or full model name used for the prime request")
	timeoutSeconds := fs.Int("timeout-seconds", 120, "maximum seconds to wait for Claude Code")
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	_ = fs.Parse(args)

	result, err := primeClaudeStatusline(claudePrimeOptions{
		Prompt:  *prompt,
		Model:   *model,
		Timeout: time.Duration(maxInt(*timeoutSeconds, 1)) * time.Second,
	})
	if err != nil {
		result.OK = false
		if result.Message == "" {
			result.Message = err.Error()
		}
	}
	var data []byte
	var encodeErr error
	if *pretty {
		data, encodeErr = json.MarshalIndent(result, "", "  ")
	} else {
		data, encodeErr = json.Marshal(result)
	}
	if encodeErr != nil {
		fmt.Fprintf(os.Stderr, "encode claude prime result: %v\n", encodeErr)
		os.Exit(1)
	}
	fmt.Println(string(data))
	if err != nil {
		fmt.Fprintln(os.Stderr, result.Message)
		os.Exit(1)
	}
}

// allowanceActive reports whether an allowance describes a subscription
// window that is still running. A cached reset time stays trustworthy
// regardless of cache age: a window cannot end before its own reset.
func allowanceActive(allowance Allowance, now time.Time) bool {
	if !allowance.Known || allowance.ResetAt == "" {
		return false
	}
	resetAt, err := time.Parse(time.RFC3339, allowance.ResetAt)
	return err == nil && resetAt.After(now.Add(time.Minute))
}

func claudeOAuthActiveSession(now time.Time) (Allowance, Allowance, bool) {
	cache := loadClaudeOAuthUsageCache(claudeOAuthUsageCachePath())
	if len(cache.Body) == 0 {
		return Allowance{}, Allowance{}, false
	}
	session, weekly, _, _, err := parseClaudeOAuthUsage(cache.Body, now)
	if err != nil {
		return Allowance{}, Allowance{}, false
	}
	return session, weekly, allowanceActive(session, now)
}

func recentlyPrimed(now time.Time) bool {
	data, err := os.ReadFile(claudePrimeCachePath())
	if err != nil {
		return false
	}
	var cache claudePrimeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return false
	}
	primedAt, err := time.Parse(time.RFC3339, firstNonEmpty(cache.UsageAt, cache.StartedAt))
	if err != nil {
		return false
	}
	age := now.Sub(primedAt)
	return age >= 0 && age < claudePrimeMinInterval
}

func primeClaudeStatusline(opts claudePrimeOptions) (claudePrimeResult, error) {
	path := claudeStatuslineCachePath()
	now := time.Now()
	result := claudePrimeResult{
		CachePath:   path,
		SessionLeft: makeUnknownAllowance("session", now),
		WeeklyLeft:  makeUnknownAllowance("weekly", now),
	}
	if configured, _ := claudeStatuslineSettings(); !configured {
		err := errors.New("Claude statusline is not configured")
		result.Message = "Configure statusLine.command to dankaiusage claude-statusline first"
		return result, err
	}
	if session, weekly, active := claudeOAuthActiveSession(now); active {
		result.SessionLeft = session
		result.WeeklyLeft = weekly
		result.OK = true
		result.Message = "Claude session already active"
		return result, nil
	}
	if recentlyPrimed(now) {
		if fallback, _, ok := claudePrimeSessionFallback(now); ok {
			result.SessionLeft = fallback
		}
		result.OK = true
		result.Message = "Claude prime ran recently; waiting for account usage data"
		return result, nil
	}
	oauthSession, oauthWeekly, _, _, _, oauthErr := collectClaudeOAuthLimits(now)
	if oauthErr == nil && allowanceActive(oauthSession, now) {
		result.SessionLeft = oauthSession
		result.WeeklyLeft = oauthWeekly
		result.OK = true
		result.Message = "Claude session already active"
		return result, nil
	}
	if oauthErr != nil {
		// Without account data the local five-hour timer is the only
		// guard left; honor it in full as before.
		if fallback, _, ok := claudePrimeSessionFallback(now); ok {
			result.SessionLeft = fallback
			result.OK = true
			result.Message = "Claude session timer already active"
			return result, nil
		}
	}
	if _, err := exec.LookPath("claude"); err != nil {
		result.Message = "Claude CLI not found"
		return result, err
	}

	oldMod := time.Time{}
	if info, err := os.Stat(path); err == nil {
		oldMod = info.ModTime()
	}
	started := time.Now()
	stdout, stderr, runErr := runClaudePrimeRequest(opts)
	if runErr != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = runErr.Error()
		}
		result.Message = "Claude prime request failed: " + msg
		return result, errors.New(result.Message)
	}
	mergeClaudePrimeOutput(&result, stdout)

	// Refresh the usage cache immediately so the widget (and the next
	// prime attempt) sees the new window without waiting out the TTL.
	// The local timer is still written as the fallback guard.
	if session, weekly, _, _, _, err := collectClaudeOAuthLimitsTTL(time.Now(), 0); err == nil && allowanceActive(session, time.Now()) {
		cachePrimeSession(started, time.Now())
		result.SessionLeft = session
		result.WeeklyLeft = weekly
		result.OK = true
		result.Message = "Claude session started; account limits refreshed"
		return result, nil
	}

	updated := waitForFreshClaudeStatusline(path, oldMod, started, 3*time.Second)
	result.CacheUpdated = updated
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		result.SessionLeft = cachePrimeSession(started, time.Now())
		result.Message = "Claude session started; account limits unavailable"
		result.OK = true
		return result, nil
	}
	limits, parseErr := parseClaudeStatusline(data, time.Now())
	if parseErr != nil {
		result.StatuslineError = parseErr.Error()
		result.SessionLeft = cachePrimeSession(started, time.Now())
		result.Message = "Claude session started; account limits unavailable"
		result.OK = true
		return result, nil
	}
	result.SessionLeft = limits.Session
	result.WeeklyLeft = limits.Weekly
	if !updated {
		result.Message = "Claude request completed, but statusline cache did not refresh"
		return result, errors.New(result.Message)
	}
	result.OK = true
	result.Message = "Claude account limits refreshed"
	return result, nil
}

func cachePrimeSession(started time.Time, usageAt time.Time) Allowance {
	sessionHours := 5
	resetAt := usageAt.Add(time.Duration(sessionHours) * time.Hour)
	cache := claudePrimeCache{
		StartedAt: started.Format(time.RFC3339),
		UsageAt:   usageAt.Format(time.RFC3339),
		ResetAt:   resetAt.Format(time.RFC3339),
		Source:    claudePrimeSessionSource,
	}
	_ = writeClaudePrimeCache(cache)
	allowance := makeUnknownAllowance("session", resetAt)
	allowance.Source = claudePrimeSessionSource
	return allowance
}

func runClaudePrimeRequest(opts claudePrimeOptions) ([]byte, string, error) {
	prompt := strings.TrimSpace(opts.Prompt)
	if prompt == "" {
		prompt = "Reply with exactly OK."
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := claudePrimeArgs(opts, prompt)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if dir := os.TempDir(); dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if ctx.Err() != nil {
		return stdout, stderr.String(), ctx.Err()
	}
	return stdout, stderr.String(), err
}

func claudePrimeArgs(opts claudePrimeOptions, prompt string) []string {
	args := []string{
		firstNonEmpty(commandPath("claude"), "claude"),
		"-p",
		"--safe-mode",
		"--no-session-persistence",
		"--tools", "",
		"--permission-mode", "dontAsk",
		"--system-prompt", "Reply with exactly OK.",
		"--output-format", "json",
		"--prompt-suggestions", "false",
		"--max-turns", "1",
		"--max-budget-usd", "0.001",
	}
	model := firstNonEmpty(strings.TrimSpace(opts.Model), defaultClaudePrimeModel)
	args = append(args, "--model", model)
	args = append(args, prompt)
	return args
}

func mergeClaudePrimeOutput(result *claudePrimeResult, data []byte) {
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &payload); err != nil {
		return
	}
	result.ClaudeSession = firstNonEmpty(
		stringValue(payload["session_id"]),
		stringValue(payload["sessionId"]),
	)
	if cost, ok := firstFloat(payload, "total_cost_usd", "totalCostUsd", "cost_usd", "costUsd"); ok {
		result.TotalCostUSD = cost
	}
}

func waitForFreshClaudeStatusline(path string, oldMod time.Time, started time.Time, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if statuslineHasFreshLimits(path, oldMod, started) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func statuslineHasFreshLimits(path string, oldMod time.Time, started time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if oldMod.IsZero() {
		if info.ModTime().Before(started.Add(-1 * time.Second)) {
			return false
		}
	} else if !info.ModTime().After(oldMod) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, err = parseClaudeStatusline(data, time.Now())
	return err == nil
}

func writeClaudePrimeCache(cache claudePrimeCache) error {
	path := claudePrimeCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func claudePrimeSessionFallback(now time.Time) (Allowance, map[string]any, bool) {
	path := claudePrimeCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return Allowance{}, nil, false
	}
	var cache claudePrimeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return Allowance{}, nil, false
	}
	resetAt, err := time.Parse(time.RFC3339, cache.ResetAt)
	if err != nil || !resetAt.After(now) {
		return Allowance{}, nil, false
	}
	allowance := makeUnknownAllowance("session", resetAt.Local())
	allowance.Source = cache.Source
	meta := map[string]any{
		"primeCache":             path,
		"sessionFallbackSource":  cache.Source,
		"sessionFallbackResetAt": resetAt.Local().Format(time.RFC3339),
	}
	if cache.StartedAt != "" {
		meta["primeStartedAt"] = cache.StartedAt
	}
	if cache.UsageAt != "" {
		meta["primeUsageAt"] = cache.UsageAt
	}
	return allowance, meta, true
}

const claudeOAuthUsageURL = "https://api.anthropic.com/api/oauth/usage"
const claudeOAuthUsageSource = "claude usage api"
const claudeOAuthUsageFreshTTL = 2 * time.Minute
const claudeOAuthUsageStaleTTL = 30 * time.Minute
const claudeFallbackCodeVersion = "2.1.185"

type claudeOAuthUsageCache struct {
	FetchedAt     string          `json:"fetchedAt,omitempty"`
	Body          json.RawMessage `json:"body,omitempty"`
	NextAttemptAt string          `json:"nextAttemptAt,omitempty"`
	LastError     string          `json:"lastError,omitempty"`
	ClaudeVersion string          `json:"claudeVersion,omitempty"`
}

func claudeOAuthUsageCachePath() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "dankaiusage", "claude-oauth-usage.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dankaiusage", "claude-oauth-usage.json")
}

func loadClaudeOAuthUsageCache(path string) claudeOAuthUsageCache {
	var cache claudeOAuthUsageCache
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	_ = json.Unmarshal(data, &cache)
	return cache
}

func saveClaudeOAuthUsageCache(path string, cache claudeOAuthUsageCache) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
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

// The claude-code User-Agent matters: anonymous clients land in an
// aggressively rate-limited bucket and get persistent 429s.
func fetchClaudeOAuthUsage(token string, version string) ([]byte, time.Duration, error) {
	req, err := http.NewRequest(http.MethodGet, claudeOAuthUsageURL, nil)
	if err != nil {
		return nil, 5 * time.Minute, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-code/"+version)
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
		backoff := 15 * time.Minute
		if ra, raErr := strconv.Atoi(resp.Header.Get("Retry-After")); raErr == nil && ra > 0 {
			backoff = max(backoff, time.Duration(ra)*time.Second)
		}
		return nil, backoff, errors.New("Claude usage API rate limited (HTTP 429)")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, 5 * time.Minute, fmt.Errorf("Claude usage API auth failed (HTTP %d); run any Claude Code session to refresh sign-in", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, 5 * time.Minute, fmt.Errorf("Claude usage API returned HTTP %d", resp.StatusCode)
	}
	return body, 0, nil
}

func parseClaudeOAuthUsage(data []byte, now time.Time) (Allowance, Allowance, []ExtraLimit, []QuotaBucket, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, err
	}
	session := parseClaudeLimitWindow("session", claudeOAuthUsageSource, firstMap(root, "five_hour", "fiveHour"), now)
	weekly := parseClaudeLimitWindow("weekly", claudeOAuthUsageSource, firstMap(root, "seven_day", "sevenDay"), now)
	if session.Known && session.WindowMinutes == 0 {
		session.WindowMinutes = 300
	}
	if weekly.Known && weekly.WindowMinutes == 0 {
		weekly.WindowMinutes = 10080
	}
	extras := parseClaudeScopedLimits(root, now)
	extras = parseClaudeTopLevelScopedLimits(root, now, extras)
	additional := parseClaudeSpendBuckets(root)
	if !session.Known && !weekly.Known {
		return session, weekly, extras, additional, errors.New("Claude usage API returned no session or weekly windows")
	}
	return session, weekly, extras, additional, nil
}

// parseClaudeScopedLimits extracts model- or surface-scoped windows from the
// structured limits array (e.g. the Fable-only weekly cap). The plain
// session and weekly_all entries duplicate five_hour/seven_day and are
// skipped.
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
		if kindLower == "" || kindLower == "session" || kindLower == "weekly" || kindLower == "weekly_all" || kindLower == "five_hour" || kindLower == "seven_day" {
			continue
		}
		used, ok := firstFloat(entry, "percent", "utilization", "used_percentage", "usedPercent", "used_percent", "percentage", "percent_used", "percentUsed")
		if !ok {
			continue
		}
		window, windowMinutes := claudeScopedLimitWindow(kind, stringValue(entry["group"]))
		label := firstNonEmpty(stringValue(entry["label"]), stringValue(entry["display_name"]), stringValue(entry["displayName"]), stringValue(entry["name"]), humanizeScopedLimitLabel(kind))
		id := firstNonEmpty(stringValue(entry["id"]), kind)
		if scope := firstMap(entry, "scope"); scope != nil {
			if model := firstMap(scope, "model"); model != nil {
				label = firstNonEmpty(stringValue(model["display_name"]), stringValue(model["id"]), label)
				id = firstNonEmpty(stringValue(model["id"]), id)
			}
		}
		allowance := makeSubscriptionAllowance(window, claudeOAuthUsageSource, used, parseReset(entry, time.Time{}), windowMinutes)
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
		window, suffix, ok := claudeTopLevelScopedLimitKey(key)
		if !ok {
			continue
		}
		allowance := parseClaudeLimitWindow(window, claudeOAuthUsageSource, values, now)
		if !allowance.Known {
			continue
		}
		if allowance.WindowMinutes == 0 {
			if window == "session" {
				allowance.WindowMinutes = 300
			} else {
				allowance.WindowMinutes = 10080
			}
		}
		out = upsertExtraLimit(out, key, humanizeScopedLimitLabel(suffix), allowance)
	}
	return out
}

func claudeTopLevelScopedLimitKey(key string) (string, string, bool) {
	switch {
	case strings.HasPrefix(key, "seven_day_"):
		return "weekly", strings.TrimPrefix(key, "seven_day_"), true
	case strings.HasPrefix(key, "sevenDay"):
		suffix := strings.TrimPrefix(key, "sevenDay")
		if suffix == "" {
			return "", "", false
		}
		return "weekly", suffix, true
	case strings.HasPrefix(key, "five_hour_"):
		return "session", strings.TrimPrefix(key, "five_hour_"), true
	case strings.HasPrefix(key, "fiveHour"):
		suffix := strings.TrimPrefix(key, "fiveHour")
		if suffix == "" {
			return "", "", false
		}
		return "session", suffix, true
	default:
		return "", "", false
	}
}

func claudeScopedLimitWindow(kind string, group string) (string, int64) {
	text := strings.ToLower(kind + " " + group)
	if strings.Contains(text, "session") || strings.Contains(text, "five_hour") || strings.Contains(text, "fivehour") {
		return "session", 300
	}
	return "weekly", 10080
}

func upsertExtraLimit(limits []ExtraLimit, id string, label string, allowance Allowance) []ExtraLimit {
	id = firstNonEmpty(id, label)
	label = firstNonEmpty(label, id)
	for i := range limits {
		if limits[i].ID != id && (label == "" || limits[i].Label != label) {
			continue
		}
		if allowance.Window == "session" {
			limits[i].Session = &allowance
		} else {
			limits[i].Weekly = &allowance
		}
		return limits
	}
	extra := ExtraLimit{ID: id, Label: label}
	if allowance.Window == "session" {
		extra.Session = &allowance
	} else {
		extra.Weekly = &allowance
	}
	return append(limits, extra)
}

func humanizeScopedLimitLabel(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		lower := strings.ToLower(part)
		switch lower {
		case "", "weekly", "session", "seven", "day", "five", "hour", "model", "only", "all":
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
	used, usedOK := firstFloat(usedMap, "amount_minor", "amountMinor")
	limit, limitOK := firstFloat(limitMap, "amount_minor", "amountMinor")
	if !usedOK || !limitOK || limit <= 0 {
		return QuotaBucket{}, false
	}
	currency := firstNonEmpty(stringValue(usedMap["currency"]), stringValue(limitMap["currency"]), "USD")
	exponent := float64(2)
	if value, ok := firstFloat(usedMap, "exponent"); ok {
		exponent = value
	} else if value, ok := firstFloat(limitMap, "exponent"); ok {
		exponent = value
	}
	return makeClaudeSpendBucket(int64(used), int64(limit), currency, int(exponent)), true
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

func claudeOAuthAllowancesFromCache(path string, cache claudeOAuthUsageCache, now time.Time, stale bool) (Allowance, Allowance, []ExtraLimit, []QuotaBucket, map[string]any, error) {
	session, weekly, extras, additional, err := parseClaudeOAuthUsage(cache.Body, now)
	if err != nil {
		return session, weekly, extras, additional, nil, err
	}
	meta := claudeOAuthUsageMeta(path, cache)
	if stale {
		meta["usageDataStale"] = true
	}
	return session, weekly, extras, additional, meta, nil
}

func collectClaudeOAuthLimits(now time.Time) (Allowance, Allowance, []ExtraLimit, []QuotaBucket, map[string]any, error) {
	return collectClaudeOAuthLimitsTTL(now, claudeOAuthUsageFreshTTL)
}

func collectClaudeOAuthLimitsTTL(now time.Time, freshTTL time.Duration) (Allowance, Allowance, []ExtraLimit, []QuotaBucket, map[string]any, error) {
	path := claudeOAuthUsageCachePath()
	cache := loadClaudeOAuthUsageCache(path)
	fetchedAt, _ := time.Parse(time.RFC3339, cache.FetchedAt)
	cacheUsable := len(cache.Body) > 0 && !fetchedAt.IsZero()
	if cacheUsable && now.Sub(fetchedAt) < freshTTL {
		return claudeOAuthAllowancesFromCache(path, cache, now, false)
	}
	staleOK := cacheUsable && now.Sub(fetchedAt) < claudeOAuthUsageStaleTTL
	if next, err := time.Parse(time.RFC3339, cache.NextAttemptAt); err == nil && now.Before(next) {
		if staleOK {
			return claudeOAuthAllowancesFromCache(path, cache, now, true)
		}
		reason := cache.LastError
		if reason == "" {
			reason = "waiting before retrying"
		}
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, fmt.Errorf("Claude usage API backing off: %s", reason)
	}

	token, subscription, err := readClaudeOAuthToken(now)
	if err != nil {
		cache.LastError = err.Error()
		cache.NextAttemptAt = now.Add(2 * time.Minute).Format(time.RFC3339)
		saveClaudeOAuthUsageCache(path, cache)
		if staleOK {
			return claudeOAuthAllowancesFromCache(path, cache, now, true)
		}
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
	}
	cache.ClaudeVersion = claudeCodeVersionString(cache.ClaudeVersion)
	body, backoff, err := fetchClaudeOAuthUsage(token, cache.ClaudeVersion)
	if err != nil {
		cache.LastError = err.Error()
		cache.NextAttemptAt = now.Add(backoff).Format(time.RFC3339)
		saveClaudeOAuthUsageCache(path, cache)
		if staleOK {
			return claudeOAuthAllowancesFromCache(path, cache, now, true)
		}
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, nil, nil, err
	}
	session, weekly, extras, additional, err := parseClaudeOAuthUsage(body, now)
	if err != nil {
		cache.LastError = err.Error()
		cache.NextAttemptAt = now.Add(5 * time.Minute).Format(time.RFC3339)
		saveClaudeOAuthUsageCache(path, cache)
		if staleOK {
			return claudeOAuthAllowancesFromCache(path, cache, now, true)
		}
		return session, weekly, extras, additional, nil, err
	}
	cache.Body = body
	cache.FetchedAt = now.Format(time.RFC3339)
	cache.LastError = ""
	cache.NextAttemptAt = ""
	saveClaudeOAuthUsageCache(path, cache)
	meta := claudeOAuthUsageMeta(path, cache)
	if subscription != "" {
		meta["subscriptionType"] = subscription
	}
	return session, weekly, extras, additional, meta, nil
}

func collectClaudeSubscriptionLimits(now time.Time) (Allowance, Allowance, []ExtraLimit, []QuotaBucket, map[string]any, error) {
	session, weekly, extras, additional, meta, err := collectClaudeOAuthLimits(now)
	if err == nil {
		return session, weekly, extras, additional, meta, nil
	}
	oauthErr := err
	session, weekly, meta, err = collectClaudeStatuslineSubscriptionLimits(now)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["oauthUsageError"] = oauthErr.Error()
	return session, weekly, nil, nil, meta, err
}

func collectClaudeStatuslineSubscriptionLimits(now time.Time) (Allowance, Allowance, map[string]any, error) {
	path := claudeStatuslineCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		meta := claudeStatuslineMeta(path)
		session := makeUnknownAllowance("session", now)
		weekly := makeUnknownAllowance("weekly", now)
		if fallback, fallbackMeta, ok := claudePrimeSessionFallback(now); ok {
			session = fallback
			for key, value := range fallbackMeta {
				meta[key] = value
			}
		}
		if configured, command := claudeStatuslineSettings(); configured {
			meta["statuslineConfigured"] = true
			meta["statuslineCommand"] = command
			meta["statuslineNextStep"] = "Open Claude Code once to refresh account limits"
			return session, weekly, meta, fmt.Errorf("Claude statusline cache not found; statusline is configured but has not run yet")
		}
		meta["statuslineNextStep"] = "Configure statusLine.command to dankaiusage claude-statusline"
		return session, weekly, meta, fmt.Errorf("Claude statusline cache not found; set statusLine.command to dankaiusage claude-statusline")
	}
	limits, err := parseClaudeStatusline(data, now)
	if err != nil {
		meta := claudeStatuslineMeta(path)
		meta["statuslineNextStep"] = "Open Claude Code once to refresh account limits"
		session := makeUnknownAllowance("session", now)
		weekly := makeUnknownAllowance("weekly", now)
		if fallback, fallbackMeta, ok := claudePrimeSessionFallback(now); ok {
			session = fallback
			for key, value := range fallbackMeta {
				meta[key] = value
			}
		}
		return session, weekly, meta, err
	}
	meta := claudeStatuslineMeta(path)
	meta["source"] = "claude statusline"
	if limits.Model != "" {
		meta["model"] = limits.Model
	}
	if limits.Version != "" {
		meta["version"] = limits.Version
	}
	return limits.Session, limits.Weekly, meta, nil
}

func claudeStatuslineMeta(path string) map[string]any {
	meta := map[string]any{
		"source":          "claude statusline",
		"statuslineCache": path,
	}
	if configured, command := claudeStatuslineSettings(); configured {
		meta["statuslineConfigured"] = true
		meta["statuslineCommand"] = command
	}
	return meta
}

func parseClaudeStatusline(data []byte, now time.Time) (claudeStatuslineLimits, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return claudeStatuslineLimits{}, err
	}
	limitsMap := firstMap(root, "rate_limits", "rateLimits", "rateLimitsInfo")
	if limitsMap == nil {
		return claudeStatuslineLimits{}, errors.New("Claude statusline has no rate limit data yet")
	}
	sessionMap := firstMap(limitsMap, "five_hour", "fiveHour", "session", "primary")
	weeklyMap := firstMap(limitsMap, "seven_day", "sevenDay", "weekly", "secondary")
	out := claudeStatuslineLimits{
		Session: parseClaudeLimitWindow("session", "claude statusline", sessionMap, time.Time{}),
		Weekly:  parseClaudeLimitWindow("weekly", "claude statusline", weeklyMap, time.Time{}),
		Version: stringValue(root["version"]),
	}
	if model := firstMap(root, "model"); model != nil {
		out.Model = firstNonEmpty(stringValue(model["display_name"]), stringValue(model["displayName"]), stringValue(model["id"]))
	}
	if !out.Session.Known && !out.Weekly.Known {
		return out, errors.New("Claude statusline has no session or weekly usage percentages")
	}
	return out, nil
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

func claudeStatuslineCachePath() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "dankaiusage", "claude-statusline.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dankaiusage", "claude-statusline.json")
}

func claudePrimeCachePath() string {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "dankaiusage", "claude-prime.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dankaiusage", "claude-prime.json")
}

func claudeStatuslineSettings() (bool, string) {
	data, err := os.ReadFile(filepath.Join(claudeHome(), "settings.json"))
	if err != nil {
		return false, ""
	}
	var settings struct {
		StatusLine struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, ""
	}
	command := strings.TrimSpace(settings.StatusLine.Command)
	return strings.Contains(command, "dankaiusage claude-statusline"), command
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
