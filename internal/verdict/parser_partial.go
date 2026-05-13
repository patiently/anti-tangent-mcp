package verdict

import (
	"bytes"
	"encoding/json"
	"math"
)

// applySeverityFloor enforces the category-based severity floors that
// match the strict parser's behavior, so partial-recovery output is
// consistent with strict output. Currently the only floor is
// unverifiable_codebase_claim → minor (the reviewer can't know if the
// claim is wrong, only that it can't check).
func applySeverityFloor(f Finding) Finding {
	if f.Category == CategoryUnverifiableCodebaseClaim && f.Severity != SeverityMinor {
		f.Severity = SeverityMinor
	}
	return f
}

// applySeverityFloorAll applies applySeverityFloor in-place to every
// element of fs.
func applySeverityFloorAll(fs []Finding) {
	for i := range fs {
		fs[i] = applySeverityFloor(fs[i])
	}
}

// ParseResultPartial parses a possibly-truncated reviewer response into a
// Result. It first attempts a strict json.Unmarshal; on failure, it walks
// the raw bytes to recover any complete Finding objects inside the
// findings[] array that appeared before the truncation point.
//
// Returns (result, true) when strict parse succeeds, OR when partial
// recovery yields at least one complete finding (in which case
// Result.Partial is set to true). Returns (zero, false) when no complete
// finding could be recovered — including the case where truncation hit
// inside a JSON string literal before any element closed, since no safe
// boundary was ever observed.
func ParseResultPartial(raw []byte) (Result, bool) {
	// Strict parse first — most calls aren't truncated.
	trimmed := bytes.TrimSpace(raw)
	var r Result
	if err := json.Unmarshal(trimmed, &r); err == nil {
		applySeverityFloorAll(r.Findings)
		return r, true
	}

	// Locate the findings array and recover complete elements.
	repaired, ok := repairOuterArray(raw, "findings")
	if !ok {
		return Result{}, false
	}
	r = Result{}
	if err := json.Unmarshal(repaired, &r); err != nil {
		return Result{}, false
	}
	if len(r.Findings) == 0 {
		return Result{}, false
	}
	applySeverityFloorAll(r.Findings)
	r.Partial = true
	return r, true
}

// ParsePlanResultPartial parses a possibly-truncated plan-level reviewer
// response. Strict parse first; on failure, recover complete elements
// from plan_findings[] and tasks[] (including synthetic recovery of a
// truncated trailing task when it has a parseable task_title and at
// least one complete finding). Test fixtures supersede AC text per plan
// Step 5: a recovered complete task counts as a recovery unit on its
// own, so two cleanly-closed empty-findings tasks are sufficient.
func ParsePlanResultPartial(raw []byte) (PlanResult, bool) {
	trimmed := bytes.TrimSpace(raw)
	var pr PlanResult
	if err := json.Unmarshal(trimmed, &pr); err == nil {
		applyPlanSeverityFloor(&pr)
		return pr, true
	}

	pr, ok := recoverPlanResult(raw)
	if !ok {
		return PlanResult{}, false
	}
	if len(pr.Tasks) == 0 && len(pr.PlanFindings) == 0 {
		return PlanResult{}, false
	}
	applyPlanSeverityFloor(&pr)
	pr.Partial = true
	return pr, true
}

// applyPlanSeverityFloor walks every findings slice inside pr and applies
// the per-category severity floor, so partial-recovery output is
// consistent with the strict ParsePlan path.
func applyPlanSeverityFloor(pr *PlanResult) {
	applySeverityFloorAll(pr.PlanFindings)
	for i := range pr.Tasks {
		applySeverityFloorAll(pr.Tasks[i].Findings)
	}
}

// repairOuterArray finds `"<key>":[ ... ` at depth 1 of the outer
// object, walks the array tracking JSON depth and string state, then
// returns a repaired byte slice that includes raw up to the last
// complete element boundary, followed by `]}`. Returns (nil, false)
// when the key cannot be located or no element boundary was observed.
// Test fixtures supersede AC text per plan Step 5: EOF inside a string
// literal is OK if an earlier element already closed cleanly (the
// earlier boundary remains a safe truncation point).
func repairOuterArray(raw []byte, key string) ([]byte, bool) {
	// Find `"<key>":` at depth 1 of the outermost object, then the `[`.
	arrStart, ok := findArrayStartForKey(raw, key)
	if !ok {
		return nil, false
	}
	// arrStart points to the byte immediately after the `[`.

	// Walk inside the array. Track depth relative to the array interior:
	// depth==0 means we are at the array's element level (i.e. just inside
	// the `[`). Each unescaped `{` or `[` increments depth; `}` or `]`
	// decrements. A boundary (end of a complete element) is recognized
	// when depth returns to 0 after having been >0, or when we see a `,`
	// at depth 0. We also recognize a `]` at depth 0 as the array close,
	// in which case the array is complete (no partial recovery needed for
	// this array).
	depth := 0
	inString := false
	escape := false
	// lastBoundary is the byte index (into raw) of the position just
	// after the last complete element ended. We close `]}` after this.
	// Initialize to arrStart so that an array with zero elements (which
	// would only happen on truncation immediately after `[`) yields an
	// empty findings list — caller decides if that counts as recovery.
	lastBoundary := arrStart
	sawAnyElement := false
	arrayClosed := false
	arrayCloseIdx := -1

	i := arrStart
	for ; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				// At array's element level, `]` closes the findings
				// array. `}` shouldn't happen here (would mean the outer
				// object closed before the array — malformed).
				if c == ']' {
					arrayClosed = true
					arrayCloseIdx = i
					// Boundary is just before this `]` (no trailing
					// comma after the last element).
					lastBoundary = i
				}
				goto done
			}
			depth--
			if depth == 0 {
				// Just finished an element (object/array). Mark the
				// position immediately after this byte as a boundary.
				lastBoundary = i + 1
				sawAnyElement = true
			}
		case ',':
			if depth == 0 {
				// Element separator at array level. Boundary is the
				// position of this comma (we'll truncate before it).
				lastBoundary = i
				sawAnyElement = true
			}
		}
	}
done:
	// EOF / array-close handling. EOF inside a string literal is OK so
	// long as an earlier `lastBoundary` was observed; that boundary
	// predates the in-progress element's open quote and is still a safe
	// truncation point. With no prior boundary, sawAnyElement stays
	// false and we fall through to the "no element" return.

	if arrayClosed {
		// The array closed cleanly. Recovery is still useful for cases
		// where truncation happened AFTER the array but before the
		// outer object closed. Build a repaired slice that includes raw
		// up to and including the array-closer `]`, then append `}`.
		repaired := make([]byte, 0, arrayCloseIdx+2)
		repaired = append(repaired, raw[:arrayCloseIdx+1]...)
		repaired = append(repaired, '}')
		return repaired, true
	}

	// Array did not close. We need at least one complete element to
	// have been observed; otherwise there is nothing to recover.
	if !sawAnyElement {
		return nil, false
	}

	// Build repaired bytes: raw[:lastBoundary] + "]}".
	// lastBoundary points either to the byte just after a complete
	// element (depth-back-to-zero case) or to the index of a `,`
	// (separator case). In both cases truncating there leaves a
	// well-formed array prefix.
	repaired := make([]byte, 0, lastBoundary+2)
	repaired = append(repaired, raw[:lastBoundary]...)
	repaired = append(repaired, ']', '}')
	return repaired, true
}

// findArrayStartForKey locates `"<key>":[` at depth 1 of the outer
// object in raw. Returns the byte index immediately after the `[`,
// and ok=true on success.
//
// The walker tracks string state (with backslash escapes) so that
// `"<key>"` matches only as a real JSON key. It accepts whitespace
// between the key, the colon, and the `[`.
func findArrayStartForKey(raw []byte, key string) (int, bool) {
	// Match: optional whitespace, '"', key bytes, '"', optional ws,
	// ':', optional ws, '['.
	target := []byte(`"` + key + `"`)

	depth := 0 // bracket/brace depth relative to start of raw
	inString := false
	escape := false

	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			// Potential key start. Check if at depth 1 (just inside the
			// outermost object) and the bytes match target exactly.
			if depth == 1 && i+len(target) <= len(raw) && bytes.Equal(raw[i:i+len(target)], target) {
				// Skip past the key string.
				j := i + len(target)
				// Consume optional whitespace.
				for j < len(raw) && isJSONWhitespace(raw[j]) {
					j++
				}
				if j >= len(raw) || raw[j] != ':' {
					// Not a key — could be a string value with the
					// same content. Continue scanning past the
					// string literal that starts at i.
					inString = true
					continue
				}
				j++ // past ':'
				for j < len(raw) && isJSONWhitespace(raw[j]) {
					j++
				}
				if j >= len(raw) || raw[j] != '[' {
					// Not an array value — give up the search here.
					return 0, false
				}
				return j + 1, true
			}
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return 0, false
			}
			depth--
		}
	}
	return 0, false
}

func isJSONWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// recoverPlanResult walks raw and builds a best-effort PlanResult by
// recovering complete elements from plan_findings[] and tasks[]. It
// returns ok=false only when truncation hit inside a string literal
// (recovery unsafe) or when neither array could be located.
//
// The returned PlanResult does NOT have Partial set — the caller
// (ParsePlanResultPartial) handles that, along with the empty-recovery
// (zero, false) decision.
func recoverPlanResult(raw []byte) (PlanResult, bool) {
	var pr PlanResult

	// Recover plan_findings[] via the same outer-array walker. The key
	// may not be present, or may be empty; either case is fine.
	if repaired, ok := repairOuterArray(raw, "plan_findings"); ok {
		// repaired wraps a synthetic {"<key>":[...]}; unmarshal into a
		// minimal shape to extract the findings slice.
		var tmp struct {
			PlanFindings []Finding `json:"plan_findings"`
		}
		if err := json.Unmarshal(repaired, &tmp); err == nil {
			pr.PlanFindings = tmp.PlanFindings
		}
	}

	// Recover tasks[]. Locate the start of the tasks array. If the
	// array closed cleanly we can use repairOuterArray. Otherwise we
	// have to walk it ourselves to identify the trailing partial task
	// and attempt synthetic recovery.
	arrStart, ok := findArrayStartForKey(raw, "tasks")
	if !ok {
		// No tasks array at all. plan_findings may still be present;
		// caller decides whether that's enough recovery.
		return pr, true
	}

	pr.Tasks = walkTasksArray(raw, arrStart)
	return pr, true
}

// walkTasksArray walks raw starting at arrStart (the byte after `[` of
// the tasks array) and returns the recovered tasks slice. It collects
// every task that closes cleanly inside the array, and — if the array
// itself is truncated mid-task — attempts synthetic recovery of the
// last (partial) task.
func walkTasksArray(raw []byte, arrStart int) []PlanTaskResult {
	var tasks []PlanTaskResult
	// Track the start index of the currently-open task element, or -1
	// when between elements (i.e. between `[`/`,` and the next `{`).
	elemStart := -1
	depth := 0 // depth relative to array interior; 0 == element-level
	inString := false
	escape := false

	for i := arrStart; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{', '[':
			if depth == 0 && c == '{' && elemStart == -1 {
				elemStart = i
			}
			depth++
		case '}', ']':
			if depth == 0 {
				// `]` closes the tasks array (a `}` here would be
				// malformed top-level — bail). Either way we're done
				// walking; closed tasks captured so far are returned.
				return tasks
			}
			depth--
			if depth == 0 && elemStart != -1 {
				// Just finished an element. Unmarshal it strictly.
				elementBytes := raw[elemStart : i+1]
				var t PlanTaskResult
				if err := json.Unmarshal(elementBytes, &t); err == nil {
					tasks = append(tasks, t)
				}
				// If unmarshal fails on a syntactically balanced
				// element (rare, e.g. enum violations), we drop it —
				// the strict parser would have rejected the whole
				// document, and we can't safely synthesize from a
				// closed-but-invalid element.
				elemStart = -1
			}
		}
	}

	// EOF reached without a closing `]` for tasks[].
	//
	// Note: encountering EOF inside a string literal does NOT
	// invalidate the tasks we already recovered — by construction,
	// every completed task we appended had its closing `}` observed
	// outside a string. The in-progress (partial) task may not be
	// synthesizable (buildSyntheticTask handles that by returning ok
	// =false), but the closed tasks are kept.
	if elemStart != -1 {
		partialBytes := raw[elemStart:]
		if synth, ok := buildSyntheticTask(partialBytes); ok {
			// task_index default = len(tasks) if not parsed.
			if synth.TaskIndex < 0 {
				synth.TaskIndex = len(tasks)
			}
			tasks = append(tasks, synth)
		}
	}

	return tasks
}

// buildSyntheticTask attempts to recover a PlanTaskResult from the
// partial bytes of a truncated task object. The partialBytes slice
// begins with `{` (the open of the task object) and continues to EOF.
//
// Synthesis rules (per the task spec):
//   - REQUIRED: parseable task_title (string-valued key at depth 1
//     inside the partial task object).
//   - REQUIRED: an opened findings[ array AND at least one complete
//     finding object inside it.
//   - Optional: task_index (int) and verdict (string) at depth 1.
//   - Defaults: verdict="warn", suggested_header_block="",
//     suggested_header_reason="", task_index = -1 (caller fills in
//     len(tasks) when -1).
//
// Returns (zero, false) if any required field is missing.
func buildSyntheticTask(partialBytes []byte) (PlanTaskResult, bool) {
	// Extract scalar fields by scanning the partial-task bytes for
	// `"<key>":<value>` at depth 1 (just inside the task `{`). The
	// walker tracks string state so keys inside nested objects /
	// strings don't confuse the match.
	scalars := scanPartialObjectScalars(partialBytes)

	title, hasTitle := scalars["task_title"]
	if !hasTitle {
		return PlanTaskResult{}, false
	}
	titleStr, ok := title.(string)
	if !ok || titleStr == "" {
		return PlanTaskResult{}, false
	}

	// Recover findings[] from inside the partial task. We invoke
	// repairOuterArray on a synthetic wrapper that makes the partial
	// task look like a top-level object: that's exactly what
	// partialBytes already is (starts with `{`), so repairOuterArray
	// can be called directly with key="findings".
	repaired, ok := repairOuterArray(partialBytes, "findings")
	if !ok {
		return PlanTaskResult{}, false
	}
	var fhost struct {
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(repaired, &fhost); err != nil {
		return PlanTaskResult{}, false
	}
	if len(fhost.Findings) == 0 {
		return PlanTaskResult{}, false
	}

	t := PlanTaskResult{
		TaskTitle:             titleStr,
		Verdict:               VerdictWarn, // default: truncation implies non-pass
		Findings:              fhost.Findings,
		SuggestedHeaderBlock:  "",
		SuggestedHeaderReason: "",
		TaskIndex:             -1, // sentinel: caller fills in
	}

	if v, ok := scalars["verdict"]; ok {
		if vs, ok := v.(string); ok {
			switch Verdict(vs) {
			case VerdictPass, VerdictWarn, VerdictFail:
				t.Verdict = Verdict(vs)
			}
		}
	}
	if v, ok := scalars["task_index"]; ok {
		// JSON numbers parse as float64; only accept non-negative
		// integer-valued floats. 2.7 → reject rather than silently
		// truncate to 2.
		if f, ok := v.(float64); ok && f >= 0 && f == math.Trunc(f) {
			t.TaskIndex = int(f)
		}
	}
	if v, ok := scalars["suggested_header_block"]; ok {
		if s, ok := v.(string); ok {
			t.SuggestedHeaderBlock = s
		}
	}
	if v, ok := scalars["suggested_header_reason"]; ok {
		if s, ok := v.(string); ok {
			t.SuggestedHeaderReason = s
		}
	}

	return t, true
}

// scanPartialObjectScalars walks the bytes of a partial JSON object
// (starting at the opening `{` and continuing through EOF) and returns
// a map of top-level scalar (string / number / bool / null) fields
// that appeared *before* the truncation point. Nested objects/arrays
// are skipped over rather than parsed.
//
// Each value is unmarshaled via json.Unmarshal into an interface{} so
// strings become Go string, numbers become float64, etc. — caller
// uses type assertions to consume them.
//
// The walker stops at EOF or when the outer `}` closes (depth == 0
// again).
func scanPartialObjectScalars(b []byte) map[string]any {
	out := map[string]any{}
	if len(b) == 0 || b[0] != '{' {
		return out
	}

	// State: positions inside the object are at depth 1 (one level
	// inside the outer `{`). We only collect keys at exactly depth 1.
	depth := 0
	inString := false
	escape := false
	// We track key extraction:
	//   awaitingKey == true: we are at depth 1 between elements,
	//     expecting either `"` (key start) or `}` (object close).
	//   keyStart >= 0: we have entered a key-string at depth 1;
	//     keyStart is the index just after the opening `"`.
	keyStart := -1
	var key string
	// expectColon == true: just finished reading a key; consuming
	// whitespace and then `:`.
	expectColon := false
	// valueStart >= 0: we have entered the value region for `key`,
	// and valueStart is the index of the first non-whitespace byte
	// of the value.
	valueStart := -1
	// scalar values live at depth==1; deeper means a nested container we skip.

	for i := 0; i < len(b); i++ {
		c := b[i]

		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
				// If we were reading a key at depth 1, finalize it.
				if depth == 1 && keyStart >= 0 && valueStart == -1 {
					key = string(b[keyStart:i])
					// Note: keys may contain JSON escapes; we use
					// json.Unmarshal to decode them properly.
					var decoded string
					if err := json.Unmarshal(b[keyStart-1:i+1], &decoded); err == nil {
						key = decoded
					}
					keyStart = -1
					expectColon = true
				}
				// If we were reading a string-valued scalar value
				// at depth 1, finalize it.
				if depth == 1 && valueStart >= 0 {
					// Decode value via json.Unmarshal of the literal
					// from valueStart..i+1 (inclusive of closing `"`).
					var v any
					if err := json.Unmarshal(b[valueStart:i+1], &v); err == nil && key != "" {
						out[key] = v
					}
					valueStart = -1
					key = ""
				}
			}
			continue
		}

		switch c {
		case '"':
			if depth == 1 && keyStart == -1 && valueStart == -1 && !expectColon {
				// Start of a key.
				inString = true
				keyStart = i + 1
				continue
			}
			if depth == 1 && valueStart == -2 {
				// First non-whitespace byte after the colon is `"` —
				// this is a string-valued scalar. Mark valueStart at
				// the opening quote so the closing-quote branch
				// (inside the inString handler) decodes the full
				// literal via json.Unmarshal.
				valueStart = i
				inString = true
				continue
			}
			// Other string starts (nested, or value-region tracking
			// for non-string scalars) — just track string state.
			inString = true
		// Nested container at depth 1 (when valueStart >= 0): we don't
		// capture nested values; let depth tracking handle skipping. The
		// '}' / ']' branch below clears valueStart when depth returns to 1.
		// If valueStart == -2 (pending scalar after `:`), the value turned
		// out to be a nested container — clear the pending state so it
		// doesn't leak into the next field.
		case '{', '[':
			if depth == 1 && valueStart == -2 {
				valueStart = -1
				key = ""
			}
			depth++
		case '}', ']':
			depth--
			if depth == 1 && valueStart >= 0 {
				// Just exited a nested container value at depth 1.
				// Drop it — we only capture scalars.
				valueStart = -1
				key = ""
			}
			if depth == 0 {
				// Outer object closed (or malformed). Stop.
				return out
			}
		case ':':
			if depth == 1 && expectColon {
				expectColon = false
				// Subsequent non-whitespace byte is the value start.
				valueStart = -2 // pending: skip whitespace, then set
			}
		case ',':
			if depth == 1 && valueStart >= 0 {
				// End of a scalar value (number/bool/null) at depth 1.
				// Decode the slice [valueStart, i).
				vs := b[valueStart:i]
				var v any
				if err := json.Unmarshal(bytes.TrimSpace(vs), &v); err == nil && key != "" {
					out[key] = v
				}
				valueStart = -1
				key = ""
			}
		default:
			// Whitespace or value-body byte.
			if depth == 1 && valueStart == -2 && !isJSONWhitespace(c) {
				valueStart = i
			}
		}
	}

	// EOF reached. If we were reading a value at depth 1 (e.g. a number
	// that wasn't terminated by `,` or `}` because truncation cut it),
	// it's incomplete — don't record it.
	return out
}
