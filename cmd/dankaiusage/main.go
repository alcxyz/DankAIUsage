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

type ModelUsage struct {
	Model    string `json:"model"`
	Input    int64  `json:"input"`
	Output   int64  `json:"output"`
	Cached   int64  `json:"cached"`
	Total    int64  `json:"total"`
	Requests int64  `json:"requests"`
}

type ProviderUsage struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Available   bool           `json:"available"`
	CLIPath     string         `json:"cliPath,omitempty"`
	DataPath    string         `json:"dataPath,omitempty"`
	Error       string         `json:"error,omitempty"`
	Today       PeriodTotals   `json:"today"`
	Session     PeriodTotals   `json:"session"`
	Week        PeriodTotals   `json:"week"`
	Month       PeriodTotals   `json:"month"`
	Period      PeriodTotals   `json:"period"`
	SessionLeft Allowance      `json:"sessionLeft"`
	WeeklyLeft  Allowance      `json:"weeklyLeft"`
	Models      []ModelUsage   `json:"models"`
	LastProject string         `json:"lastProject,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type Summary struct {
	Version      string          `json:"version"`
	GeneratedAt  string          `json:"generatedAt"`
	PeriodDays   int             `json:"periodDays"`
	Providers    []ProviderUsage `json:"providers"`
	GrandTotal   PeriodTotals    `json:"grandTotal"`
	Errors       []string        `json:"errors,omitempty"`
	Capabilities map[string]bool `json:"capabilities"`
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
	if sessionLeft, weeklyLeft, meta, err := collectCodexSubscriptionLimits(now); err == nil {
		provider.SessionLeft = sessionLeft
		provider.WeeklyLeft = weeklyLeft
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
	if sessionLeft, weeklyLeft, meta, err := collectClaudeSubscriptionLimits(now); err == nil {
		provider.SessionLeft = sessionLeft
		provider.WeeklyLeft = weeklyLeft
		mergeProviderMeta(&provider, meta)
	} else {
		mergeProviderMeta(&provider, meta)
		setProviderMeta(&provider, "limitError", err.Error())
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
		Known:   limit > 0,
		Window:  window,
		Unit:    "tokens",
		Used:    used,
		Limit:   limit,
		ResetAt: resetAt.Format(time.RFC3339),
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
	return Allowance{
		Window:  window,
		Unit:    "subscription",
		ResetAt: resetAt.Format(time.RFC3339),
	}
}

func makeSubscriptionAllowance(window string, source string, usedPercent float64, resetAt time.Time, windowMinutes int64) Allowance {
	remaining := clampPercent(100 - usedPercent)
	return Allowance{
		Known:            true,
		Window:           window,
		Unit:             "percent",
		Source:           source,
		Used:             int64(clampPercent(usedPercent) + 0.5),
		Limit:            100,
		Remaining:        int64(remaining + 0.5),
		PercentUsed:      clampPercent(usedPercent),
		PercentRemaining: remaining,
		ResetAt:          resetAt.Format(time.RFC3339),
		WindowMinutes:    windowMinutes,
	}
}

type codexRateLimitResponse struct {
	ID     int `json:"id"`
	Result struct {
		RateLimits          codexRateLimitSnapshot            `json:"rateLimits"`
		RateLimitsByLimitID map[string]codexRateLimitSnapshot `json:"rateLimitsByLimitId"`
	} `json:"result"`
	Error any `json:"error,omitempty"`
}

type codexRateLimitSnapshot struct {
	LimitID              string               `json:"limitId"`
	LimitName            string               `json:"limitName"`
	Primary              codexRateLimitWindow `json:"primary"`
	Secondary            codexRateLimitWindow `json:"secondary"`
	PlanType             string               `json:"planType"`
	RateLimitReachedType any                  `json:"rateLimitReachedType"`
}

type codexRateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

func collectCodexSubscriptionLimits(now time.Time) (Allowance, Allowance, map[string]any, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://", "--analytics-default-enabled")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, err
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
			return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, err
		}
		if response.Error != nil {
			_ = stdin.Close()
			cancel()
			_ = cmd.Wait()
			return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, fmt.Errorf("%v", response.Error)
		}
		snapshot := response.Result.RateLimits
		if byID := response.Result.RateLimitsByLimitID["codex"]; byID.LimitID != "" {
			snapshot = byID
		}
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		return codexSnapshotAllowances(snapshot, now), codexSnapshotWeeklyAllowance(snapshot, now), codexSnapshotMeta(snapshot), nil
	}
	if err := scanner.Err(); err != nil {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
		return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, err
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
	return makeUnknownAllowance("session", now), makeUnknownAllowance("weekly", now), nil, errors.New(msg)
}

func codexSnapshotAllowances(snapshot codexRateLimitSnapshot, now time.Time) Allowance {
	return makeSubscriptionAllowance("session", "codex app-server", snapshot.Primary.UsedPercent, codexResetTime(snapshot.Primary, now), codexWindowMinutes(snapshot.Primary))
}

func codexSnapshotWeeklyAllowance(snapshot codexRateLimitSnapshot, now time.Time) Allowance {
	return makeSubscriptionAllowance("weekly", "codex app-server", snapshot.Secondary.UsedPercent, codexResetTime(snapshot.Secondary, now), codexWindowMinutes(snapshot.Secondary))
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

func codexResetTime(window codexRateLimitWindow, fallback time.Time) time.Time {
	if window.ResetsAt == nil || *window.ResetsAt <= 0 {
		return fallback
	}
	return time.Unix(*window.ResetsAt, 0).Local()
}

func codexWindowMinutes(window codexRateLimitWindow) int64 {
	if window.WindowDurationMins == nil {
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

func primeClaudeStatusline(opts claudePrimeOptions) (claudePrimeResult, error) {
	path := claudeStatuslineCachePath()
	now := time.Now()
	result := claudePrimeResult{
		CachePath:   path,
		SessionLeft: makeUnknownAllowance("session", now),
		WeeklyLeft:  makeUnknownAllowance("weekly", now),
	}
	if _, err := exec.LookPath("claude"); err != nil {
		result.Message = "Claude CLI not found"
		return result, err
	}
	if configured, _ := claudeStatuslineSettings(); !configured {
		err := errors.New("Claude statusline is not configured")
		result.Message = "Configure statusLine.command to dankaiusage claude-statusline first"
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

func collectClaudeSubscriptionLimits(now time.Time) (Allowance, Allowance, map[string]any, error) {
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
		Session: parseClaudeLimitWindow("session", sessionMap, now),
		Weekly:  parseClaudeLimitWindow("weekly", weeklyMap, now),
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

func parseClaudeLimitWindow(window string, values map[string]any, now time.Time) Allowance {
	if values == nil {
		return makeUnknownAllowance(window, now)
	}
	used, ok := firstFloat(values, "used_percentage", "usedPercent", "used_percent", "percentage", "percent_used", "percentUsed")
	if !ok {
		return makeUnknownAllowance(window, parseReset(values, now))
	}
	windowMinutes, _ := firstFloat(values, "window_duration_mins", "windowDurationMins", "window_minutes", "windowMinutes")
	return makeSubscriptionAllowance(window, "claude statusline", used, parseReset(values, now), int64(windowMinutes))
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
