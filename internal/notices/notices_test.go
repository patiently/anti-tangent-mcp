package notices

import (
	"os"
	"strings"
	"testing"
)

// noticesPath is relative because tests run with the package directory as cwd.
const noticesPath = "../../THIRD_PARTY_NOTICES.md"
const canonicalLicensePath = "testdata/apache-2.0.txt"

func TestThirdPartyNoticesPresent(t *testing.T) {
	b, err := os.ReadFile(noticesPath)
	if err != nil {
		t.Fatalf("THIRD_PARTY_NOTICES.md is required: the shunt-derived hooks under "+
			"plugin/anti-tangent-shunt/ are Apache-2.0 and must carry attribution: %v", err)
	}
	body := string(b)
	// Check attribution metadata: copyright holder, upstream URL, and MIT statement.
	for _, want := range []string{
		"Spotify AB",
		"https://github.com/spotify/portal-ai-plugins",
		"MIT License",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("THIRD_PARTY_NOTICES.md must contain %q", want)
		}
	}
	// Check that all derived files are listed.
	for _, path := range []string{
		"plugin/anti-tangent-shunt/hooks/check-file-size",
		"plugin/anti-tangent-shunt/hooks/check-bash-read",
		"plugin/anti-tangent-shunt/evals/run.sh",
		"plugin/anti-tangent-shunt/evals/hook-evals.json",
		"plugin/anti-tangent-shunt/evals/bash-hook-evals.json",
	} {
		if !strings.Contains(body, path) {
			t.Errorf("THIRD_PARTY_NOTICES.md must list the derived file %s", path)
		}
	}
	// Verify the full Apache-2.0 license text is present and complete.
	// Extract the license block from THIRD_PARTY_NOTICES.md (everything after the "---" separator).
	parts := strings.Split(body, "---")
	if len(parts) < 2 {
		t.Fatal("THIRD_PARTY_NOTICES.md must contain license text after a --- separator")
	}
	licenseBlock := strings.TrimSpace(parts[len(parts)-1])
	// Read the canonical license text.
	canonical, err := os.ReadFile(canonicalLicensePath)
	if err != nil {
		t.Fatalf("canonical Apache-2.0 text required at %s: %v", canonicalLicensePath, err)
	}
	canonicalText := strings.TrimSpace(string(canonical))
	// Compare: the license block in THIRD_PARTY_NOTICES.md must match the canonical version.
	// This ensures the full text with all nine numbered sections is present, and prevents
	// a notice gutted down to only keywords and attribution metadata.
	if licenseBlock != canonicalText {
		t.Errorf("License text in THIRD_PARTY_NOTICES.md does not match canonical Apache-2.0 text. " +
			"This can indicate the licence has been truncated or modified. " +
			"The full, unmodified Apache License Version 2.0 is required by Apache-2.0 §4.")
	}
}

// The ponytail ruleset adapted into lean.tmpl and plan_lean_rules.tmpl is
// MIT-licensed; the notice must survive alongside the shunt entry.
func TestThirdPartyNoticesListPonytail(t *testing.T) {
	b, err := os.ReadFile(noticesPath)
	if err != nil {
		t.Fatalf("THIRD_PARTY_NOTICES.md is required: %v", err)
	}
	body := string(b)
	for _, want := range []string{
		"## ponytail",
		"https://github.com/DietrichGebert/ponytail",
		"Copyright (c) 2026 DietrichGebert",
		"MIT",
		"e3ba2aa6f1e6f0bc4d69eb09c9f0d0a93af56156",
		"internal/prompts/templates/lean.tmpl",
		"internal/prompts/templates/plan_lean_rules.tmpl",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("THIRD_PARTY_NOTICES.md must contain %q", want)
		}
	}
}
