package verdict

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// reviewerSchema names one of the embedded reviewer-output JSON schemas
// in this package. v0.6.0 Task 5 added prime_schema.json; Task 8 added
// extract_schema.json. The slice now covers all six reviewer-output
// schemas in lock-step.
type reviewerSchema struct {
	name string
	raw  []byte
}

// reviewerSchemas returns the schemas the strict-mode invariants walk.
// Task 5 (v0.6.0) added prime_schema.json; Task 8 added extract_schema.json,
// bringing the total to six reviewer-output schemas walked by the invariants.
//
// NOTE on OpenAI strict-mode keyword support (verified 2026-05-20 via the
// platform docs at https://platform.openai.com/docs/guides/structured-outputs):
// `additionalProperties` (must be false), `required`, `properties`, `type`,
// `enum`, `items`, `$ref`, `oneOf`/`anyOf`/`allOf`, `description`,
// `minLength`/`maxLength`, `minimum`/`maximum`, `minItems`/`maxItems`,
// `pattern`. These schemas use the subset (`required`, `properties`,
// `additionalProperties: false`, `type`, `enum`, `items`, `$ref`,
// `minLength`, `minimum`). Parser-side `validateFindingStrings` (parser.go)
// is the durable enforcement of non-empty criterion/evidence/suggestion
// regardless of any future schema-keyword decision.
func reviewerSchemas() []reviewerSchema {
	return []reviewerSchema{
		{"schema.json", Schema()},
		{"plan_schema.json", PlanSchema()},
		{"tasks_only_schema.json", TasksOnlySchema()},
		{"plan_findings_only_schema.json", PlanFindingsOnlySchema()},
		{"prime_schema.json", PrimeSchema()},
		{"extract_schema.json", ExtractSchema()},
	}
}

// TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode asserts that for
// every JSON Schema object node (anywhere in the schema reachable via properties,
// items, or definitions) the required array contains exactly the same set of keys
// as the properties map. OpenAI structured-outputs (response_format strict:true)
// rejects any schema where a property is absent from required, returning HTTP 400.
//
// The test walks each of the six reviewer-output schemas embedded in this package
// and fails with a clear diagnostic naming the file and schema path on any mismatch.
func TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode(t *testing.T) {
	for _, s := range reviewerSchemas() {
		t.Run(s.name, func(t *testing.T) {
			var root map[string]any
			require.NoError(t, json.Unmarshal(s.raw, &root), "schema must be valid JSON")
			walkSchemaObject(t, s.name, root, "$")
		})
	}
}

// TestReviewerSchemas_NoFreeformObject_ForOpenAIStrictMode asserts that no
// object-typed node anywhere in a reviewer-output schema is "freeform" —
// declared as {"type": "object"} with no properties (or empty properties).
// OpenAI strict-mode rejects bare object schemas because there is no
// enumerated property set against which to apply additionalProperties: false.
func TestReviewerSchemas_NoFreeformObject_ForOpenAIStrictMode(t *testing.T) {
	for _, s := range reviewerSchemas() {
		t.Run(s.name, func(t *testing.T) {
			var root map[string]any
			require.NoError(t, json.Unmarshal(s.raw, &root), "schema must be valid JSON")
			walkSchemaNoFreeform(t, s.name, root, "$")
		})
	}
}

// TestReviewerSchemas_AdditionalPropertiesFalse_ForOpenAIStrictMode asserts
// that every object-typed node has "additionalProperties": false. Without it
// OpenAI returns HTTP 400 at request time. The walk follows the same
// recursion as the required-vs-properties invariant.
func TestReviewerSchemas_AdditionalPropertiesFalse_ForOpenAIStrictMode(t *testing.T) {
	for _, s := range reviewerSchemas() {
		t.Run(s.name, func(t *testing.T) {
			var root map[string]any
			require.NoError(t, json.Unmarshal(s.raw, &root), "schema must be valid JSON")
			walkSchemaAdditionalPropertiesFalse(t, s.name, root, "$")
		})
	}
}

// TestReviewerSchemas_CategoryEnumsAreInLockstep asserts that every
// `properties.category.enum` array reachable in each reviewer-output schema
// matches the same canonical set, with an exception: plan-level schemas
// (plan_schema.json, plan_findings_only_schema.json, tasks_only_schema.json)
// may have additional validate_plan-specific categories that do not appear
// in the base schemas (schema.json, prime_schema.json, extract_schema.json).
// v0.6.0 adds six new categories alongside the existing v0.5.x set; this
// test ensures they propagate to ALL schemas correctly, while allowing
// intentional schema-specific divergence for plan-level findings. The walker
// resolves no $ref — it descends into `definitions` directly, mirroring the
// required-vs-properties walker.
func TestReviewerSchemas_CategoryEnumsAreInLockstep(t *testing.T) {
	schemas := reviewerSchemas()

	// Collect category enums from all schemas.
	type schemaEnums struct {
		name  string
		enums [][]string
	}
	var allSchemas []schemaEnums
	for _, s := range schemas {
		var root map[string]any
		require.NoError(t, json.Unmarshal(s.raw, &root), "schema must be valid JSON")
		enums := collectCategoryEnums(root)
		require.NotEmpty(t, enums, "%s: no properties.category.enum found", s.name)
		allSchemas = append(allSchemas, schemaEnums{name: s.name, enums: enums})
	}

	// Separate plan schemas from non-plan schemas.
	planSchemaNames := map[string]bool{
		"plan_schema.json":               true,
		"plan_findings_only_schema.json": true,
		"tasks_only_schema.json":         true,
	}
	var planSchemas, nonPlanSchemas []schemaEnums
	for _, s := range allSchemas {
		if planSchemaNames[s.name] {
			planSchemas = append(planSchemas, s)
		} else {
			nonPlanSchemas = append(nonPlanSchemas, s)
		}
	}

	// Build canonical set from non-plan schemas (baseline for all schemas).
	var canonical []string
	for _, s := range nonPlanSchemas {
		for _, enum := range s.enums {
			sorted := append([]string(nil), enum...)
			sort.Strings(sorted)
			if canonical == nil {
				canonical = sorted
				continue
			}
			if !stringSlicesEqual(canonical, sorted) {
				t.Errorf("%s (non-plan): category enum diverges from canonical set\n  canonical: %v\n  this file: %v", s.name, canonical, sorted)
			}
		}
	}

	// Plan schemas must include the canonical set, but may have additional categories.
	for _, s := range planSchemas {
		for _, enum := range s.enums {
			sorted := append([]string(nil), enum...)
			sort.Strings(sorted)
			// Check that canonical is a subset of sorted.
			for _, c := range canonical {
				found := false
				for _, e := range sorted {
					if e == c {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s (plan): missing canonical category %q\n  canonical: %v\n  this file: %v", s.name, c, canonical, sorted)
				}
			}
		}
	}

	// The plan-only extras must be EXACTLY the documented set. "Superset of
	// canonical" alone would let any new category be added to a plan schema
	// and nowhere else — including by accident, or as a typo of a category
	// that was meant to be shared — and this test would still pass. The
	// divergence is intentional but it is a closed list, so pin it.
	//
	// contradicted_codebase_claim is validate_plan-only because it depends on
	// context_paths attachments, which only validate_plan takes.
	wantPlanOnly := []string{"contradicted_codebase_claim"}

	// Plan schemas must have consistent category enums among themselves.
	var planCanonical []string
	for _, s := range planSchemas {
		for _, enum := range s.enums {
			sorted := append([]string(nil), enum...)
			sort.Strings(sorted)
			if planCanonical == nil {
				planCanonical = sorted
				continue
			}
			if !stringSlicesEqual(planCanonical, sorted) {
				t.Errorf("%s (plan): category enum diverges from other plan schemas\n  plan canonical: %v\n  this file: %v", s.name, planCanonical, sorted)
			}
		}
	}

	inCanonical := make(map[string]bool, len(canonical))
	for _, c := range canonical {
		inCanonical[c] = true
	}
	var planOnly []string
	for _, e := range planCanonical {
		if !inCanonical[e] {
			planOnly = append(planOnly, e)
		}
	}
	sort.Strings(planOnly)
	if !stringSlicesEqual(wantPlanOnly, planOnly) {
		t.Errorf("plan-only category extras changed\n  want: %v\n  got:  %v\nAdding a plan-only category is a deliberate act: update wantPlanOnly (and the protocol docs) in the same change.", wantPlanOnly, planOnly)
	}
}

func walkSchemaNoFreeform(t *testing.T, file string, node map[string]any, path string) {
	t.Helper()
	for _, defsKey := range []string{"definitions", "$defs"} {
		if defs, ok := node[defsKey].(map[string]any); ok {
			for name, child := range defs {
				if childObj, ok := child.(map[string]any); ok {
					walkSchemaNoFreeform(t, file, childObj, fmt.Sprintf("%s/%s/%s", path, defsKey, name))
				}
			}
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for propName, propVal := range props {
			if propObj, ok := propVal.(map[string]any); ok {
				walkSchemaNoFreeform(t, file, propObj, fmt.Sprintf("%s/properties/%s", path, propName))
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		walkSchemaNoFreeform(t, file, items, path+"/items")
	}
	if typeVal, _ := node["type"].(string); typeVal == "object" {
		props, _ := node["properties"].(map[string]any)
		if len(props) == 0 {
			t.Errorf("%s: %s: freeform {\"type\": \"object\"} without properties — OpenAI strict mode rejects bare object schemas (use additionalProperties: false with enumerated properties, or shape as a JSON-encoded string)", file, path)
		}
	}
}

func walkSchemaAdditionalPropertiesFalse(t *testing.T, file string, node map[string]any, path string) {
	t.Helper()
	for _, defsKey := range []string{"definitions", "$defs"} {
		if defs, ok := node[defsKey].(map[string]any); ok {
			for name, child := range defs {
				if childObj, ok := child.(map[string]any); ok {
					walkSchemaAdditionalPropertiesFalse(t, file, childObj, fmt.Sprintf("%s/%s/%s", path, defsKey, name))
				}
			}
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for propName, propVal := range props {
			if propObj, ok := propVal.(map[string]any); ok {
				walkSchemaAdditionalPropertiesFalse(t, file, propObj, fmt.Sprintf("%s/properties/%s", path, propName))
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		walkSchemaAdditionalPropertiesFalse(t, file, items, path+"/items")
	}
	typeVal, _ := node["type"].(string)
	_, hasProps := node["properties"].(map[string]any)
	if typeVal != "object" && !hasProps {
		return
	}
	addProps, present := node["additionalProperties"]
	if !present {
		t.Errorf("%s: %s: missing additionalProperties — OpenAI strict mode requires every object schema to set additionalProperties: false", file, path)
		return
	}
	if v, ok := addProps.(bool); !ok || v {
		t.Errorf("%s: %s: additionalProperties must be false (got %v) — OpenAI strict mode rejects any other value", file, path, addProps)
	}
}

// collectCategoryEnums walks node and returns every `properties.category.enum`
// array it finds (as []string). The walker descends through `properties`,
// `items`, `definitions`, and `$defs`; it does NOT resolve `$ref` (each
// schema either inlines the finding shape or declares the enum inside its
// own definitions block, so direct descent suffices).
func collectCategoryEnums(node map[string]any) [][]string {
	var out [][]string
	for _, defsKey := range []string{"definitions", "$defs"} {
		if defs, ok := node[defsKey].(map[string]any); ok {
			for _, child := range defs {
				if childObj, ok := child.(map[string]any); ok {
					out = append(out, collectCategoryEnums(childObj)...)
				}
			}
		}
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for propName, propVal := range props {
			propObj, ok := propVal.(map[string]any)
			if !ok {
				continue
			}
			if propName == "category" {
				if enumRaw, ok := propObj["enum"].([]any); ok {
					var enum []string
					for _, v := range enumRaw {
						if s, ok := v.(string); ok {
							enum = append(enum, s)
						}
					}
					if len(enum) > 0 {
						out = append(out, enum)
					}
				}
			}
			out = append(out, collectCategoryEnums(propObj)...)
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		out = append(out, collectCategoryEnums(items)...)
	}
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// walkSchemaObject recursively visits every object-typed subschema reachable
// from node via properties, items, and definitions (/$defs and /definitions).
// At each object-typed node (identified by type=="object" or a non-empty
// properties map) it asserts that required contains every key in properties
// and properties contains every key in required.
func walkSchemaObject(t *testing.T, file string, node map[string]any, path string) {
	t.Helper()

	// Recurse into definitions / $defs first (unordered, but visit all).
	for _, defsKey := range []string{"definitions", "$defs"} {
		if defs, ok := node[defsKey].(map[string]any); ok {
			for name, child := range defs {
				if childObj, ok := child.(map[string]any); ok {
					walkSchemaObject(t, file, childObj, fmt.Sprintf("%s/%s/%s", path, defsKey, name))
				}
			}
		}
	}

	// Recurse into properties values.
	if props, ok := node["properties"].(map[string]any); ok {
		for propName, propVal := range props {
			if propObj, ok := propVal.(map[string]any); ok {
				walkSchemaObject(t, file, propObj, fmt.Sprintf("%s/properties/%s", path, propName))
			}
		}
	}

	// Recurse into items (array items schema).
	if items, ok := node["items"].(map[string]any); ok {
		walkSchemaObject(t, file, items, path+"/items")
	}

	// Check this node if it is object-typed.
	typeVal, _ := node["type"].(string)
	propsMap, hasProps := node["properties"].(map[string]any)
	if typeVal != "object" && !hasProps {
		return
	}

	// Build the set of property keys.
	propKeys := make(map[string]bool, len(propsMap))
	for k := range propsMap {
		propKeys[k] = true
	}

	// Build the set of required keys.
	requiredKeys := make(map[string]bool)
	if reqRaw, ok := node["required"].([]any); ok {
		for _, v := range reqRaw {
			if s, ok := v.(string); ok {
				requiredKeys[s] = true
			}
		}
	}

	// Compute diffs.
	var inPropsNotRequired []string
	for k := range propKeys {
		if !requiredKeys[k] {
			inPropsNotRequired = append(inPropsNotRequired, k)
		}
	}
	var inRequiredNotProps []string
	for k := range requiredKeys {
		if !propKeys[k] {
			inRequiredNotProps = append(inRequiredNotProps, k)
		}
	}

	sort.Strings(inPropsNotRequired)
	sort.Strings(inRequiredNotProps)

	var msgs []string
	if len(inPropsNotRequired) > 0 {
		msgs = append(msgs, fmt.Sprintf("properties not in required: %v", inPropsNotRequired))
	}
	if len(inRequiredNotProps) > 0 {
		msgs = append(msgs, fmt.Sprintf("required entries not in properties: %v", inRequiredNotProps))
	}
	if len(msgs) > 0 {
		t.Errorf("%s: %s: %s", file, path, strings.Join(msgs, "; "))
	}
}
