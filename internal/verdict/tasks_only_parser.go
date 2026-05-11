package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParseTasksOnly unmarshals a per-chunk reviewer response and validates
// required-field constraints (non-empty tasks array, valid verdict enum,
// non-empty task_title, and per-finding severity/category).
func ParseTasksOnly(raw []byte) (TasksOnly, error) {
	body := stripFences(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r TasksOnly
	if err := dec.Decode(&r); err != nil {
		return TasksOnly{}, fmt.Errorf("decode tasks_only: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return TasksOnly{}, fmt.Errorf("decode tasks_only: extra JSON after document")
	}
	if len(r.Tasks) == 0 {
		return TasksOnly{}, fmt.Errorf("tasks_only: tasks array must be non-empty")
	}
	for i, t := range r.Tasks {
		switch t.Verdict {
		case VerdictPass, VerdictWarn, VerdictFail:
		default:
			return TasksOnly{}, fmt.Errorf("tasks_only: tasks[%d]: invalid verdict %q", i, t.Verdict)
		}
		if t.TaskTitle == "" {
			return TasksOnly{}, fmt.Errorf("tasks_only: tasks[%d]: task_title must be non-empty", i)
		}
		for j, f := range t.Findings {
			if err := validateFinding(f, fmt.Sprintf("tasks[%d].findings[%d]", i, j)); err != nil {
				return TasksOnly{}, err
			}
		}
	}
	return r, nil
}
