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

func TestBarFill(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0, "🟩"}, {59, "🟩"}, {59.9, "🟩"},
		{60, "🟨"}, {79, "🟨"}, {79.9, "🟨"},
		{80, "🟥"}, {100, "🟥"}, {150, "🟥"},
	}
	for _, c := range cases {
		if got := barFill(c.pct); got != c.want {
			t.Errorf("barFill(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestUsageBar(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0, "⬜⬜⬜⬜⬜"},
		{3, "🟩⬜⬜⬜⬜"},  // nonzero rounds to 0 cells → forced to 1
		{27, "🟩⬜⬜⬜⬜"}, // round(1.35) = 1
		{38, "🟩🟩⬜⬜⬜"}, // round(1.9) = 2
		{60, "🟨🟨🟨⬜⬜"}, // round(3.0) = 3, yellow
		{65, "🟨🟨🟨⬜⬜"}, // round(3.25) = 3
		{80, "🟥🟥🟥🟥⬜"}, // round(4.0) = 4, red
		{82, "🟥🟥🟥🟥⬜"}, // round(4.1) = 4
		{100, "🟥🟥🟥🟥🟥"},
		{150, "🟥🟥🟥🟥🟥"}, // clamped to width
	}
	for _, c := range cases {
		if got := usageBar(c.pct, barWidth); got != c.want {
			t.Errorf("usageBar(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestClaudeOverviewLabels_PrimaryAccount(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset5h := time.Date(2026, 6, 3, 11, 46, 0, 0, time.UTC)
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
	got := claudeOverviewLabels(cs, now)
	if len(got) != 1 {
		t.Fatalf("single account → one overview row, got %d: %q", len(got), got)
	}
	row := got[0]
	for _, want := range []string{"5h", "4%", "wk", "26%", "🟩"} {
		if !strings.Contains(row, want) {
			t.Errorf("overview row %q missing %q", row, want)
		}
	}
	if strings.Contains(row, "$84") || strings.Contains(row, "resets") || strings.Contains(row, "in 2h") {
		t.Errorf("overview must not carry cost/reset: %q", row)
	}
	if strings.Contains(row, "default") {
		t.Errorf("single account should not be prefixed with its name: %q", row)
	}
}

func TestClaudeOverviewLabels_HighUtilizationWarnsRed(t *testing.T) {
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
	got := claudeOverviewLabels(cs, now)
	if len(got) != 1 || !strings.Contains(got[0], "⚠") || !strings.Contains(got[0], "🟥") {
		t.Errorf("91%% utilization should warn (⚠) and fill red (🟥): %q", got)
	}
}

func TestClaudeOverviewLabels_MultiAccountPrefixed(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present: true, GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(27), ResetsAt: &reset},
				SevenDay: &claudestats.Window{Utilization: ptrF(38), ResetsAt: &reset},
			}},
			"alt": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(65), ResetsAt: &reset},
			}},
		},
	}
	got := claudeOverviewLabels(cs, now)
	if len(got) != 2 {
		t.Fatalf("two limit-accounts → two rows, got %d: %q", len(got), got)
	}
	// accountsWithWindows sorts keys: "alt" < "default".
	if !strings.Contains(got[0], "alt") || !strings.Contains(got[0], "🟨") || !strings.Contains(got[0], "65%") {
		t.Errorf("alt row should be first, yellow, 65%%: %q", got[0])
	}
	if !strings.Contains(got[1], "default") || !strings.Contains(got[1], "wk") {
		t.Errorf("default row should be second and carry the week window: %q", got[1])
	}
}

func TestClaudeOverviewLabels_AbsentOrNoLimits(t *testing.T) {
	now := time.Now()
	if got := claudeOverviewLabels(claudestats.Stats{Present: false}, now); got != nil {
		t.Errorf("absent stats → nil overview rows, got %q", got)
	}
	errMsg := "usage endpoint HTTP 401"
	cs := claudestats.Stats{Present: true, GeneratedAt: now, Accounts: map[string]claudestats.Account{
		"alt": {Limits: &claudestats.Limits{Error: &errMsg}},
	}}
	if got := claudeOverviewLabels(cs, now); len(got) != 0 {
		t.Errorf("limit-error-only account → no overview rows, got %q", got)
	}
}

func TestClaudeOverviewLabels_StaleMarker(t *testing.T) {
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
	got := claudeOverviewLabels(cs, now)
	if len(got) == 0 || !strings.Contains(got[0], "stale") {
		t.Errorf("stale snapshot should lead with a stale marker row: %q", got)
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
	if got := claudeOverviewLabels(cs, now); len(got) != 0 {
		t.Errorf("all-null window should produce no overview row, got %q", got)
	}
	rows := strings.Join(claudeUsageRows(cs, now), "\n")
	if strings.Contains(rows, "5h") {
		t.Errorf("all-null window should not render a bare 5h detail row:\n%s", rows)
	}
}

func TestMaxKeyLen(t *testing.T) {
	if got := maxKeyLen(nil); got != 0 {
		t.Errorf("maxKeyLen(nil) = %d, want 0", got)
	}
	if got := maxKeyLen([]string{"alt", "default", "x"}); got != 7 {
		t.Errorf("maxKeyLen = %d, want 7 (len \"default\")", got)
	}
}

func TestClaudeOverviewLabels_UnknownUtilization(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present: true, GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			// HasData via ResetsAt, but Utilization is unknown (nil).
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{ResetsAt: &reset},
			}},
		},
	}
	got := claudeOverviewLabels(cs, now)
	if len(got) != 1 || !strings.Contains(got[0], "5h —") {
		t.Errorf("unknown utilization should render '5h —' (no bar): %q", got)
	}
}

func TestClaudeOverviewLabels_StaleNoWindows(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	errMsg := "usage endpoint HTTP 401"
	cs := claudestats.Stats{
		Present:     true,
		GeneratedAt: now.Add(-15 * time.Minute), // stale
		Accounts: map[string]claudestats.Account{
			"alt": {Limits: &claudestats.Limits{Error: &errMsg}}, // no renderable windows
		},
	}
	got := claudeOverviewLabels(cs, now)
	if len(got) != 1 || !strings.Contains(got[0], "stale") {
		t.Errorf("stale + no windows should yield exactly the stale marker row: %q", got)
	}
}

func TestClaudeUsageRowsPerModel(t *testing.T) {
	util := 69.0
	reset := time.Date(2026, 6, 8, 20, 0, 0, 0, time.UTC)
	cs := claudestats.Stats{Present: true, Accounts: map[string]claudestats.Account{
		"default": {Limits: &claudestats.Limits{
			WeeklyModels: map[string]*claudestats.Window{"Fable": {Utilization: &util, ResetsAt: &reset}},
		}},
	}}
	rows := claudeUsageRows(cs, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	found := false
	for _, r := range rows {
		if strings.Contains(r, "Fable") && strings.Contains(r, "69%") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Fable per-model row in %#v", rows)
	}
}

func TestClaudeOverviewLabels_FableSegment(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	reset5h := now.Add(2 * time.Hour)
	reset7d := now.Add(5 * 24 * time.Hour)
	withFableKey := func(modelKey string) claudestats.Stats {
		return claudestats.Stats{
			Present: true, GeneratedAt: now,
			Accounts: map[string]claudestats.Account{
				"default": {Limits: &claudestats.Limits{
					FiveHour: &claudestats.Window{Utilization: ptrF(5), ResetsAt: &reset5h},
					SevenDay: &claudestats.Window{Utilization: ptrF(22), ResetsAt: &reset7d},
					WeeklyModels: map[string]*claudestats.Window{
						modelKey: {Utilization: ptrF(13), ResetsAt: &reset7d},
					},
				}},
			},
		}
	}
	// "Fable" is today's API display_name; "Fable 5" guards a future rename —
	// both must resolve to the f5 overview segment (case-insensitive prefix match).
	for _, key := range []string{"Fable", "Fable 5"} {
		got := claudeOverviewLabels(withFableKey(key), now)
		if len(got) != 1 {
			t.Fatalf("%s: want one overview row, got %d: %q", key, len(got), got)
		}
		row := got[0]
		for _, want := range []string{"5h", "wk", "f5", "13%"} {
			if !strings.Contains(row, want) {
				t.Errorf("%s: overview row %q missing %q", key, row, want)
			}
		}
	}
}

func TestClaudeOverviewLabels_NoFableNoSegment(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	cs := claudestats.Stats{
		Present: true, GeneratedAt: now,
		Accounts: map[string]claudestats.Account{
			"default": {Limits: &claudestats.Limits{
				FiveHour: &claudestats.Window{Utilization: ptrF(5), ResetsAt: &reset},
				SevenDay: &claudestats.Window{Utilization: ptrF(22), ResetsAt: &reset},
			}},
		},
	}
	if got := claudeOverviewLabels(cs, now); len(got) != 1 || strings.Contains(got[0], "f5") {
		t.Errorf("no Fable window → no f5 segment: %q", got)
	}
}

func TestFableWindow_DeterministicOnDualKey(t *testing.T) {
	// A transient producer rename can leave both "Fable" and "Fable 5" in
	// WeeklyModels at once. Map iteration is randomized, so fableWindow must
	// still resolve to one stable window (most-specific name wins) rather than
	// flickering the f5 segment between refreshes.
	newer, older := 13.0, 99.0
	l := &claudestats.Limits{WeeklyModels: map[string]*claudestats.Window{
		"Fable":   {Utilization: &older},
		"Fable 5": {Utilization: &newer},
	}}
	for i := 0; i < 64; i++ {
		w := fableWindow(l)
		if w == nil || w.Utilization == nil || *w.Utilization != newer {
			t.Fatalf("iter %d: want the most-specific 'Fable 5' window (%.0f%%), got %+v", i, newer, w)
		}
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
