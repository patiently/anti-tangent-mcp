package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/providers"
	"github.com/patiently/anti-tangent-mcp/internal/session"
)

// toolInputSchemas lists every tool through a real in-memory MCP client and
// returns each input schema decoded as plain JSON, keyed by tool name.
func toolInputSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	})
	require.NoError(t, err)
	srv := New(Deps{Cfg: cfg, Reviews: providers.Registry{"anthropic": &fakeReviewer{name: "anthropic"}}})

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	out := map[string]map[string]any{}
	for _, tool := range res.Tools {
		b, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(b, &schema), tool.Name)
		out[tool.Name] = schema
	}
	return out
}

// propertyDescriptions walks object properties and array items, recording each
// property's description under a dotted path; array items add "[]".
func propertyDescriptions(schema map[string]any, path string, into map[string]string) {
	if props, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range props {
			prop, _ := raw.(map[string]any)
			p := path + "." + name
			desc, _ := prop["description"].(string)
			into[p] = desc
			propertyDescriptions(prop, p, into)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		propertyDescriptions(items, path+"[]", into)
	}
}

func allPropertyDescriptions(t *testing.T) map[string]string {
	t.Helper()
	descs := map[string]string{}
	for name, schema := range toolInputSchemas(t) {
		propertyDescriptions(schema, name, descs)
	}
	return descs
}

// schemaIndirections lists every $ref, $defs, definitions, allOf, anyOf or oneOf
// node under node. propertyDescriptions and requiredSets follow only properties
// and items, which covers a schema completely only while it has none of these.
func schemaIndirections(node any, path string, into *[]string) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "$ref", "$defs", "definitions", "allOf", "anyOf", "oneOf":
				*into = append(*into, path+"."+key)
			}
			schemaIndirections(child, path+"."+key, into)
		}
	case []any:
		for i, child := range v {
			schemaIndirections(child, fmt.Sprintf("%s[%d]", path, i), into)
		}
	}
}

func TestToolInputSchemas_ToolSetAndShape(t *testing.T) {
	schemas := toolInputSchemas(t)
	names := make([]string, 0, len(schemas))
	var indirections []string
	for name, schema := range schemas {
		names = append(names, name)
		schemaIndirections(schema, name, &indirections)
	}
	sort.Strings(names)
	assert.Equal(t, []string{
		"bulk_read", "check_progress", "code_write", "extract_project_knowledge", "plan_run_report",
		"prime_project_knowledge", "validate_completion", "validate_plan", "validate_task_spec",
	}, names)
	sort.Strings(indirections)
	assert.Empty(t, indirections, "the contract walkers do not follow these nodes; extend them before trusting the other schema tests")
}

func TestToolInputSchemas_EveryPropertyDescribed(t *testing.T) {
	descs := allPropertyDescriptions(t)
	var missing []string
	for path, desc := range descs {
		if strings.TrimSpace(desc) == "" || desc == "required" {
			missing = append(missing, fmt.Sprintf("%s (%q)", path, desc))
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "input properties without a real description")
}

// requiredSets records each object's sorted required property names under the
// same dotted path propertyDescriptions uses.
func requiredSets(schema map[string]any, path string, into map[string][]string) {
	if req, ok := schema["required"].([]any); ok && len(req) > 0 {
		names := make([]string, 0, len(req))
		for _, r := range req {
			names = append(names, fmt.Sprint(r))
		}
		sort.Strings(names)
		into[path] = names
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range props {
			prop, _ := raw.(map[string]any)
			requiredSets(prop, path+"."+name, into)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		requiredSets(items, path+"[]", into)
	}
}

// TestToolInputSchemas_RequiredSetsUnchanged pins every object's required set.
// Descriptions live in jsonschema tags and cannot change required-ness, but a
// json tag edited alongside one can; update this table only for an intended
// change to a tool's contract.
func TestToolInputSchemas_RequiredSetsUnchanged(t *testing.T) {
	want := map[string][]string{
		"bulk_read":                      {"paths", "question"},
		"check_progress":                 {"session_id", "working_on"},
		"check_progress.changed_files[]": {"content", "path"},
		"code_write":                     {"reference_path", "spec"},
		"extract_project_knowledge":      {"completion_envelopes"},
		"extract_project_knowledge.completion_envelopes[]":               {"summary", "verdict"},
		"extract_project_knowledge.completion_envelopes[].final_files[]": {"content", "path"},
		"extract_project_knowledge.completion_envelopes[].findings[]":    {"category", "criterion", "evidence", "severity", "suggestion"},
		"extract_project_knowledge.kb_index[]":                           {"permalink", "summary", "title", "type"},
		"plan_run_report":                                                {"plan_run_id"},
		"prime_project_knowledge":                                        {"acceptance_criteria", "goal", "task_title"},
		"prime_project_knowledge.kb_index[]":                             {"permalink", "summary", "title", "type"},
		"validate_completion":                                            {"session_id", "summary"},
		"validate_completion.codescene.verdicts":                         {"degraded", "improved", "stable"},
		"validate_completion.controller_rulings[]":                       {"finding_id", "ruling"},
		"validate_completion.final_files[]":                              {"path"},
		"validate_completion.finding_responses[]":                        {"finding_id", "response"},
		"validate_plan.controller_rulings[]":                             {"finding_id", "ruling"},
		"validate_task_spec":                                             {"goal", "task_title"},
		"validate_task_spec.harness_shape_attestation[]":                 {"assertions", "harness", "path"},
	}
	got := map[string][]string{}
	for name, schema := range toolInputSchemas(t) {
		requiredSets(schema, name, got)
	}
	assert.Equal(t, want, got)
}

func TestToolInputSchemas_StatedLimitsMatchConstants(t *testing.T) {
	descs := allPropertyDescriptions(t)
	n := strconv.Itoa
	bounded := []string{n(maxPinnedByEntries), n(maxPinnedByChars)}
	verification := []string{n(maxPinnedByEntries), n(maxVerificationChars)}
	verifiedRefs := []string{n(maxVerifiedReferenceEntries), n(maxPinnedByChars)}
	payload := []string{n(config.DefaultMaxPayloadBytes), "ANTI_TANGENT_MAX_PAYLOAD_BYTES"}
	planPayload := []string{n(config.DefaultPlanMaxPayloadBytes), "ANTI_TANGENT_PLAN_MAX_PAYLOAD_BYTES"}
	cases := map[string][]string{
		"validate_task_spec.pinned_by":                              bounded,
		"validate_task_spec.verification":                           verification,
		"validate_task_spec.controller_verified_references":         verifiedRefs,
		"validate_task_spec.test_strategy_notes":                    bounded,
		"validate_task_spec.codebase_conventions":                   bounded,
		"validate_task_spec.testability_extractions":                bounded,
		"validate_task_spec.normative_test_bodies":                  {n(maxNormativeTestBodyEntries), n(maxNormativeTestBodyChars)},
		"validate_task_spec.harness_shape_attestation":              {n(maxHarnessShapeAttestationEntries)},
		"validate_task_spec.harness_shape_attestation[].harness":    {n(maxHarnessShapeAttestationHarnessChars)},
		"validate_task_spec.harness_shape_attestation[].path":       {n(maxHarnessShapeAttestationPathChars)},
		"validate_task_spec.harness_shape_attestation[].assertions": {n(maxHarnessShapeAttestationAssertions), n(maxHarnessShapeAttestationAssertionChars)},
		"validate_task_spec.project_knowledge":                      payload,
		"validate_completion.exit_contracts":                        bounded,
		"validate_completion.final_diff":                            payload,
		"validate_completion.final_files":                           payload,
		"check_progress.changed_files":                              payload,
		"bulk_read.paths":                                           append([]string{n(maxBulkReadPaths)}, payload...),
		"validate_plan.context_paths":                               {n(maxContextFiles)},
		"validate_task_spec.context_paths":                          {n(maxContextFiles)},
		"prime_project_knowledge.max_picks":                         {n(defaultMaxPicks), n(maxMaxPicks)},
		"validate_completion.final_diff_path":                       {"ANTI_TANGENT_PLAN_ROOTS"},
		"validate_completion.repo_root":                             {"ANTI_TANGENT_PLAN_ROOTS"},
		"validate_plan.plan_text":                                   planPayload,
		"validate_plan.plan_path":                                   append([]string{"ANTI_TANGENT_PLAN_ROOTS"}, planPayload...),
		"validate_plan.project_knowledge":                           planPayload,
		"validate_completion.codescene":                             {"analyze_change_set", "pre_commit_code_health_safeguard"},
		"validate_completion.finding_responses":                     {n(maxFindingResponseEntries), n(maxFindingResponseChars)},
		"validate_completion.finding_responses[].response":          {n(maxFindingResponseChars)},
		"validate_completion.controller_rulings":                    {n(maxControllerRulingEntries), n(maxControllerRulingChars), n(session.MaxRulings)},
		"validate_completion.controller_rulings[].ruling":           {n(maxControllerRulingChars)},
		"validate_plan.controller_rulings":                          {n(maxControllerRulingEntries), n(maxControllerRulingChars)},
		"validate_plan.controller_rulings[].ruling":                 {n(maxControllerRulingChars)},
		"validate_plan.controller_verified_references":              verifiedRefs,
	}
	for path, wants := range cases {
		desc, ok := descs[path]
		if !assert.True(t, ok, "no property %s", path) {
			continue
		}
		for _, want := range wants {
			re := regexp.MustCompile(`(^|[^0-9A-Za-z_])` + regexp.QuoteMeta(want) + `($|[^0-9A-Za-z_])`)
			assert.Regexp(t, re, desc, "%s must state %s", path, want)
		}
	}
}
