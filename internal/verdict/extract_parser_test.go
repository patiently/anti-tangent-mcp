package verdict_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// happyExtract returns a fully-populated valid extract envelope as a JSON
// string. Tests mutate it via fmt.Sprintf-style template helpers below.
const happyExtract = `{
	"verdict": "pass",
	"findings": [],
	"proposals": [
		{
			"action": "create",
			"type": "decision",
			"permalink": "decisions/0099-x",
			"title": "Title",
			"frontmatter_json": "{}",
			"body": "body content",
			"body_patch": "",
			"rationale": "rationale",
			"evidence_refs": ["completion[0].finding[0]"],
			"supersedes": []
		}
	],
	"bm_commands": [],
	"next_action": "attach proposals and dispatch"
}`

func TestParseExtract_Happy(t *testing.T) {
	r, err := verdict.ParseExtract([]byte(happyExtract))
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != verdict.VerdictPass {
		t.Fatalf("verdict: got %q, want pass", r.Verdict)
	}
	if len(r.Proposals) != 1 {
		t.Fatalf("proposals: got %d, want 1", len(r.Proposals))
	}
	p := r.Proposals[0]
	if p.Action != verdict.ProposalActionCreate || p.Type != verdict.ProposalTypeDecision {
		t.Fatalf("unexpected proposal: %+v", p)
	}
	if p.FrontmatterJSON != "{}" {
		t.Fatalf("frontmatter_json placeholder lost: %q", p.FrontmatterJSON)
	}
	if p.Body != "body content" || p.BodyPatch != "" {
		t.Fatalf("body/body_patch: %q / %q", p.Body, p.BodyPatch)
	}
	if r.BMCommands == nil {
		t.Fatalf("BMCommands must be non-nil even when empty")
	}
	// SummaryBlock and Partial are server-owned — must stay zero.
	if r.SummaryBlock != "" || r.Partial {
		t.Fatalf("server-owned fields populated by parser: %+v", r)
	}
}

func TestParseExtract_TopLevelErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"invalid verdict", `{"verdict":"oops","findings":[],"proposals":[],"bm_commands":[],"next_action":"x"}`, "invalid verdict"},
		{"missing next_action", `{"verdict":"pass","findings":[],"proposals":[],"bm_commands":[]}`, "next_action is required"},
		{"missing findings", `{"verdict":"pass","proposals":[],"bm_commands":[],"next_action":"x"}`, "findings is required"},
		{"missing proposals", `{"verdict":"pass","findings":[],"bm_commands":[],"next_action":"x"}`, "proposals is required"},
		{"missing bm_commands", `{"verdict":"pass","findings":[],"proposals":[],"next_action":"x"}`, "bm_commands is required"},
		{"extra fields rejected", `{"verdict":"pass","findings":[],"proposals":[],"bm_commands":[],"next_action":"x","mystery":1}`, "unknown field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.ParseExtract([]byte(c.raw))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParseExtract_FindingValidation(t *testing.T) {
	cases := []struct {
		name    string
		finding string
		want    string
	}{
		{"empty criterion", `{"severity":"minor","category":"quality","criterion":"","evidence":"e","suggestion":"s"}`, "criterion is required"},
		{"empty evidence", `{"severity":"minor","category":"quality","criterion":"c","evidence":"","suggestion":"s"}`, "evidence is required"},
		{"empty suggestion", `{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":""}`, "suggestion is required"},
		{"invalid category", `{"severity":"minor","category":"bogus","criterion":"c","evidence":"e","suggestion":"s"}`, "invalid category"},
		{"invalid severity", `{"severity":"huge","category":"quality","criterion":"c","evidence":"e","suggestion":"s"}`, "invalid severity"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"verdict":"warn","findings":[%s],"proposals":[],"bm_commands":[],"next_action":"x"}`, c.finding)
			_, err := verdict.ParseExtract([]byte(payload))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParseExtract_AcceptsNewExtractCategories(t *testing.T) {
	// Extract-specific categories must be accepted by ParseExtract.
	for _, c := range []verdict.Category{
		verdict.CategoryInsufficientEvidence,
		verdict.CategoryRedundantProposal,
		verdict.CategoryContradictsExisting,
	} {
		raw := []byte(`{"verdict":"warn","findings":[{"severity":"minor","category":"` + string(c) + `","criterion":"c","evidence":"e","suggestion":"s"}],"proposals":[],"bm_commands":[],"next_action":"x"}`)
		if _, err := verdict.ParseExtract(raw); err != nil {
			t.Fatalf("category %q: %v", c, err)
		}
	}
}

func TestParseExtract_StripsFences(t *testing.T) {
	raw := []byte("```json\n" + happyExtract + "\n```")
	if _, err := verdict.ParseExtract(raw); err != nil {
		t.Fatalf("fenced parse: %v", err)
	}
}

// proposalCase is a template helper: it embeds the given proposal JSON
// (verbatim — including any deliberate field omissions) into an otherwise
// valid envelope and returns the bytes.
func proposalCase(proposal string) []byte {
	return []byte(fmt.Sprintf(
		`{"verdict":"warn","findings":[],"proposals":[%s],"bm_commands":[],"next_action":"x"}`,
		proposal,
	))
}

func TestParseExtract_ProposalEnumErrors(t *testing.T) {
	cases := []struct {
		name     string
		proposal string
		want     string
	}{
		{
			name:     "invalid action",
			proposal: `{"action":"delete","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "invalid action",
		},
		{
			name:     "invalid type",
			proposal: `{"action":"create","type":"saga","permalink":"p","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "invalid type",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.ParseExtract(proposalCase(c.proposal))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParseExtract_ProposalPresenceErrors(t *testing.T) {
	// Per-proposal required-field presence is enforced before action-conditional
	// rules. Missing scalar required fields (permalink/title/rationale) and
	// empty evidence_refs are rejected by name.
	cases := []struct {
		name     string
		proposal string
		want     string
	}{
		{
			name:     "empty permalink",
			proposal: `{"action":"create","type":"decision","permalink":"","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "permalink is required",
		},
		{
			name:     "empty title",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "title is required",
		},
		{
			name:     "empty rationale",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"","evidence_refs":["x"],"supersedes":[]}`,
			want:     "rationale is required",
		},
		{
			name:     "empty evidence_refs",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":[],"supersedes":[]}`,
			want:     "evidence_refs is required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.ParseExtract(proposalCase(c.proposal))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParseExtract_ProposalMissingPlaceholderableFields(t *testing.T) {
	// frontmatter_json / body / body_patch / supersedes are required to be
	// PRESENT on the wire — the schema requires them, and the parser's
	// *string-typed wire fields let it distinguish missing (nil pointer)
	// from present-but-empty placeholders. Missing → actionable hint.
	cases := []struct {
		name     string
		proposal string
		want     string
	}{
		{
			name:     "missing frontmatter_json",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"t","body":"b","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     `frontmatter_json is required (use "{}" for none)`,
		},
		{
			name:     "missing body",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     `body is required (use "" for none)`,
		},
		{
			name:     "missing body_patch",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"b","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     `body_patch is required (use "" for none)`,
		},
		{
			name:     "missing supersedes",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":["x"]}`,
			want:     "supersedes is required (use [] for none)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.ParseExtract(proposalCase(c.proposal))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParseExtract_ProposalFrontmatterJSONShape(t *testing.T) {
	// frontmatter_json must be a JSON object literal. Empty string is rejected
	// (use "{}" for none); JSON null unmarshals to nil map and is rejected;
	// array is rejected; malformed JSON is rejected.
	cases := []struct {
		name            string
		frontmatterJSON string
		want            string
	}{
		{"empty string", "", `frontmatter_json must not be empty string`},
		{"json null", "null", "JSON object literal"},
		{"json array", "[1,2,3]", "not a JSON object"},
		{"malformed", "{not json", "not a JSON object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Build a proposal where only frontmatter_json varies.
			frontmatterField, err := json.Marshal(c.frontmatterJSON)
			if err != nil {
				t.Fatal(err)
			}
			proposal := fmt.Sprintf(
				`{"action":"create","type":"decision","permalink":"p","title":"t","frontmatter_json":%s,"body":"b","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
				string(frontmatterField),
			)
			_, err = verdict.ParseExtract(proposalCase(proposal))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParseExtract_ActionConditionalRules(t *testing.T) {
	cases := []struct {
		name     string
		proposal string
		want     string
	}{
		{
			name:     "create with empty body",
			proposal: `{"action":"create","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "action=create requires non-empty body",
		},
		{
			name:     "update with neither body nor body_patch",
			proposal: `{"action":"update","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "action=update requires body or body_patch",
		},
		{
			name:     "supersede with empty supersedes",
			proposal: `{"action":"supersede","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "action=supersede requires non-empty supersedes",
		},
		{
			name:     "body and body_patch mutually exclusive",
			proposal: `{"action":"update","type":"decision","permalink":"p","title":"t","frontmatter_json":"{}","body":"b","body_patch":"--- a\n+++ b\n@@\n-x\n+y\n","rationale":"r","evidence_refs":["x"],"supersedes":[]}`,
			want:     "body and body_patch are mutually exclusive",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.ParseExtract(proposalCase(c.proposal))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParseExtract_SupersedeProposalAllowsEmptyBodies(t *testing.T) {
	// action=supersede may legitimately omit body/body_patch content — the
	// new superseding note (if any) ships as a separate create proposal.
	proposal := `{"action":"supersede","type":"decision","permalink":"decisions/0099-x","title":"t","frontmatter_json":"{}","body":"","body_patch":"","rationale":"r","evidence_refs":["x"],"supersedes":["decisions/0042-y"]}`
	r, err := verdict.ParseExtract(proposalCase(proposal))
	if err != nil {
		t.Fatalf("supersede with empty bodies should pass: %v", err)
	}
	if len(r.Proposals) != 1 || r.Proposals[0].Action != verdict.ProposalActionSupersede {
		t.Fatalf("unexpected: %+v", r.Proposals)
	}
}

func TestParseExtract_UpdateAcceptsBodyPatchOnly(t *testing.T) {
	proposal := `{"action":"update","type":"module","permalink":"modules/mcpsrv","title":"t","frontmatter_json":"{}","body":"","body_patch":"--- a\n+++ b\n@@\n-x\n+y\n","rationale":"r","evidence_refs":["x"],"supersedes":[]}`
	r, err := verdict.ParseExtract(proposalCase(proposal))
	if err != nil {
		t.Fatalf("update with body_patch only should pass: %v", err)
	}
	if r.Proposals[0].BodyPatch == "" || r.Proposals[0].Body != "" {
		t.Fatalf("body/body_patch content lost: %+v", r.Proposals[0])
	}
}

func TestParseExtract_BMCommandsRoundTrip(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [{"action":"create","type":"decision","permalink":"decisions/0099-x","title":"t","frontmatter_json":"{}","body":"b","body_patch":"","rationale":"r","evidence_refs":["completion[0].finding[0]"],"supersedes":[]}],
		"bm_commands": [{"tool": "write_note", "args_json": "{\"permalink\":\"decisions/0099-x\"}"}],
		"next_action": "go"
	}`)
	r, err := verdict.ParseExtract(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.BMCommands) != 1 || r.BMCommands[0].Tool != "write_note" {
		t.Fatalf("BMCommands not preserved: %+v", r.BMCommands)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(r.BMCommands[0].ArgsJSON), &args); err != nil {
		t.Fatalf("args_json should parse as object: %v", err)
	}
	if args["permalink"] != "decisions/0099-x" {
		t.Fatalf("args_json content not preserved: %+v", args)
	}
}

func TestParseExtract_RejectsMissingBMCommands(t *testing.T) {
	// bm_commands is required (OpenAI strict-mode invariant).
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"proposals": [],
		"next_action": "go"
	}`)
	if _, err := verdict.ParseExtract(raw); err == nil || !strings.Contains(err.Error(), "bm_commands is required") {
		t.Fatalf("want bm_commands-required error, got %v", err)
	}
}

func TestParseExtract_RejectsBMCommandsArgsJSONNonObject(t *testing.T) {
	// args_json mirrors prime's contract: must be a JSON object literal.
	cases := []struct {
		name, argsJSON string
	}{
		{"array", "[1,2,3]"},
		{"scalar", "42"},
		{"null", "null"},
		{"malformed", "{not json"},
		{"empty", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload := fmt.Sprintf(
				`{"verdict":"pass","findings":[],"proposals":[],"bm_commands":[{"tool":"write_note","args_json":%q}],"next_action":"go"}`,
				c.argsJSON,
			)
			if _, err := verdict.ParseExtract([]byte(payload)); err == nil || !strings.Contains(err.Error(), "args_json") {
				t.Fatalf("%s: want args_json error, got %v", c.name, err)
			}
		})
	}
}

func TestParseExtract_RejectsReviewerSpoofedServerFields(t *testing.T) {
	// summary_block and partial are server-owned. Non-OpenAI providers do
	// not enforce strict-mode at the wire level, so a malicious or confused
	// reviewer could try to emit them. The parser MUST reject these as
	// unknown fields (decoded into extractWire which has neither).
	cases := []struct{ name, field string }{
		{"summary_block", `"summary_block":"spoof"`},
		{"partial", `"partial":true`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := []byte(`{"verdict":"pass","findings":[],"proposals":[],"bm_commands":[],` + c.field + `,"next_action":"go"}`)
			if _, err := verdict.ParseExtract(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("want unknown-field error for spoofed %s, got %v", c.field, err)
			}
		})
	}
}

func TestParseExtract_SeverityFloorAppliedToConventionDeviation(t *testing.T) {
	// applySeverityFloor floors convention_deviation to minor regardless of
	// the reviewer's chosen severity. Confirm ParseExtract goes through it.
	raw := []byte(`{"verdict":"warn","findings":[{"severity":"major","category":"convention_deviation","criterion":"c","evidence":"e","suggestion":"s"}],"proposals":[],"bm_commands":[],"next_action":"x"}`)
	r, err := verdict.ParseExtract(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Findings[0].Severity != verdict.SeverityMinor {
		t.Fatalf("convention_deviation should floor to minor, got %q", r.Findings[0].Severity)
	}
}
