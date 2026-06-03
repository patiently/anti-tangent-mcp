// Package claudestats reads the sandbox's claude-stats.json (schema 1.x) from
// $ANTI_TANGENT_STATS_DIR if present: per-account Claude Code usage plus real
// 5h/weekly rate-limit utilization from Anthropic's /api/oauth/usage endpoint.
// Pure local file read; absence (or any read/parse failure) is reported as
// Present=false with no error. The contract lives in the claude-sandbox repo
// (docs/claude-stats/); this is a tolerant reader — unknown fields are ignored
// and every window is nullable.
package claudestats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const fileName = "claude-stats.json"

// Stats is the decoded snapshot. Present is false when the file is absent,
// unreadable, or unparseable.
type Stats struct {
	Present        bool               `json:"present"`
	SchemaVersion  string             `json:"schema_version"`
	GeneratedAt    time.Time          `json:"generated_at"`
	CCUsageVersion string             `json:"ccusage_version,omitempty"`
	Accounts       map[string]Account `json:"accounts"`
	Totals         Totals             `json:"totals"`
}

type Account struct {
	DisplayName string       `json:"display_name,omitempty"`
	ConfigDir   string       `json:"config_dir"`
	Today       *Usage       `json:"today"`
	Week        *Usage       `json:"week"`
	Month       *Usage       `json:"month"`
	ActiveBlock *ActiveBlock `json:"active_block"`
	Limits      *Limits      `json:"limits"`
	Error       *string      `json:"error"`
}

type Usage struct {
	TotalTokens         int64   `json:"total_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	InputTokens         int64   `json:"input_tokens,omitempty"`
	OutputTokens        int64   `json:"output_tokens,omitempty"`
	CacheCreationTokens int64   `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int64   `json:"cache_read_tokens,omitempty"`
}

type ActiveBlock struct {
	IsActive         bool      `json:"is_active"`
	StartedAt        time.Time `json:"started_at"`
	TotalTokens      int64     `json:"total_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	ProjectedCostUSD float64   `json:"projected_cost_usd"`
	RemainingMinutes int       `json:"remaining_minutes"`
}

// Limits is the /api/oauth/usage data. Error is non-nil when the fetch failed,
// in which case every window is nil. Per-window pointers distinguish "absent"
// from a real 0% utilization.
type Limits struct {
	FetchedAt      time.Time   `json:"fetched_at"`
	Error          *string     `json:"error"`
	FiveHour       *Window     `json:"five_hour"`
	SevenDay       *Window     `json:"seven_day"`
	SevenDayOpus   *Window     `json:"seven_day_opus"`
	SevenDaySonnet *Window     `json:"seven_day_sonnet"`
	ExtraUsage     *ExtraUsage `json:"extra_usage"`
}

// Window is one rate-limit window. Utilization is a percent 0-100 (nil when the
// producer can't determine it); ResetsAt is when the window resets.
type Window struct {
	Utilization *float64   `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

type ExtraUsage struct {
	IsEnabled    bool     `json:"is_enabled"`
	MonthlyLimit float64  `json:"monthly_limit"`
	UsedCredits  float64  `json:"used_credits"`
	Utilization  *float64 `json:"utilization"`
	Currency     string   `json:"currency"`
}

type Totals struct {
	TodayCostUSD     float64 `json:"today_cost_usd"`
	WeekCostUSD      float64 `json:"week_cost_usd"`
	MonthCostUSD     float64 `json:"month_cost_usd"`
	TodayTotalTokens int64   `json:"today_total_tokens"`
	WeekTotalTokens  int64   `json:"week_total_tokens"`
	MonthTotalTokens int64   `json:"month_total_tokens"`
}

// staleAfter is how long after generated_at a present snapshot is considered
// stale. The producer rewrites every ~120s; 10 min is ~5 missed cycles.
const staleAfter = 10 * time.Minute

// Stale reports whether a present snapshot's generated_at is older than
// staleAfter relative to now. An absent snapshot is never "stale" (nothing is
// rendered for it).
func (s Stats) Stale(now time.Time) bool {
	return s.Present && now.Sub(s.GeneratedAt) > staleAfter
}

// Read returns Present=false (no error) when dir is empty or claude-stats.json
// is absent/unreadable/unparseable, mirroring atstats.Read's posture.
func Read(dir string) Stats {
	if dir == "" {
		return Stats{}
	}
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return Stats{}
	}
	var s Stats
	if err := json.Unmarshal(b, &s); err != nil {
		return Stats{}
	}
	s.Present = true
	return s
}
