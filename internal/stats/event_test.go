package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestCountFindings(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryScopeDrift},
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryAmbiguousSpec},
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryScopeDrift},
	}
	sev, cat, crit, total := CountFindings(findings)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if sev["major"] != 2 || sev["minor"] != 1 {
		t.Fatalf("severity = %v", sev)
	}
	if cat["scope_drift"] != 2 || cat["ambiguous_spec"] != 1 {
		t.Fatalf("category = %v", cat)
	}
	if crit != nil {
		t.Fatalf("criterion should be nil for findings with no allowlisted criteria, got %v", crit)
	}
}

func TestCountFindingsEmpty(t *testing.T) {
	sev, cat, crit, total := CountFindings(nil)
	if sev != nil || cat != nil || crit != nil || total != 0 {
		t.Fatalf("want nil,nil,nil,0; got %v,%v,%v,%d", sev, cat, crit, total)
	}
}

func TestEventTokenFieldsOmittedWhenZero(t *testing.T) {
	b, err := json.Marshal(Event{Ts: time.Now(), Tool: "check_progress"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"input_tokens", "output_tokens"} {
		if strings.Contains(string(b), k) {
			t.Errorf("%s must be omitted when zero, got %s", k, b)
		}
	}
	b, err = json.Marshal(Event{Ts: time.Now(), Tool: "bulk_read", InputTokens: 5, OutputTokens: 7})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"input_tokens":5`, `"output_tokens":7`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("expected %s in %s", k, b)
		}
	}
}

func TestCountFindings_CriterionAllowlistOnly(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: "comment_hygiene", Evidence: "e", Suggestion: "s"},
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: "the exporter MUST emit one span per outbound request",
			Evidence: "e", Suggestion: "s"},
	}
	_, _, crit, total := CountFindings(findings)
	require.Equal(t, 2, total)
	assert.Equal(t, 1, crit["comment_hygiene"])
	assert.Len(t, crit, 1,
		"verbatim acceptance-criterion text must never reach the ledger")
}

func TestCountFindings_NoAllowlistedCriteriaYieldsNilMap(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryQuality,
			Criterion: "some verbatim AC", Evidence: "e", Suggestion: "s"},
	}
	_, _, crit, _ := CountFindings(findings)
	assert.Nil(t, crit, "nil map keeps criterion_counts out of the JSON via omitempty")

	blob, err := json.Marshal(Event{Tool: "validate_task_spec", CriterionCounts: crit})
	require.NoError(t, err)
	assert.NotContains(t, string(blob), "criterion_counts",
		"omitempty must actually drop the key, not merely leave it null")
}
