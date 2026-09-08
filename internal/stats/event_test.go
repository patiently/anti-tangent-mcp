package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestCountFindings(t *testing.T) {
	findings := []verdict.Finding{
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryScopeDrift},
		{Severity: verdict.SeverityMajor, Category: verdict.CategoryAmbiguousSpec},
		{Severity: verdict.SeverityMinor, Category: verdict.CategoryScopeDrift},
	}
	sev, cat, total := CountFindings(findings)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if sev["major"] != 2 || sev["minor"] != 1 {
		t.Fatalf("severity = %v", sev)
	}
	if cat["scope_drift"] != 2 || cat["ambiguous_spec"] != 1 {
		t.Fatalf("category = %v", cat)
	}
}

func TestCountFindingsEmpty(t *testing.T) {
	sev, cat, total := CountFindings(nil)
	if sev != nil || cat != nil || total != 0 {
		t.Fatalf("want nil,nil,0; got %v,%v,%d", sev, cat, total)
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
