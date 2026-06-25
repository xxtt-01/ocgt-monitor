package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ocgt-monitor/internal/formatter"
)

const openCodeGoBaseURL = "https://opencode.ai"

type OpenCodeGoQuerier struct {
	Cookie      string
	WorkspaceID string
}

func NewOpenCodeGoQuerier() *OpenCodeGoQuerier {
	return &OpenCodeGoQuerier{Cookie: os.Getenv("OPENCODE_GO_AUTH_COOKIE"), WorkspaceID: os.Getenv("OPENCODE_GO_WORKSPACE_ID")}
}

// pageGoUsage mirrors the JSON shape embedded in the /workspace/{id}/go page.
type pageGoUsage struct {
	Rolling *windowData `json:"rollingUsage"`
	Weekly  *windowData `json:"weeklyUsage"`
	Monthly *windowData `json:"monthlyUsage"`
}
type windowData struct {
	Status       *string  `json:"status"`
	UsagePercent *float64 `json:"usagePercent"`
	UsedPercent  *float64 `json:"usedPercent"`
	ResetInSec   *float64 `json:"resetInSec"`
}

func (q *OpenCodeGoQuerier) FetchQuota() (*QuotaData, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	// Fetch the Go usage page, which returns JSON with rolling/weekly/monthly windows.
	pageURL := fmt.Sprintf("%s/workspace/%s/go", openCodeGoBaseURL, q.WorkspaceID)
	req, _ := http.NewRequest("GET", pageURL, nil)
	req.Header.Set("accept", "*/*")
	req.Header.Set("cookie", q.Cookie)
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return parseGoUsage(string(body))
}

func (q *OpenCodeGoQuerier) validate() error {
	if q.Cookie == "" {
		return fmt.Errorf("OPENCODE_GO_AUTH_COOKIE not set")
	}
	if q.WorkspaceID == "" {
		return fmt.Errorf("OPENCODE_GO_WORKSPACE_ID not set")
	}
	return nil
}

// parseGoUsage tries JSON first, then falls back to regex.
func parseGoUsage(text string) (*QuotaData, error) {
	data, err := parseGoUsageJSON(text)
	if err == nil && data != nil {
		return data, nil
	}
	return parseGoUsageRegex(text)
}

// parseGoUsageJSON attempts to unmarshal the page body as JSON.
func parseGoUsageJSON(text string) (*QuotaData, error) {
	var page pageGoUsage
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		return nil, err
	}
	if page.Rolling == nil || page.Weekly == nil {
		return nil, fmt.Errorf("incomplete JSON: missing rolling or weekly data")
	}
	toPct := func(w *windowData) int {
		if w.UsagePercent != nil {
			return clampPct(int(*w.UsagePercent))
		}
		if w.UsedPercent != nil {
			return clampPct(int(*w.UsedPercent))
		}
		return 0
	}
	toSec := func(w *windowData) int {
		if w.ResetInSec != nil {
			return int(*w.ResetInSec)
		}
		return 0
	}
	toStatus := func(w *windowData) string {
		if w.Status != nil {
			return *w.Status
		}
		return ""
	}
	rolling := buildUsage(toStatus(page.Rolling), toSec(page.Rolling), toPct(page.Rolling))
	weekly := buildUsage(toStatus(page.Weekly), toSec(page.Weekly), toPct(page.Weekly))
	var monthly *QuotaUsage
	if page.Monthly != nil {
		m := buildUsage(toStatus(page.Monthly), toSec(page.Monthly), toPct(page.Monthly))
		if m.Status != "unlimited" {
			monthly = &m
		}
	}
	return &QuotaData{Rolling: rolling, Weekly: weekly, Monthly: monthly, FetchedAt: time.Now()}, nil
}

// parseGoUsageRegex extracts window data from text/javascript responses (fallback).
// Each window key + its fields is matched in a single regex for accuracy.
func parseGoUsageRegex(text string) (*QuotaData, error) {
	type windowMatch struct {
		pct     int
		resetMs int
	}
	extract := func(key string) (windowMatch, bool) {
		pctRe := regexp.MustCompile(key + `[^}]*?usagePercent\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)`)
		pm := pctRe.FindStringSubmatch(text)
		if pm == nil {
			return windowMatch{}, false
		}
		resetRe := regexp.MustCompile(key + `[^}]*?resetInSec\s*[:=]\s*([0-9]+)`)
		rm := resetRe.FindStringSubmatch(text)
		resetSec := 0
		if rm != nil {
			resetSec, _ = strconv.Atoi(rm[1])
		}
		return windowMatch{pct: clampPct(int(parseFloat(pm[1]))), resetMs: resetSec}, true
	}

	rolling, rok := extract("rollingUsage")
	weekly, wok := extract("weeklyUsage")
	if !rok || !wok {
		return nil, fmt.Errorf("failed to parse usagePercent from page response")
	}

	result := &QuotaData{
		Rolling:   buildUsage("", rolling.resetMs, rolling.pct),
		Weekly:    buildUsage("", weekly.resetMs, weekly.pct),
		FetchedAt: time.Now(),
	}
	if monthly, mok := extract("monthlyUsage"); mok {
		m := buildUsage("", monthly.resetMs, monthly.pct)
		if m.Status != "unlimited" {
			result.Monthly = &m
		}
	}
	return result, nil
}

func buildUsage(status string, resetSec, pct int) QuotaUsage {
	if status == "" {
		status = "active"
	}
	return QuotaUsage{
		Status:       status,
		UsagePercent: pct,
		ResetInSec:   resetSec,
		ResetDisplay: formatter.FormatDurationCompact(resetSec),
	}
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func clampPct(pct int) int {
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}
