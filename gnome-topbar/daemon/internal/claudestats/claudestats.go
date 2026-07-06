// Package claudestats reads the sandbox's claude-stats.json (schema 1.x) from
// $ANTI_TANGENT_STATS_DIR if present: per-account Claude Code usage plus real
// 5h/weekly rate-limit utilization from Anthropic's /api/oauth/usage endpoint.
// Pure local file read. The contract lives in the claude-sandbox repo
// (docs/claude-stats/); this is a tolerant reader — unknown fields are ignored
// and every window is nullable.
//
// Read distinguishes "absent" (feature off → no error) from "present but
// broken" (corrupt JSON / unsupported major / oversized → error) so the daemon
// can surface a diagnosable signal instead of silently showing nothing.
package claudestats

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	fileName = "claude-stats.json"
	// supportedMajor is the claude-stats schema MAJOR this consumer understands.
	// A higher major may reshape/remove fields, so it is rejected rather than
	// mis-decoded into the v1 structs and rendered as if valid (contract: branch
	// breaking changes off the MAJOR).
	supportedMajor = 1
	// maxBytes bounds the producer file we'll load. It is written by another
	// tool; a runaway/garbage file must not balloon daemon memory.
	maxBytes = 4 << 20 // 4 MiB
	// maxErrLen caps producer-supplied error strings on ingest. They are
	// rendered into the tray and the /state debug JSON; the producer is supposed
	// to summarize, not echo raw upstream response bodies — bound it defensively.
	maxErrLen = 200
)

// Stats is the decoded snapshot. Present is false when the file is absent (then
// Read's error is nil) or could not be loaded (then Read's error is non-nil).
type Stats struct {
	Present       bool               `json:"present"`
	SchemaVersion string             `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	Accounts      map[string]Account `json:"accounts"`
	// CCUsageVersion / Totals are decoded for contract-completeness but not yet
	// rendered by the tray.
	CCUsageVersion string `json:"ccusage_version,omitempty"`
	Totals         Totals `json:"totals"`
}

// Account is one sandbox account. config_dir is intentionally NOT modeled: it
// holds absolute home paths and is unused by the consumer, so leaving it out
// keeps those paths off the /state debug endpoint.
type Account struct {
	DisplayName string       `json:"display_name,omitempty"`
	Today       *Usage       `json:"today"`
	Week        *Usage       `json:"week"`
	Month       *Usage       `json:"month"`
	ActiveBlock *ActiveBlock `json:"active_block"`
	Limits      *Limits      `json:"limits"`
	Error       *string      `json:"error"`
}

type Usage struct {
	TotalTokens int64   `json:"total_tokens"`
	CostUSD     float64 `json:"cost_usd"`
	// Per-channel token breakdown is decoded for contract-completeness but not
	// rendered.
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_tokens,omitempty"`
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
	FetchedAt time.Time `json:"fetched_at"`
	Error     *string   `json:"error"`
	FiveHour  *Window   `json:"five_hour"`
	SevenDay  *Window   `json:"seven_day"`
	// Per-model weekly sub-limits and overage credits are decoded for
	// contract-completeness but not yet rendered by the tray.
	SevenDayOpus   *Window     `json:"seven_day_opus"`
	SevenDaySonnet *Window     `json:"seven_day_sonnet"`
	// WeeklyModels holds per-model weekly sub-limits keyed by model display_name
	// (schema 1.2+, from the producer's /api/oauth/usage limits[] weekly_scoped
	// entries). Nil/empty when the producer emits none.
	WeeklyModels map[string]*Window `json:"weekly_models"`
	ExtraUsage   *ExtraUsage        `json:"extra_usage"`
}

// Window is one rate-limit window. Utilization is a percent 0-100 (nil when the
// producer can't determine it); ResetsAt is when the window resets. HasData
// reports whether the window carries anything worth rendering.
type Window struct {
	Utilization *float64   `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

// HasData reports whether the window carries any renderable field. A
// schema-valid all-null window ({utilization:null, resets_at:null}) has none.
func (w *Window) HasData() bool {
	return w != nil && (w.Utilization != nil || w.ResetsAt != nil)
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
// staleAfter. An absent snapshot, or one with a zero/omitted generated_at, is
// never "stale" (a zero timestamp must not make a fresh file look ancient).
func (s Stats) Stale(now time.Time) bool {
	return s.Present && !s.GeneratedAt.IsZero() && now.Sub(s.GeneratedAt) > staleAfter
}

// Read loads claude-stats.json from dir. An absent file (or empty dir) returns
// (Stats{}, nil) — the feature is simply off. A present file that can't be used
// (read error, oversized, malformed JSON, or an unsupported schema major)
// returns (Stats{}, err) so the caller can log it and surface a status row.
func Read(dir string) (Stats, error) {
	if dir == "" {
		return Stats{}, nil
	}
	f, err := os.Open(filepath.Join(dir, fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Stats{}, nil // absent: feature off, not an error
		}
		return Stats{}, err
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return Stats{}, err
	}
	if len(b) > maxBytes {
		return Stats{}, fmt.Errorf("%s exceeds %d bytes", fileName, maxBytes)
	}
	var s Stats
	if err := json.Unmarshal(b, &s); err != nil {
		return Stats{}, fmt.Errorf("parse %s: %w", fileName, err)
	}
	if !majorSupported(s.SchemaVersion) {
		return Stats{}, fmt.Errorf("unsupported claude-stats schema_version %q (consumer supports major %d)", s.SchemaVersion, supportedMajor)
	}
	capErrorStrings(&s)
	s.Present = true
	return s, nil
}

// majorSupported reports whether the schema_version's MAJOR is one this
// consumer understands. An empty or unparseable version is treated leniently
// (proceed) rather than rejected.
func majorSupported(v string) bool {
	if v == "" {
		return true
	}
	maj := v
	if i := strings.IndexByte(v, '.'); i >= 0 {
		maj = v[:i]
	}
	n, err := strconv.Atoi(maj)
	if err != nil {
		return true
	}
	return n == supportedMajor
}

// capErrorStrings bounds the producer-supplied error strings in place.
func capErrorStrings(s *Stats) {
	for k, a := range s.Accounts {
		a.Error = capStr(a.Error)
		if a.Limits != nil {
			a.Limits.Error = capStr(a.Limits.Error)
		}
		s.Accounts[k] = a
	}
}

func capStr(p *string) *string {
	if p == nil {
		return nil
	}
	if r := []rune(*p); len(r) > maxErrLen {
		t := string(r[:maxErrLen]) + "…"
		return &t
	}
	return p
}
