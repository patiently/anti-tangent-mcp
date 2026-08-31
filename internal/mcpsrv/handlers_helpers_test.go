package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/providers"
)

// scriptedReviewer returns canned responses in order. Each Review() call
// pops the next response. Useful for testing the chunked path's exact
// call sequence. It also records every providers.Request it receives, in
// call order, so a test can assert on exactly what was wired onto the wire
// (e.g. CachePrefix) without needing a live provider — see
// TestReviewPlanChunked_CachePrefixWiring.
type scriptedReviewer struct {
	responses []providers.Response
	errors    []error // optional parallel slice; entry at index i applies to call i
	calls     int
	requests  []providers.Request
}

func (s *scriptedReviewer) Name() string { return "anthropic" }

func (s *scriptedReviewer) Review(_ context.Context, req providers.Request) (providers.Response, error) {
	s.requests = append(s.requests, req)
	if s.calls >= len(s.responses) {
		return providers.Response{}, fmt.Errorf("scriptedReviewer: unexpected call #%d", s.calls+1)
	}
	i := s.calls
	s.calls++
	if i < len(s.errors) && s.errors[i] != nil {
		// Faithfully mirror the real-provider contract: ErrResponseTruncated
		// carries the partial response body alongside the sentinel so handlers
		// can recover findings; any OTHER error returns a zero Response.
		if errors.Is(s.errors[i], providers.ErrResponseTruncated) {
			return s.responses[i], s.errors[i]
		}
		return providers.Response{}, s.errors[i]
	}
	return s.responses[i], nil
}

// newTestHandlers returns handlers wired with a default passing per-task
// reviewer response, for tests that only care about validate_task_spec /
// check_progress / validate_completion plumbing, not the review's content.
func newTestHandlers(t *testing.T) *handlers {
	t.Helper()
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	return &handlers{deps: newDeps(t, rv)}
}

// newTestPlanHandlers returns handlers wired with a default passing
// single-task plan reviewer response, for validate_plan tests that only care
// about plumbing, not the review's content.
func newTestPlanHandlers(t *testing.T) *handlers {
	t.Helper()
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	return &handlers{deps: newDeps(t, rv)}
}

// newDepsWithScripted builds a Deps wired to use the scripted reviewer for the
// plan model provider ("anthropic") with PlanTasksPerChunk set to chunkSize.
func newDepsWithScripted(t *testing.T, sr *scriptedReviewer, chunkSize int) Deps {
	t.Helper()
	d := newDeps(t, &fakeReviewer{name: "anthropic"})
	d.Cfg.PlanTasksPerChunk = chunkSize
	// Override the plan model to use the scripted reviewer's provider name.
	d.Cfg.PlanModel = d.Cfg.PreModel // already anthropic:claude-sonnet-4-6
	d.Reviews = providers.Registry{"anthropic": sr}
	return d
}

// buildPlanWithNTasks emits a markdown plan with N ### Task k: tasks, each
// with a Goal and one acceptance criterion.
func buildPlanWithNTasks(n int) string {
	var b strings.Builder
	b.WriteString("# Plan\n\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "### Task %d: t%d\n\n**Goal:** g%d\n\n**Acceptance criteria:**\n- ac%d\n\n", i, i, i, i)
	}
	return b.String()
}

// titlesRange returns expected task titles Task lo: tlo .. Task hi: thi inclusive.
func titlesRange(lo, hi int) []string {
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, fmt.Sprintf("Task %d: t%d", i, i))
	}
	return out
}

// passOneResp returns a scripted providers.Response containing a valid
// PlanFindingsOnly JSON (no tasks key — pass1 shape).
func passOneResp() providers.Response {
	return providers.Response{
		RawJSON:      []byte(`{"plan_verdict":"pass","plan_findings":[],"next_action":"Proceed with implementation."}`),
		Model:        "claude-sonnet-4-6",
		InputTokens:  10,
		OutputTokens: 5,
	}
}

// chunkResp returns a providers.Response containing a valid TasksOnly JSON for
// the given task titles. TaskIndex within the JSON is 1-based relative to the
// slice position (identity validation uses task_title, not task_index).
func chunkResp(t *testing.T, titles []string) providers.Response {
	t.Helper()
	type item struct {
		TaskIndex             int    `json:"task_index"`
		TaskTitle             string `json:"task_title"`
		Verdict               string `json:"verdict"`
		Findings              []any  `json:"findings"`
		SuggestedHeaderBlock  string `json:"suggested_header_block"`
		SuggestedHeaderReason string `json:"suggested_header_reason"`
	}
	items := make([]item, 0, len(titles))
	for i, ttl := range titles {
		items = append(items, item{
			TaskIndex:             i + 1,
			TaskTitle:             ttl,
			Verdict:               "pass",
			Findings:              []any{},
			SuggestedHeaderBlock:  "",
			SuggestedHeaderReason: "",
		})
	}
	raw, err := json.Marshal(struct {
		Tasks []item `json:"tasks"`
	}{items})
	if err != nil {
		t.Fatalf("chunkResp marshal: %v", err)
	}
	return providers.Response{
		RawJSON:      raw,
		Model:        "claude-sonnet-4-6",
		InputTokens:  20,
		OutputTokens: 10,
	}
}
