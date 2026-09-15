package mcpsrv

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/patiently/anti-tangent-mcp/internal/planparser"
	"github.com/patiently/anti-tangent-mcp/internal/verdict"
)

// TestParsedTaskIndexes_ADriftedTitleOnlyRekeysItsOwnResult covers a
// truncation-recovered merge: two complete 8-task chunks (chunk 2's results
// carry chunk-local task_index 1..8, as reviewOnePlanChunk emits) followed by
// a truncation-recovered task 17 whose restated title no longer matches the
// plan's own heading at that position. Alignment is judged per result, so
// every matching-title result keys on its own heading regardless of any
// other result's title, and the drifted one keys on its de-based index.
func TestParsedTaskIndexes_ADriftedTitleOnlyRekeysItsOwnResult(t *testing.T) {
	tasks := make([]planparser.RawTask, 17)
	for i := range tasks {
		tasks[i] = planparser.RawTask{Title: fmt.Sprintf("Task %d: t%d", i+1, i+1)}
	}

	results := make([]verdict.PlanTaskResult, 17)
	for i := 0; i < 8; i++ { // chunk 1: task_index already global (1..8)
		results[i] = verdict.PlanTaskResult{TaskIndex: i + 1, TaskTitle: fmt.Sprintf("Task %d: t%d", i+1, i+1)}
	}
	for i := 8; i < 16; i++ { // chunk 2: matching titles, but chunk-local task_index (1..8)
		results[i] = verdict.PlanTaskResult{TaskIndex: i - 7, TaskTitle: fmt.Sprintf("Task %d: t%d", i+1, i+1)}
	}
	results[16] = verdict.PlanTaskResult{TaskIndex: 17, TaskTitle: "Task 17: parse helper"} // recovered, drifted title, global index

	got := parsedTaskIndexes(results, tasks)
	want := make([]int, 17)
	for i := range want {
		want[i] = i
	}
	assert.Equal(t, want, got, "every matching-title result keys on its own heading; the drifted one keys on its index's heading")
}
