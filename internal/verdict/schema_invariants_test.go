package verdict

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode asserts that for
// every JSON Schema object node (anywhere in the schema reachable via properties,
// items, or definitions) the required array contains exactly the same set of keys
// as the properties map. OpenAI structured-outputs (response_format strict:true)
// rejects any schema where a property is absent from required, returning HTTP 400.
//
// The test walks each of the four reviewer-output schemas embedded in this package
// and fails with a clear diagnostic naming the file and schema path on any mismatch.
func TestReviewerSchemas_RequireAllProperties_ForOpenAIStrictMode(t *testing.T) {
	schemas := []struct {
		name string
		raw  []byte
	}{
		{"schema.json", Schema()},
		{"plan_schema.json", PlanSchema()},
		{"tasks_only_schema.json", TasksOnlySchema()},
		{"plan_findings_only_schema.json", PlanFindingsOnlySchema()},
	}

	for _, s := range schemas {
		s := s // capture
		t.Run(s.name, func(t *testing.T) {
			var root map[string]any
			require.NoError(t, json.Unmarshal(s.raw, &root), "schema must be valid JSON")
			walkSchemaObject(t, s.name, root, "$")
		})
	}
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
