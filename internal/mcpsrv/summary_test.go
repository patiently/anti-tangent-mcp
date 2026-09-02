package mcpsrv

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestFormatEnvelopeSummary_Basic(t *testing.T) {
	env := Envelope{
		SessionID: "sess-abc",
		Verdict:   string(verdict.VerdictWarn),
		Findings: []verdict.Finding{
			{
				Severity:   verdict.SeverityMajor,
				Category:   verdict.CategoryAmbiguousSpec,
				Criterion:  "AC #2",
				Evidence:   `"under 50ms" — at what load?`,
				Suggestion: "Pin the load profile (RPS).",
			},
		},
		NextAction: "Pin the load profile and re-run.",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
		ReviewMS:   1234,
	}
	got := formatEnvelopeSummary(env)
	wantLines := []string{
		"anti-tangent envelope",
		"  session_id:    sess-abc",
		"  verdict:       warn",
		"  model_used:    anthropic:claude-sonnet-4-6",
		"  review_ms:     1234",
		"  findings:      1 total (0 critical, 1 major, 0 minor)",
		`    - [major][ambiguous_spec] AC #2 — "under 50ms" — at what load?`,
		"  next_action:   Pin the load profile and re-run.",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Errorf("summary missing line %q\n--- got ---\n%s", line, got)
		}
	}
}

func TestFormatEnvelopeSummary_TruncatesLongEvidence(t *testing.T) {
	longEvidence := strings.Repeat("x", 500)
	env := Envelope{
		Verdict: string(verdict.VerdictPass),
		Findings: []verdict.Finding{{
			Severity:   verdict.SeverityMinor,
			Category:   verdict.CategoryOther,
			Criterion:  "long",
			Evidence:   longEvidence,
			Suggestion: "fix it",
		}},
		NextAction: "ok",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
	}
	got := formatEnvelopeSummary(env)
	lines := strings.Split(got, "\n")
	var findingLine string
	for _, l := range lines {
		if strings.Contains(l, "long") {
			findingLine = l
			break
		}
	}
	if findingLine == "" {
		t.Fatalf("could not find finding line:\n%s", got)
	}
	if !strings.Contains(findingLine, "…") {
		t.Errorf("expected truncation marker (…) in finding line, got:\n%s", findingLine)
	}
	if idx := strings.Index(findingLine, "— "); idx != -1 {
		evidence := findingLine[idx+len("— "):]
		// summaryEvidenceMax = 120 runes ASCII (= 120 bytes) + 3 bytes for the
		// UTF-8 ellipsis. Allow a small margin (≤124 bytes).
		if len(evidence) > 124 {
			t.Errorf("evidence too long: %d bytes (expected ≤124 with UTF-8 marker)\n%s", len(evidence), evidence)
		}
	}
}

func TestFormatEnvelopeSummary_NoSession_NoTTLLine(t *testing.T) {
	env := Envelope{
		Verdict:    string(verdict.VerdictFail),
		Findings:   nil,
		NextAction: "Re-submit with complete evidence.",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
	}
	got := formatEnvelopeSummary(env)
	if strings.Contains(got, "session_ttl_remaining_seconds") {
		t.Errorf("no-TTL envelope should not include session_ttl line:\n%s", got)
	}
}

func TestFormatEnvelopeSummary_Partial_LineShown(t *testing.T) {
	env := Envelope{
		SessionID:  "sess-xyz",
		Verdict:    string(verdict.VerdictWarn),
		Partial:    true,
		NextAction: "Retry with higher max_tokens_override.",
		ModelUsed:  "anthropic:claude-sonnet-4-6",
	}
	got := formatEnvelopeSummary(env)
	if !strings.Contains(got, "partial:       true") {
		t.Errorf("partial=true envelope should show the partial line:\n%s", got)
	}
}

func TestFormatEnvelopeSummary_SubmissionDefectLine(t *testing.T) {
	got := formatEnvelopeSummary(Envelope{
		SessionID: "s1", Verdict: "fail", NextAction: "n",
		ModelUsed: "m", SubmissionDefectOnly: true,
	})
	assert.Contains(t, got, "submission_defect_only: true")
	assert.Contains(t, got, "no code rework implied")
}

func TestFormatPlanSummary_Basic(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict:  verdict.VerdictWarn,
		PlanFindings: []verdict.Finding{},
		Tasks: []verdict.PlanTaskResult{
			{
				TaskIndex:             1,
				TaskTitle:             "Task 1: example",
				Verdict:               verdict.VerdictPass,
				Findings:              []verdict.Finding{},
				SuggestedHeaderBlock:  "",
				SuggestedHeaderReason: "",
			},
		},
		NextAction:  "go",
		PlanQuality: verdict.PlanQualityRigorous,
	}
	got := formatPlanSummary(pr, planSummaryMeta{ModelUsed: "anthropic:claude-opus-4-7", ReviewMS: 5678})
	for _, line := range []string{
		"anti-tangent envelope (validate_plan)",
		"  plan_verdict:  warn",
		"  plan_quality:  rigorous",
		"  model_used:    anthropic:claude-opus-4-7",
		"  review_ms:     5678",
		"  tasks: 1",
		"    Task 1: Task 1: example  [pass]",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("plan summary missing line %q\n--- got ---\n%s", line, got)
		}
	}
}

func TestFormatPlanSummary_PartialFlag_Shown(t *testing.T) {
	pr := verdict.PlanResult{
		PlanVerdict: verdict.VerdictWarn,
		Partial:     true,
		NextAction:  "retry",
		PlanQuality: verdict.PlanQualityActionable,
	}
	got := formatPlanSummary(pr, planSummaryMeta{ModelUsed: "anthropic:claude-opus-4-7", ReviewMS: 100})
	if !strings.Contains(got, "partial:       true") {
		t.Errorf("partial=true plan should show partial line:\n%s", got)
	}
}

func TestFormatPlanSummarySource(t *testing.T) {
	pr := verdict.PlanResult{PlanVerdict: verdict.VerdictPass, PlanQuality: "good"}

	t.Run("with source", func(t *testing.T) {
		out := formatPlanSummary(pr, planSummaryMeta{
			ModelUsed: "anthropic:claude-sonnet-4-6",
			ReviewMS:  1200,
			Source:    "/abs/plan.md (170158 B, sha256 4f2a9c1e…)",
		})
		assert.Contains(t, out, "source:")
		assert.Contains(t, out, "/abs/plan.md (170158 B, sha256 4f2a9c1e…)")
	})

	t.Run("without source", func(t *testing.T) {
		out := formatPlanSummary(pr, planSummaryMeta{ModelUsed: "m", ReviewMS: 1})
		assert.NotContains(t, out, "source:")
	})
}

func TestFormatPlanSummary_ContextFiles(t *testing.T) {
	pr := verdict.PlanResult{PlanVerdict: verdict.VerdictPass, PlanQuality: verdict.PlanQualityActionable}
	out := formatPlanSummary(pr, planSummaryMeta{
		ModelUsed: "anthropic:m",
		ContextFiles: []fileSource{
			{Path: "/repo/a.go", Bytes: 1204, SHA256: "9f2ab41c00"},
			{Path: "/repo/b.go", Bytes: 22, SHA256: "3c1af09b00"},
		},
	})
	assert.Contains(t, out, "context:       2 files, 1226 B")
	assert.Contains(t, out, "/repo/a.go")
	assert.Contains(t, out, "/repo/b.go")
}

func TestFormatPlanSummary_NoContextFilesOmitsLine(t *testing.T) {
	pr := verdict.PlanResult{PlanVerdict: verdict.VerdictPass, PlanQuality: verdict.PlanQualityActionable}
	out := formatPlanSummary(pr, planSummaryMeta{ModelUsed: "anthropic:m"})
	assert.NotContains(t, out, "context:")
}
