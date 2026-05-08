package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Parse decodes provider output into a Result and validates enum fields.
// It tolerates a ```json ... ``` wrapper and surrounding whitespace.
func Parse(raw []byte) (Result, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r Result
	if err := dec.Decode(&r); err != nil {
		return Result{}, fmt.Errorf("decode result: %w", err)
	}
	if dec.More() {
		return Result{}, fmt.Errorf("decode result: extra JSON after document")
	}
	switch r.Verdict {
	case VerdictPass, VerdictWarn, VerdictFail:
	default:
		return Result{}, fmt.Errorf("invalid verdict %q", r.Verdict)
	}
	for i, f := range r.Findings {
		switch f.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			return Result{}, fmt.Errorf("finding[%d]: invalid severity %q", i, f.Severity)
		}
		if !validCategory(f.Category) {
			return Result{}, fmt.Errorf("finding[%d]: invalid category %q", i, f.Category)
		}
	}
	return r, nil
}

func validCategory(c Category) bool {
	switch c {
	case CategoryMissingAC, CategoryScopeDrift, CategoryAmbiguousSpec,
		CategoryUnaddressed, CategoryQuality, CategorySessionMissing,
		CategoryTooLarge, CategoryOther:
		return true
	}
	return false
}

func stripFences(b []byte) []byte {
	s := string(b)
	if !strings.HasPrefix(s, "```") {
		return b
	}
	// strip leading fence (with optional language tag)
	if nl := strings.IndexByte(s, '\n'); nl != -1 {
		s = s[nl+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return []byte(strings.TrimSpace(s))
}

// RetryHint is the system-side instruction we append when reissuing
// the prompt after a failed parse.
func RetryHint() string {
	return "Your previous response was not valid JSON matching the schema. " +
		"Respond with ONLY the JSON object, no commentary, no code fences."
}
