package main

import (
	"bufio"
	"bytes"
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
	Used             int64   `json:"used"`
	Limit            int64   `json:"limit"`
	Remaining        int64   `json:"remaining"`
	PercentUsed      float64 `json:"percentUsed"`
	PercentRemaining float64 `json:"percentRemaining"`
	ResetAt          string  `json:"resetAt,omitempty"`
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
	PeriodDays         int
	SessionHours       int
	CodexSessionLimit  int64
	CodexWeeklyLimit   int64
	ClaudeSessionLimit int64
	ClaudeWeeklyLimit  int64
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}

	fs := flag.NewFlagSet("summary", flag.ExitOnError)
	periodDays := fs.Int("period-days", 7, "rolling period length in days")
	sessionHours := fs.Int("session-hours", 5, "rolling session window length in hours")
	codexSessionLimit := fs.Int64("codex-session-limit", 0, "Codex session allowance in token units; 0 means unknown")
	codexWeeklyLimit := fs.Int64("codex-weekly-limit", 0, "Codex weekly allowance in token units; 0 means unknown")
	claudeSessionLimit := fs.Int64("claude-session-limit", 0, "Claude session allowance in token units; 0 means unknown")
	claudeWeeklyLimit := fs.Int64("claude-weekly-limit", 0, "Claude weekly allowance in token units; 0 means unknown")
	pretty := fs.Bool("pretty", false, "pretty-print JSON")
	_ = fs.Parse(dropSummaryArg(os.Args[1:]))

	if *periodDays < 1 {
		*periodDays = 1
	}
	if *sessionHours < 1 {
		*sessionHours = 1
	}

	summary := collect(options{
		PeriodDays:         *periodDays,
		SessionHours:       *sessionHours,
		CodexSessionLimit:  *codexSessionLimit,
		CodexWeeklyLimit:   *codexWeeklyLimit,
		ClaudeSessionLimit: *claudeSessionLimit,
		ClaudeWeeklyLimit:  *claudeWeeklyLimit,
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

	if _, err := exec.LookPath("sqlite3"); err != nil {
		provider.Error = "sqlite3 not found"
		return provider
	}

	db := filepath.Join(root, "logs_2.sqlite")
	if _, err := os.Stat(db); err != nil {
		provider.Error = "Codex logs not found"
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
		provider.Error = msg
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
		provider.Error = err.Error()
	}

	applyEvents(&provider, events, now, opts, opts.CodexSessionLimit, opts.CodexWeeklyLimit)
	return provider
}

func collectClaude(now time.Time, opts options) ProviderUsage {
	provider := ProviderUsage{
		ID:        "claude",
		Name:      "Claude",
		Available: hasCommand("claude"),
		CLIPath:   commandPath("claude"),
	}
	root := claudeHome()
	provider.DataPath = root

	projects := filepath.Join(root, "projects")
	if _, err := os.Stat(projects); err != nil {
		provider.Error = "Claude projects not found"
		return provider
	}

	cutoff := now.AddDate(0, 0, -maxInt(opts.PeriodDays, 31)-1)
	var events []tokenEvent
	err := filepath.WalkDir(projects, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && info.ModTime().Before(cutoff) {
			return nil
		}
		fileEvents, parseErr := readClaudeJSONL(path)
		if parseErr != nil && provider.Error == "" {
			provider.Error = parseErr.Error()
		}
		events = append(events, fileEvents...)
		return nil
	})
	if err != nil {
		provider.Error = err.Error()
	}

	applyEvents(&provider, events, now, opts, opts.ClaudeSessionLimit, opts.ClaudeWeeklyLimit)
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

func applyEvents(provider *ProviderUsage, events []tokenEvent, now time.Time, opts options, sessionLimit int64, weeklyLimit int64) {
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
	provider.SessionLeft = makeAllowance("session", provider.Session.Total, sessionLimit, sessionReset)
	provider.WeeklyLeft = makeAllowance("weekly", provider.Week.Total, weeklyLimit, weekStart.AddDate(0, 0, 7))
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
