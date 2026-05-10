package planparser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitTasks_AllStructured(t *testing.T) {
	in := `# Plan

Intro text.

### Task 1: First

**Goal:** Do thing one.

**Acceptance criteria:**
- AC

### Task 2: Second

**Goal:** Do thing two.

**Acceptance criteria:**
- AC
`
	tasks, preamble := SplitTasks(in)
	assert.Contains(t, preamble, "Intro text.")
	require.Len(t, tasks, 2)
	assert.Equal(t, "Task 1: First", tasks[0].Title)
	assert.Equal(t, "Task 2: Second", tasks[1].Title)
	assert.True(t, tasks[0].HasStructuredHeader)
	assert.True(t, tasks[1].HasStructuredHeader)
}

func TestSplitTasks_AllTDDShape(t *testing.T) {
	in := `### Task 1: Bootstrap

Files:
- main.go

Step 1: write main.

### Task 2: Add tests

Files:
- main_test.go

Step 1: write tests.
`
	tasks, preamble := SplitTasks(in)
	assert.Empty(t, preamble)
	require.Len(t, tasks, 2)
	assert.False(t, tasks[0].HasStructuredHeader)
	assert.False(t, tasks[1].HasStructuredHeader)
}

func TestSplitTasks_Mixed(t *testing.T) {
	in := `### Task 1: Structured

**Goal:** g
**Acceptance criteria:**
- AC

### Task 2: TDD

Files:
- f.go
`
	tasks, _ := SplitTasks(in)
	require.Len(t, tasks, 2)
	assert.True(t, tasks[0].HasStructuredHeader)
	assert.False(t, tasks[1].HasStructuredHeader)
}

func TestSplitTasks_NoHeadings(t *testing.T) {
	in := "Just some text without any task headings."
	tasks, preamble := SplitTasks(in)
	assert.Empty(t, tasks)
	assert.Equal(t, in, preamble)
}

func TestSplitTasks_Empty(t *testing.T) {
	tasks, preamble := SplitTasks("")
	assert.Empty(t, tasks)
	assert.Empty(t, preamble)
}

func TestSplitTasks_HeadingWithoutNumberIgnored(t *testing.T) {
	in := `### Task: Not Numbered

Body.

### Task 1: Numbered

Body.
`
	tasks, preamble := SplitTasks(in)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Task 1: Numbered", tasks[0].Title)
	assert.Contains(t, preamble, "Task: Not Numbered")
}

func TestSplitTasks_FencedTaskWordNotMatched(t *testing.T) {
	in := "" +
		"### Task 1: Real\n\n" +
		"```\n" +
		"### Task 99: Inside fence\n" +
		"```\n\n" +
		"Body continues.\n"
	tasks, _ := SplitTasks(in)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Task 1: Real", tasks[0].Title)
	assert.Contains(t, tasks[0].Body, "### Task 99: Inside fence")
	assert.Contains(t, tasks[0].Body, "Body continues.")
}
