package notices

import (
	"os"
	"strings"
	"testing"
)

// noticesPath is relative because tests run with the package directory as cwd.
const noticesPath = "../../THIRD_PARTY_NOTICES.md"

func TestThirdPartyNoticesPresent(t *testing.T) {
	b, err := os.ReadFile(noticesPath)
	if err != nil {
		t.Fatalf("THIRD_PARTY_NOTICES.md is required: the shunt-derived hooks under "+
			"plugin/anti-tangent-shunt/ are Apache-2.0 and must carry attribution: %v", err)
	}
	body := string(b)
	// Substantive check, not a keyword check: a notice gutted down to the word
	// "Apache" would satisfy a naive test while failing the licence obligation.
	for _, want := range []string{
		"Spotify AB",
		"https://github.com/spotify/portal-ai-plugins",
		"MIT License",
		// Stable markers from three separate parts of the Apache-2.0 text.
		"Apache License",
		"TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION",
		"Version 2.0, January 2004",
		"WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("THIRD_PARTY_NOTICES.md must contain %q", want)
		}
	}
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
}
