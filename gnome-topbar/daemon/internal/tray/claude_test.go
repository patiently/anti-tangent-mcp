package tray

import (
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

func TestHumanUntil(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Minute, "now"},
		{0, "now"},
		{41 * time.Minute, "in 41m"},
		{2*time.Hour + 41*time.Minute, "in 2h41m"},
		{3 * time.Hour, "in 3h"},
		{5*24*time.Hour + 3*time.Hour, "in 5d"},
	}
	for _, c := range cases {
		if got := humanUntil(c.d); got != c.want {
			t.Errorf("humanUntil(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestClaudeClock(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	soon := time.Date(2026, 6, 3, 11, 40, 0, 0, time.UTC) // <24h → clock
	if got, want := claudeClock(soon, now), soon.Local().Format("15:04"); got != want {
		t.Errorf("claudeClock(soon) = %q, want %q", got, want)
	}
	later := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC) // >24h → date
	if got, want := claudeClock(later, now), later.Local().Format("Jan 2"); got != want {
		t.Errorf("claudeClock(later) = %q, want %q", got, want)
	}
}

func ptrF(f float64) *float64 { return &f }

func TestClaudeInlineLabels_PrimaryAccount(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset5h := time.Date(2026, 6, 3, 11, 46, 0, 0, time.UTC) // ~2h41m out
	reset7d := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC)
	cs := claudestats.Stats{
		Present:     true,
		GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {
				Week: &claudestats.Usage{CostUSD: 84.10, TotalTokens: 33106912},
				Limits: &claudestats.Limits{
					FiveHour: &claudestats.Window{Utilization: ptrF(4), ResetsAt: &reset5h},
					SevenDay: &claudestats.Window{Utilization: ptrF(26), ResetsAt: &reset7d},
				},
			},
		},
	}
	got := claudeInlineLabels(cs, now)
	if len(got) != 2 {
		t.Fatalf("expected 2 inline rows, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], "5h") || !strings.Contains(got[0], "4%") || !strings.Contains(got[0], "in 2h41m") {
		t.Errorf("5h row = %q, want 5h/4%%/in 2h41m", got[0])
	}
	if !strings.Contains(got[1], "7d") || !strings.Contains(got[1], "26%") || !strings.Contains(got[1], "$84") || !strings.Contains(got[1], "in 5d") {
		t.Errorf("7d row = %q, want 7d/26%%/$84/in 5d", got[1])
	}
	// Single limit-account → no account-name prefix.
	if strings.Contains(got[0], "default") {
		t.Errorf("single account should not be prefixed with its name: %q", got[0])
	}
}

func TestClaudeInlineLabels_HighUtilizationWarns(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present: true, GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(91), ResetsAt: &reset},
			}},
		},
	}
	got := claudeInlineLabels(cs, now)
	if len(got) != 1 || !strings.Contains(got[0], "⚠") {
		t.Errorf("91%% utilization should warn with ⚠: %q", got)
	}
}

func TestClaudeInlineLabels_AbsentOrNoLimits(t *testing.T) {
	now := time.Now()
	if got := claudeInlineLabels(claudestats.Stats{Present: false}, now); got != nil {
		t.Errorf("absent stats → nil inline rows, got %q", got)
	}
	// Present but the only account's limit fetch failed → no inline rows.
	errMsg := "usage endpoint HTTP 401"
	cs := claudestats.Stats{Present: true, GeneratedAt: now, Accounts: map[string]claudestats.Account{
		"alt": {Limits: &claudestats.Limits{Error: &errMsg}},
	}}
	if got := claudeInlineLabels(cs, now); len(got) != 0 {
		t.Errorf("limit-error-only account → no inline rows, got %q", got)
	}
}

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{512, "512"},
		{4821, "4.8k"},
		{512004, "512k"},
		{4821093, "4.8M"},
		{33106912, "33.1M"},
	}
	for _, c := range cases {
		if got := humanTokens(c.n); got != c.want {
			t.Errorf("humanTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestClaudeUsageRows_Detail(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	r5 := time.Date(2026, 6, 3, 11, 46, 0, 0, time.UTC)
	r7 := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC)
	errMsg := "usage endpoint HTTP 401"
	cs := claudestats.Stats{
		Present: true, GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {
				DisplayName: "default (~/.claude)",
				Today:       &claudestats.Usage{CostUSD: 12.47, TotalTokens: 4821093},
				Week:        &claudestats.Usage{CostUSD: 84.10, TotalTokens: 33106912},
				ActiveBlock: &claudestats.ActiveBlock{IsActive: true, CostUSD: 5.12, ProjectedCostUSD: 8.40, RemainingMinutes: 78},
				Limits: &claudestats.Limits{
					FiveHour: &claudestats.Window{Utilization: ptrF(4), ResetsAt: &r5},
					SevenDay: &claudestats.Window{Utilization: ptrF(26), ResetsAt: &r7},
				},
			},
			"alt": {
				DisplayName: "alt (~/.claude-alt)",
				Week:        &claudestats.Usage{CostUSD: 1.92},
				Limits:      &claudestats.Limits{Error: &errMsg},
			},
		},
	}
	rows := strings.Join(claudeUsageRows(cs, now), "\n")
	for _, want := range []string{
		"default (~/.claude)",
		"5h", "4%", r5.Local().Format("15:04"), "in 2h41m",
		"7d", "26%",
		"$84.10", "33.1M",
		"block", "$5.12", "$8.40", "78m",
		"alt (~/.claude-alt)",
		"limits unavailable", "401",
	} {
		if !strings.Contains(rows, want) {
			t.Errorf("submenu rows missing %q\n---\n%s", want, rows)
		}
	}
}

func TestClaudeInlineLabels_StaleMarker(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present:     true,
		GeneratedAt: now.Add(-15 * time.Minute), // stale
		Accounts: map[string]claudestats.Account{
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(4), ResetsAt: &reset},
			}},
		},
	}
	got := claudeInlineLabels(cs, now)
	if len(got) == 0 || !strings.Contains(got[0], "stale") {
		t.Errorf("stale snapshot should lead with a stale marker row: %q", got)
	}
}

func TestActiveBlockGatedOnIsActive(t *testing.T) {
	now := time.Now()
	mk := func(active bool) string {
		cs := claudestats.Stats{Present: true, GeneratedAt: now, Accounts: map[string]claudestats.Account{
			"default": {ActiveBlock: &claudestats.ActiveBlock{IsActive: active, CostUSD: 5.12, RemainingMinutes: 78}},
		}}
		return strings.Join(claudeUsageRows(cs, now), "\n")
	}
	if strings.Contains(mk(false), "block") {
		t.Error("an inactive block (is_active=false) must not render as a live block row")
	}
	if !strings.Contains(mk(true), "block") {
		t.Error("an active block should render a block row")
	}
}

func TestEmptyWindowNotRendered(t *testing.T) {
	now := time.Now()
	cs := claudestats.Stats{Present: true, GeneratedAt: now, Accounts: map[string]claudestats.Account{
		"default": {Limits: &claudestats.Limits{FiveHour: &claudestats.Window{}}}, // all-null window
	}}
	if got := claudeInlineLabels(cs, now); len(got) != 0 {
		t.Errorf("all-null window should produce no inline row, got %q", got)
	}
	rows := strings.Join(claudeUsageRows(cs, now), "\n")
	if strings.Contains(rows, "5h") {
		t.Errorf("all-null window should not render a bare 5h detail row:\n%s", rows)
	}
}

func TestPastResetRendersNow(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	past := now.Add(-2 * time.Hour) // window already reset
	cs := claudestats.Stats{Present: true, GeneratedAt: now, Accounts: map[string]claudestats.Account{
		"default": {Limits: &claudestats.Limits{
			FiveHour: &claudestats.Window{Utilization: ptrF(4), ResetsAt: &past},
		}},
	}}
	rows := strings.Join(claudeUsageRows(cs, now), "\n")
	if !strings.Contains(rows, "resets now") {
		t.Errorf("past reset should render 'resets now':\n%s", rows)
	}
	if strings.Contains(rows, past.Local().Format("15:04")) {
		t.Errorf("past reset must not show a misleading past clock %q:\n%s", past.Local().Format("15:04"), rows)
	}
}
