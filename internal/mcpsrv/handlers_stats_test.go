package mcpsrv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/internal/stats"
)

func TestValidateTaskSpecRecordsStats(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}

	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": rv},
		Stats:    rec,
	}}

	_, _, err = h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle:          "Add healthz",
		Goal:               "Expose a liveness probe",
		AcceptanceCriteria: []string{"GET /healthz returns 200 ok"},
	})
	if err != nil {
		t.Fatalf("ValidateTaskSpec: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("expected one event recorded, file is empty")
	}
}

// TestValidatePlanRecordsStats verifies that ValidatePlan with a Stats recorder
// writes exactly one event to events.jsonl, and that the event's Tool is
// "validate_plan" and its Verdict matches the plan-level verdict.
func TestValidatePlanRecordsStats(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 10000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}

	// Single-task plan so the single-call path fires (< default chunkSize=8).
	plan := buildPlanWithNTasks(1)
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"plan_verdict":"pass","plan_quality":"actionable","plan_findings":[],"tasks":[{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""}],"next_action":"Proceed."}`),
			Model:   "claude-sonnet-4-6",
		},
	}
	cfg, cfgErr := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	if cfgErr != nil {
		t.Fatalf("config.Load: %v", cfgErr)
	}

	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     rec,
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(cfg.SessionTTL),
	}}

	_, pr, callErr := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	if callErr != nil {
		t.Fatalf("ValidatePlan: %v", callErr)
	}

	// Read and decode events.jsonl.
	b, readErr := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if readErr != nil {
		t.Fatalf("events.jsonl: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 event line, got %d: %q", len(lines), string(b))
	}

	var ev stats.Event
	if jsonErr := json.Unmarshal([]byte(lines[0]), &ev); jsonErr != nil {
		t.Fatalf("unmarshal event: %v", jsonErr)
	}
	if ev.Tool != "validate_plan" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_plan")
	}
	if ev.Verdict == "" {
		t.Error("event.Verdict must be non-empty")
	}
	if ev.Verdict != string(pr.PlanVerdict) {
		t.Errorf("event.Verdict = %q, want %q (plan verdict)", ev.Verdict, string(pr.PlanVerdict))
	}
	// buildPlanWithNTasks(1) emits one task with a structured Goal /
	// Acceptance criteria header, so both counts must be 1.
	if ev.TasksTotal != 1 {
		t.Errorf("event.TasksTotal = %d, want 1", ev.TasksTotal)
	}
	if ev.TasksWithHeader != 1 {
		t.Errorf("event.TasksWithHeader = %d, want 1", ev.TasksWithHeader)
	}
}

// TestValidatePlan_CacheHitRecordsPlanHeaderCounts verifies that the
// plan-pass-cache-hit recordStat call inside ValidatePlan (the "cached: true"
// branch) also threads tasksTotal/tasksWithHeader, and that the threaded
// counts match the original (reviewer) call's counts. Before this test, only
// the final-success call site had any assertion on these fields, so a
// regression at the cache-hit call site would have shipped silently.
func TestValidatePlan_CacheHitRecordsPlanHeaderCounts(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	rv := &fakeReviewer{name: "anthropic", resp: passPlanResp("Proceed with implementation.")}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     newStatsRecorder(t, dir),
		planCache: newPlanPassCache(),
		PlanRuns:  planrun.NewStore(cfg.SessionTTL),
	}}

	// 3 headered tasks so the counts are unambiguously non-zero and not just
	// coincidentally equal to some other field.
	args := ValidatePlanArgs{PlanText: buildPlanWithNTasks(3)}

	_, first, callErr := h.ValidatePlan(context.Background(), nil, args)
	if callErr != nil {
		t.Fatalf("ValidatePlan (first call): %v", callErr)
	}
	if string(first.PlanVerdict) != "pass" {
		t.Fatalf("first call PlanVerdict = %q, want %q (only pass results are cached)", first.PlanVerdict, "pass")
	}

	_, _, callErr = h.ValidatePlan(context.Background(), nil, args)
	if callErr != nil {
		t.Fatalf("ValidatePlan (second call): %v", callErr)
	}
	if rv.Calls != 1 {
		t.Fatalf("rv.Calls = %d, want 1; second ValidatePlan call must be a cache hit, not a fresh reviewer call", rv.Calls)
	}

	b, readErr := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if readErr != nil {
		t.Fatalf("events.jsonl: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 event lines (reviewer call + cache hit), got %d: %q", len(lines), string(b))
	}

	var firstEv, secondEv stats.Event
	if err := json.Unmarshal([]byte(lines[0]), &firstEv); err != nil {
		t.Fatalf("unmarshal first event: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &secondEv); err != nil {
		t.Fatalf("unmarshal second event: %v", err)
	}

	if firstEv.Cached {
		t.Error("first event.Cached = true, want false (this is the reviewer call, not the cache hit)")
	}
	if !secondEv.Cached {
		t.Fatal("second event.Cached = false, want true; test setup did not produce a cache hit")
	}
	if firstEv.TasksTotal != 3 || firstEv.TasksWithHeader != 3 {
		t.Fatalf("first (reviewer) event TasksTotal/TasksWithHeader = %d/%d, want 3/3", firstEv.TasksTotal, firstEv.TasksWithHeader)
	}
	if secondEv.TasksTotal != firstEv.TasksTotal || secondEv.TasksWithHeader != firstEv.TasksWithHeader {
		t.Errorf("cache-hit event TasksTotal/TasksWithHeader = %d/%d, want %d/%d (must match the original reviewer call)",
			secondEv.TasksTotal, secondEv.TasksWithHeader, firstEv.TasksTotal, firstEv.TasksWithHeader)
	}
}

// TestValidatePlan_PartialRecoveryRecordsPlanHeaderCounts verifies that the
// handlePlanReviewErr handled/partial-recovery recordStat call inside
// ValidatePlan (the truncation-recovery branch, distinct from both the
// cache-hit and final-success branches) also threads
// tasksTotal/tasksWithHeader. Mirrors the existing
// TestValidatePlan_PartialFindingsRecoveredOnTruncation scenario (task 1
// complete, task 2 truncated mid-response) but with a headered plan so the
// counts are non-zero and distinguishable from an unwired zero value.
func TestValidatePlan_PartialRecoveryRecordsPlanHeaderCounts(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)

	rawJSON := []byte(`{"plan_verdict":"warn","plan_findings":[],"tasks":[` +
		`{"task_index":1,"task_title":"Task 1: t1","verdict":"pass","findings":[],"suggested_header_block":"","suggested_header_reason":""},` +
		`{"task_index":2,"task_title":"Task 2: t2","verdict":"warn","find`)
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{RawJSON: rawJSON, Model: "claude-sonnet-4-6"},
		err:  providers.ErrResponseTruncated,
	}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     newStatsRecorder(t, dir),
		planCache: newPlanPassCache(),
	}}

	plan := buildPlanWithNTasks(2) // both tasks headered
	_, pr, callErr := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: plan})
	if callErr != nil {
		t.Fatalf("ValidatePlan: %v", callErr)
	}
	if !pr.Partial {
		t.Fatal("expected pr.Partial=true on truncation recovery; test setup did not exercise the partial-recovery branch")
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "validate_plan" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_plan")
	}
	if !ev.Partial {
		t.Errorf("event.Partial = false, want true")
	}
	if ev.TasksTotal != 2 {
		t.Errorf("event.TasksTotal = %d, want 2", ev.TasksTotal)
	}
	if ev.TasksWithHeader != 2 {
		t.Errorf("event.TasksWithHeader = %d, want 2", ev.TasksWithHeader)
	}
}

// TestValidateTaskSpec_PartialRecoveryRecordsStat verifies that a
// truncation-recovered ValidateTaskSpec review records exactly one stat event
// with Partial=true, so partial_rate in the rollup is accurate.
func TestValidateTaskSpec_PartialRecoveryRecordsStat(t *testing.T) {
	dir := t.TempDir()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 1000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}

	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Reviewer returns partial JSON (one complete finding + truncation mid-second).
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{
			RawJSON: []byte(`{"verdict":"warn","findings":[` +
				`{"severity":"major","category":"other","criterion":"ac1","evidence":"e1","suggestion":"s1"},` +
				`{"severity":"minor","category":"other","crit`),
			Model: "claude-sonnet-4-6",
		},
		err: providers.ErrResponseTruncated,
	}
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": rv},
		Stats:    rec,
	}}

	_, env, callErr := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if callErr != nil {
		t.Fatalf("ValidateTaskSpec: %v", callErr)
	}
	if !env.Partial {
		t.Fatal("expected envelope.Partial=true on truncation recovery")
	}

	b, readErr := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if readErr != nil {
		t.Fatalf("events.jsonl: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 stat event, got %d: %q", len(lines), string(b))
	}

	var ev stats.Event
	if jsonErr := json.Unmarshal([]byte(lines[0]), &ev); jsonErr != nil {
		t.Fatalf("unmarshal event: %v", jsonErr)
	}
	if ev.Tool != "validate_task_spec" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_task_spec")
	}
	if !ev.Partial {
		t.Errorf("event.Partial = false, want true; partial_rate would remain silently 0")
	}
}

// newStatsRecorder builds a no-reviewer recorder writing into dir. Shared by the
// early-return recording tests below.
func newStatsRecorder(t *testing.T, dir string) *stats.Recorder {
	t.Helper()
	rec, err := stats.New(stats.Options{
		Dir: dir, Reviewer: nil,
		SummaryInterval: 24 * time.Hour, SummaryThreshold: 100000, RetentionDays: 30,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("stats.New: %v", err)
	}
	return rec
}

// readSingleEvent asserts events.jsonl holds exactly one record and returns it.
func readSingleEvent(t *testing.T, dir string) stats.Event {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("events.jsonl is empty, expected exactly 1 event")
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 event line, got %d: %q", len(lines), string(b))
	}
	var ev stats.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return ev
}

func statsTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// TestCheckProgressSessionMissingRecordsStat pins that a structured early-return
// (session not found) still lands exactly one events.jsonl record. Before the
// fix, recordStat only fired on the reviewer-result / truncation paths, so
// session-missing exits silently undercounted hook calls.
func TestCheckProgressSessionMissingRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}},
		Stats:    newStatsRecorder(t, dir),
	}}

	_, _, err := h.CheckProgress(context.Background(), nil, CheckProgressArgs{
		SessionID: "does-not-exist", WorkingOn: "stuff",
	})
	if err != nil {
		t.Fatalf("CheckProgress: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "check_progress" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "check_progress")
	}
	if ev.Verdict != "fail" {
		t.Errorf("event.Verdict = %q, want %q", ev.Verdict, "fail")
	}
}

// TestValidatePlanNoHeadingsRecordsStat pins that the no-headings early return
// (zero `### Task N:` headings) records one validate_plan event.
func TestValidatePlanNoHeadingsRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}},
		Stats:     newStatsRecorder(t, dir),
		planCache: newPlanPassCache(),
	}}

	_, _, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{PlanText: "no task headings here, just prose"})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "validate_plan" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_plan")
	}
	if ev.Verdict != "fail" {
		t.Errorf("event.Verdict = %q, want %q", ev.Verdict, "fail")
	}
	// No TasksTotal/TasksWithHeader assertion here on purpose: the no-headings
	// scenario structurally always parses to zero tasks, so those fields read
	// 0 via Go's zero value whether or not the no-headings call site threads
	// them at all. An assertion here cannot fail and provides no regression
	// signal — see TestValidatePlan_CacheHitRecordsPlanHeaderCounts and
	// TestValidatePlan_PartialRecoveryRecordsPlanHeaderCounts for call sites
	// that actually exercise non-zero counts.
}

// TestValidatePlan_ContextPathsPayloadTooLarge_RecordsContextBytes pins the
// plan-cap-breach recordStat call site (the "total := planBytes + pkBytes"
// branch): contextBytes is resolved before this branch runs, and its bytes
// must be folded into the recorded payloadBytes even though the cap
// COMPARISON itself deliberately excludes them (attachments have their own
// separate budget — see tooLargePlanResult's doc comment). Before the fix,
// this call site read the pre-existing local `total` (== planBytes+pkBytes)
// verbatim, so a successful context_paths resolution alongside an over-cap
// plan silently under-reported payloadBytes by exactly the attachment size.
func TestValidatePlan_ContextPathsPayloadTooLarge_RecordsContextBytes(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	cfg.PlanMaxPayloadBytes = 20 // buildPlanWithNTasks(1) alone already exceeds this

	ctxDir := t.TempDir()
	f := filepath.Join(ctxDir, "config.go")
	contextContent := "package config\n// SENTINEL_ATTACHED\n"
	if err := os.WriteFile(f, []byte(contextContent), 0o600); err != nil {
		t.Fatalf("write context file: %v", err)
	}

	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": rv},
		Stats:     newStatsRecorder(t, dir),
		planCache: newPlanPassCache(),
	}}

	planText := buildPlanWithNTasks(1)
	_, pr, err := h.ValidatePlan(context.Background(), nil, ValidatePlanArgs{
		PlanText:     planText,
		ContextPaths: []string{f},
	})
	if err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	if pr.PlanVerdict != "fail" {
		t.Fatalf("pr.PlanVerdict = %q, want %q (plan cap must trip)", pr.PlanVerdict, "fail")
	}
	if rv.Calls != 0 {
		t.Fatalf("rv.Calls = %d, want 0 (an over-cap rejection must short-circuit the reviewer)", rv.Calls)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "validate_plan" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "validate_plan")
	}
	if ev.Verdict != "fail" {
		t.Errorf("event.Verdict = %q, want %q", ev.Verdict, "fail")
	}
	wantPayload := len(planText) + len(contextContent) // pkBytes is 0
	if ev.PayloadBytes != wantPayload {
		t.Errorf("event.PayloadBytes = %d, want %d (planBytes=%d + contextBytes=%d); "+
			"the plan-cap-breach recordStat call site must include contextBytes",
			ev.PayloadBytes, wantPayload, len(planText), len(contextContent))
	}
}

// TestExtractEmptyEnvelopesRecordsStat pins that the empty-completion_envelopes
// refusal records one extract event carrying the verdict (previously the
// success-only recordStat skipped this path AND omitted the verdict).
func TestExtractEmptyEnvelopesRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}},
		Stats:    newStatsRecorder(t, dir),
	}}

	_, _, err := h.ExtractProjectKnowledge(context.Background(), nil, ExtractProjectKnowledgeArgs{})
	if err != nil {
		t.Fatalf("ExtractProjectKnowledge: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "extract_project_knowledge" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "extract_project_knowledge")
	}
	if ev.Verdict != "fail" {
		t.Errorf("event.Verdict = %q, want %q (verdict must be populated)", ev.Verdict, "fail")
	}
}

// TestPrimeTruncationRecordsStat pins that a truncated prime review records one
// event carrying the warn verdict — the non-success structured path that the
// old success-only recordStat skipped.
func TestPrimeTruncationRecordsStat(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	rv := &fakeReviewer{
		name: "anthropic",
		resp: providers.Response{RawJSON: []byte(`{"picks":[`), Model: "claude-sonnet-4-6"},
		err:  providers.ErrResponseTruncated,
	}
	h := &handlers{deps: Deps{
		Cfg:      cfg,
		Sessions: session.NewStore(cfg.SessionTTL),
		Reviews:  providers.Registry{"anthropic": rv},
		Stats:    newStatsRecorder(t, dir),
	}}

	_, _, err := h.PrimeProjectKnowledge(context.Background(), nil, PrimeProjectKnowledgeArgs{
		TaskTitle: "T", Goal: "G", AcceptanceCriteria: []string{"AC"},
	})
	if err != nil {
		t.Fatalf("PrimeProjectKnowledge: %v", err)
	}

	ev := readSingleEvent(t, dir)
	if ev.Tool != "prime_project_knowledge" {
		t.Errorf("event.Tool = %q, want %q", ev.Tool, "prime_project_knowledge")
	}
	if ev.Verdict != "warn" {
		t.Errorf("event.Verdict = %q, want %q", ev.Verdict, "warn")
	}
}

func TestNilStatsDisabledNoFiles(t *testing.T) {
	// Nil Stats must not panic or error; there is no stats dir to check.
	cfg, _ := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "x"
		}
		return ""
	})
	rv := &fakeReviewer{name: "anthropic", resp: passResp("claude-sonnet-4-6")}
	h := &handlers{deps: Deps{
		Cfg: cfg, Sessions: session.NewStore(cfg.SessionTTL),
		Reviews: providers.Registry{"anthropic": rv}, Stats: nil,
	}}
	_, _, err := h.ValidateTaskSpec(context.Background(), nil, ValidateTaskSpecArgs{
		TaskTitle: "t", Goal: "g", AcceptanceCriteria: []string{"a"},
	})
	if err != nil {
		t.Fatalf("ValidateTaskSpec: %v", err)
	}
}

// TestValidatePlan_CacheHitBillsAttachmentBytesOnce pins the cache-hit
// recordStat call site. contextPayloadBytes multiplies the attached set by
// rendered.reviewerCalls() because a chunked round re-sends every attached
// byte on every reviewer call — but a cache HIT makes no reviewer call at
// all, so it sends the set zero times. Using the multiplied figure there
// billed a 3-call round's worth of attachment traffic for a round that sent
// none, and the miss got worse, not better, once the multiplier landed.
func TestValidatePlan_CacheHitBillsAttachmentBytesOnce(t *testing.T) {
	dir := t.TempDir()
	cfg := statsTestConfig(t)
	cfg.PlanTasksPerChunk = 1 // force the chunked path: reviewerCalls() > 1

	ctxDir := t.TempDir()
	f := filepath.Join(ctxDir, "attached.go")
	contextContent := "package a\n// SENTINEL_ATTACHED\n"
	if err := os.WriteFile(f, []byte(contextContent), 0o600); err != nil {
		t.Fatalf("write context file: %v", err)
	}

	sr := &scriptedReviewer{responses: []providers.Response{
		passOneResp(),
		chunkResp(t, titlesRange(1, 1)),
		chunkResp(t, titlesRange(2, 2)),
	}}
	h := &handlers{deps: Deps{
		Cfg:       cfg,
		Sessions:  session.NewStore(cfg.SessionTTL),
		Reviews:   providers.Registry{"anthropic": sr},
		Stats:     newStatsRecorder(t, dir),
		PlanRuns:  planrun.NewStore(time.Hour),
		planCache: newPlanPassCache(),
	}}

	planText := buildPlanWithNTasks(2)
	args := ValidatePlanArgs{PlanText: planText, ContextPaths: []string{f}}

	if _, _, err := h.ValidatePlan(context.Background(), nil, args); err != nil {
		t.Fatalf("ValidatePlan (fresh): %v", err)
	}
	if _, _, err := h.ValidatePlan(context.Background(), nil, args); err != nil {
		t.Fatalf("ValidatePlan (cached): %v", err)
	}

	evs := readEvents(t, dir)
	if len(evs) != 2 {
		t.Fatalf("expected 2 stat events (one fresh, one cache hit), got %d", len(evs))
	}
	fresh, cached := evs[0], evs[1]
	if !cached.Cached {
		t.Fatalf("second event must be the cache hit; Cached = %v", cached.Cached)
	}

	// The fresh round really did re-send the attachment on every one of its
	// reviewer calls, so its bill is the multiplied one. That half must keep
	// working — otherwise this test would pass against a fix that simply
	// deleted the multiplier.
	wantFresh := len(planText) + len(contextContent)*3
	if fresh.PayloadBytes != wantFresh {
		t.Errorf("fresh event.PayloadBytes = %d, want %d (planBytes=%d + contextBytes=%d x 3 reviewer calls)",
			fresh.PayloadBytes, wantFresh, len(planText), len(contextContent))
	}

	wantCached := len(planText) + len(contextContent)
	if cached.PayloadBytes != wantCached {
		t.Errorf("cache-hit event.PayloadBytes = %d, want %d (planBytes=%d + contextBytes=%d, counted ONCE); "+
			"a cache hit makes no reviewer call, so it must not be billed per reviewer call",
			cached.PayloadBytes, wantCached, len(planText), len(contextContent))
	}
}

// readEvents returns every record in events.jsonl, in order.
func readEvents(t *testing.T, dir string) []stats.Event {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("events.jsonl is empty")
	}
	var out []stats.Event
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		var ev stats.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal event %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}
