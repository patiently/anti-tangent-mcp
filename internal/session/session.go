// Package session defines the per-task session structures and an in-memory
// store with TTL eviction.
package session

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

type TaskSpec struct {
	Title              string   `json:"title"`
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	NonGoals           []string `json:"non_goals,omitempty"`
	Context            string   `json:"context,omitempty"`
	PinnedBy           []string `json:"pinned_by,omitempty"`
	Phase              string   `json:"phase,omitempty"`
}

type ModelDefaults struct {
	Pre, Mid, Post config.ModelRef
}

type Checkpoint struct {
	At        time.Time         `json:"at"`
	WorkingOn string            `json:"working_on"`
	FileCount int               `json:"file_count"`
	Verdict   verdict.Verdict   `json:"verdict"`
	Findings  []verdict.Finding `json:"findings,omitempty"`
}

type Session struct {
	ID            string
	CreatedAt     time.Time
	LastAccessed  time.Time
	Spec          TaskSpec
	PreFindings   []verdict.Finding
	Checkpoints   []Checkpoint
	PostFindings  []verdict.Finding
	ModelDefaults ModelDefaults
}
