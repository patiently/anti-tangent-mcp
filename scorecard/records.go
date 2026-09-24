// Package scorecard scores anti-tangent's verdicts against an independent
// review of the same work. It is public and stdlib-only because two Go
// modules compute with it: the MCP server over its local stats files, and the
// gnome-topbar daemon over records pooled from Basic Memory. Both must derive
// identical numbers from identical records, so neither may fork this logic.
package scorecard

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SourceFinalReview = "final_review"
	SourceReviewNow   = "review_now"
)

// Sources lists every outcome source in display order.
var Sources = []string{SourceFinalReview, SourceReviewNow}

func ValidSource(s string) bool { return s == SourceFinalReview || s == SourceReviewNow }

func ValidSeverity(s string) bool { return s == "critical" || s == "major" || s == "minor" }

// ToolCall is one anti-tangent call made on behalf of a task (or, for
// validate_plan, a run).
type ToolCall struct {
	Tool     string `json:"tool"`
	Model    string `json:"model"`
	Verdict  string `json:"verdict,omitempty"`
	Findings int    `json:"findings"`
	MS       int64  `json:"ms"`
	Partial  bool   `json:"partial,omitempty"`
}

// TaskSnapshot is the content-free state of one plan task after a change.
type TaskSnapshot struct {
	Index          int            `json:"index"`
	PreVerdict     string         `json:"pre_verdict,omitempty"`
	PostVerdict    string         `json:"post_verdict,omitempty"`
	Checkpoints    int            `json:"checkpoints"`
	Attempts       int            `json:"attempts,omitempty"`
	Severity       map[string]int `json:"severity,omitempty"`
	Waived         int            `json:"waived,omitempty"`
	Escalated      bool           `json:"escalated,omitempty"`
	Lite           bool           `json:"lite,omitempty"`
	Unmatched      bool           `json:"unmatched,omitempty"`
	CodesceneState string         `json:"codescene_state,omitempty"`
	Calls          []ToolCall     `json:"calls,omitempty"`
	CallsDropped   int            `json:"calls_dropped,omitempty"`
}

// RunLine is one line of runs.jsonl: a run header (Header true, Task nil) or
// one task's snapshot. Publisher is empty on the server and filled in by the
// daemon when records are pooled.
type RunLine struct {
	Ts               time.Time         `json:"ts"`
	RunHash          string            `json:"run_hash"`
	Publisher        string            `json:"publisher,omitempty"`
	ServerVersion    string            `json:"server_version,omitempty"`
	Header           bool              `json:"header,omitempty"`
	PlanVerdict      string            `json:"plan_verdict,omitempty"`
	PlanQuality      string            `json:"plan_quality,omitempty"`
	TaskCount        int               `json:"task_count,omitempty"`
	ConfiguredModels map[string]string `json:"configured_models,omitempty"`
	PlanCall         *ToolCall         `json:"plan_call,omitempty"`
	Task             *TaskSnapshot     `json:"task,omitempty"`
}

type OutcomeFinding struct {
	TaskIndex int    `json:"task_index"`
	Severity  string `json:"severity"`
	Category  string `json:"category"`
}

type ImplementerModel struct {
	TaskIndex int    `json:"task_index"`
	Model     string `json:"model"`
}

// OutcomeLine is one line of outcomes.jsonl: what one independent review
// found, per task. An empty Findings slice means the review found nothing.
type OutcomeLine struct {
	Ts                time.Time          `json:"ts"`
	RunHash           string             `json:"run_hash"`
	Publisher         string             `json:"publisher,omitempty"`
	Source            string             `json:"source"`
	ReviewerModel     string             `json:"reviewer_model,omitempty"`
	ImplementerModels []ImplementerModel `json:"implementer_models,omitempty"`
	Findings          []OutcomeFinding   `json:"findings"`
}

// HashRunID is the salted digest that stands in for a plan_run_id in every
// record. It lives here, not in either module, because the server writes it
// and the daemon recomputes it to join local task titles: two copies of the
// construction would drift silently and the join would just find nothing.
func HashRunID(salt, planRunID string) string {
	sum := sha256.Sum256([]byte(salt + ":run:" + planRunID))
	return "r_" + hex.EncodeToString(sum[:12])
}

// NormalizeCategory is the only transformation free text gets before it is
// stored: lower-cased, trimmed, and cut to 40 runes so a caller that pastes
// a finding description instead of a category word cannot store it whole.
func NormalizeCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if utf8.RuneCountInString(s) <= 40 {
		return s
	}
	return string([]rune(s)[:40])
}

// MaxModelRunes bounds ReviewerModel and ImplementerModel.Model: both are
// free-form "provider:model" strings a caller supplies, and nothing else in
// the wire contract constrains their length.
const MaxModelRunes = 100

// ValidModelString reports whether s fits within MaxModelRunes runes and
// contains no control character. The server's record_review_outcome
// validation and the daemon's defensive clamp on the same fields both key
// off this bound so the wire contract has one definition, not two.
func ValidModelString(s string) bool {
	if utf8.RuneCountInString(s) > MaxModelRunes {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ClampModelString truncates s to MaxModelRunes runes and drops every
// control character, for a caller that must always produce a bounded value
// rather than reject an out-of-bound one.
func ClampModelString(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if n >= MaxModelRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
