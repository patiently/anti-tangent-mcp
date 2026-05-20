package verdict_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

func TestParsePrime_Happy(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"picks": [
			{"permalink": "decisions/0042-x", "reason": "shaped recent caching", "priority": "major"},
			{"permalink": "modules/mcpsrv", "reason": "invariants apply", "priority": "minor"}
		],
		"bm_commands": [],
		"next_action": "attach picks and dispatch"
	}`)
	r, err := verdict.ParsePrime(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != verdict.VerdictPass || len(r.Picks) != 2 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if r.BMCommands == nil {
		t.Fatalf("BMCommands must be non-nil even when empty")
	}
}

func TestParsePrime_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"invalid verdict", `{"verdict":"oops","findings":[],"picks":[],"bm_commands":[],"next_action":"x"}`, "invalid verdict"},
		{"missing next_action", `{"verdict":"pass","findings":[],"picks":[],"bm_commands":[]}`, "next_action is required"},
		{"missing bm_commands", `{"verdict":"pass","findings":[],"picks":[],"next_action":"x"}`, "bm_commands is required"},
		{"missing findings", `{"verdict":"pass","picks":[],"bm_commands":[],"next_action":"x"}`, "findings is required"},
		{"missing picks", `{"verdict":"pass","findings":[],"bm_commands":[],"next_action":"x"}`, "picks is required"},
		{"empty criterion", `{"verdict":"warn","findings":[{"severity":"minor","category":"quality","criterion":"","evidence":"e","suggestion":"s"}],"picks":[],"bm_commands":[],"next_action":"x"}`, "criterion is required"},
		{"empty evidence", `{"verdict":"warn","findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"","suggestion":"s"}],"picks":[],"bm_commands":[],"next_action":"x"}`, "evidence is required"},
		{"empty suggestion", `{"verdict":"warn","findings":[{"severity":"minor","category":"quality","criterion":"c","evidence":"e","suggestion":""}],"picks":[],"bm_commands":[],"next_action":"x"}`, "suggestion is required"},
		{"invalid priority", `{"verdict":"pass","findings":[],"picks":[{"permalink":"p","reason":"r","priority":"huge"}],"bm_commands":[],"next_action":"x"}`, "invalid priority"},
		{"invalid category", `{"verdict":"warn","findings":[{"severity":"minor","category":"bogus","criterion":"c","evidence":"e","suggestion":"s"}],"picks":[],"bm_commands":[],"next_action":"x"}`, "invalid category"},
		{"extra fields rejected", `{"verdict":"pass","findings":[],"picks":[],"bm_commands":[],"next_action":"x","mystery":1}`, "unknown field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verdict.ParsePrime([]byte(c.raw))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v; want substring %q", err, c.want)
			}
		})
	}
}

func TestParsePrime_AcceptsNewCategories(t *testing.T) {
	for _, c := range []verdict.Category{
		verdict.CategoryKBGap,
		verdict.CategoryAmbiguousPick,
		verdict.CategoryMissingIndexEntry,
	} {
		raw := []byte(`{"verdict":"warn","findings":[{"severity":"minor","category":"` + string(c) + `","criterion":"c","evidence":"e","suggestion":"s"}],"picks":[],"bm_commands":[],"next_action":"x"}`)
		if _, err := verdict.ParsePrime(raw); err != nil {
			t.Fatalf("category %q: %v", c, err)
		}
	}
}

func TestParsePrime_StripsFences(t *testing.T) {
	raw := []byte("```json\n{\"verdict\":\"pass\",\"findings\":[],\"picks\":[],\"bm_commands\":[],\"next_action\":\"x\"}\n```")
	if _, err := verdict.ParsePrime(raw); err != nil {
		t.Fatalf("fenced parse: %v", err)
	}
}

func TestParsePrime_BMCommandsRoundTrip(t *testing.T) {
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"picks": [{"permalink": "decisions/0042-x", "reason": "r", "priority": "minor"}],
		"bm_commands": [{"tool": "read_note", "args_json": "{\"permalink\":\"decisions/0042-x\"}"}],
		"next_action": "go"
	}`)
	r, err := verdict.ParsePrime(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.BMCommands) != 1 || r.BMCommands[0].Tool != "read_note" {
		t.Fatalf("BMCommands not preserved: %+v", r.BMCommands)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(r.BMCommands[0].ArgsJSON), &args); err != nil {
		t.Fatalf("args_json should parse as object: %v", err)
	}
	if args["permalink"] != "decisions/0042-x" {
		t.Fatalf("args_json content not preserved: %+v", args)
	}
}

func TestParsePrime_RejectsArgsJSONNonObject(t *testing.T) {
	// args_json must be a JSON object literal — array, scalar, JSON null,
	// or malformed JSON is rejected with an actionable error. The `null`
	// case is load-bearing: json.Unmarshal of "null" into &m succeeds with
	// m == nil, which would silently slip past as "valid JSON object" if
	// the parser only checked for unmarshal errors.
	cases := []struct {
		name, argsJSON string
	}{
		{"array", "[1,2,3]"},
		{"scalar", "42"},
		{"null", "null"},
		{"malformed", "{not json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload := fmt.Sprintf(
				`{"verdict":"pass","findings":[],"picks":[{"permalink":"decisions/0042-x","reason":"r","priority":"minor"}],"bm_commands":[{"tool":"read_note","args_json":%q}],"next_action":"go"}`,
				c.argsJSON,
			)
			if _, err := verdict.ParsePrime([]byte(payload)); err == nil || !strings.Contains(err.Error(), "args_json") {
				t.Fatalf("%s: want args_json error, got %v", c.name, err)
			}
		})
	}
}

func TestParsePrime_RejectsReviewerSpoofedServerFields(t *testing.T) {
	// summary_block and partial are server-owned. Non-OpenAI providers do
	// not enforce strict-mode at the wire level, so a malicious or
	// confused reviewer could try to emit them. The parser MUST reject
	// these as unknown fields (decoded into primeWire which has neither).
	cases := []struct{ name, field string }{
		{"summary_block", `"summary_block":"spoof"`},
		{"partial", `"partial":true`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := []byte(`{"verdict":"pass","findings":[],"picks":[],"bm_commands":[],` + c.field + `,"next_action":"go"}`)
			if _, err := verdict.ParsePrime(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("want unknown-field error for spoofed %s, got %v", c.field, err)
			}
		})
	}
}

func TestParsePrime_RejectsMissingBMCommands(t *testing.T) {
	// bm_commands is required (OpenAI strict-mode invariant). Missing the
	// field must fail even though every other required field is present.
	raw := []byte(`{
		"verdict": "pass",
		"findings": [],
		"picks": [],
		"next_action": "go"
	}`)
	if _, err := verdict.ParsePrime(raw); err == nil || !strings.Contains(err.Error(), "bm_commands is required") {
		t.Fatalf("want bm_commands-required error, got %v", err)
	}
}
