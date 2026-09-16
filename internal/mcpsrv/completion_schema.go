package mcpsrv

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// validateCompletionInputSchema is the schema inferred from
// ValidateCompletionArgs with the codescene object, and its verdicts, opened
// to unknown keys. Inference closes every struct with additionalProperties:
// false, and the SDK validates arguments against the schema before the
// handler runs, so a single unexpected key in this optional field would
// reject the whole call. Only additionalProperties is relaxed; which keys are
// required stays as inferred. codescene.Digest's UnmarshalJSON decides what
// the extra keys mean.
func validateCompletionInputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[ValidateCompletionArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("infer validate_completion input schema: %v", err))
	}
	cs, ok := s.Properties["codescene"]
	if !ok {
		panic("validate_completion input schema has no codescene property")
	}
	cs.AdditionalProperties = nil
	if v, ok := cs.Properties["verdicts"]; ok {
		v.AdditionalProperties = nil
	}
	return s
}
