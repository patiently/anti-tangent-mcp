// Package planparser splits an implementation-plan markdown string into a
// preamble and an ordered list of tasks. It is intentionally minimal: each
// task's body is returned verbatim including its heading line so the reviewer
// LLM sees the same text the human wrote.
package planparser

import (
	"regexp"
	"strings"
)

// RawTask is one task carved out of a plan markdown document.
type RawTask struct {
	// Title is the heading text after "### " — for example, "Task 4: Add /healthz endpoint".
	Title string
	// Body is the full task content including the "### Task N: …" heading line.
	Body string
	// HasStructuredHeader is true iff the body contains both **Goal:** and
	// **Acceptance criteria:** markers. Used for telemetry only; not sent to
	// the reviewer.
	HasStructuredHeader bool
}

// taskHeadingRe matches lines like "### Task 4: Add /healthz endpoint".
var taskHeadingRe = regexp.MustCompile(`(?m)^### Task \d+:.*$`)

// SplitTasks carves planText into a preamble and an ordered list of RawTask.
// The preamble is everything before the first task heading (empty if the plan
// starts with a heading). If no headings match, tasks is nil and preamble is
// the full input.
func SplitTasks(planText string) ([]RawTask, string) {
	if planText == "" {
		return nil, ""
	}
	matches := taskHeadingRe.FindAllStringIndex(planText, -1)
	if len(matches) == 0 {
		return nil, planText
	}

	// Filter out matches that are inside fenced code blocks.
	matches = filterFencedMatches(planText, matches)
	if len(matches) == 0 {
		return nil, planText
	}

	preamble := planText[:matches[0][0]]
	tasks := make([]RawTask, 0, len(matches))
	for i, m := range matches {
		end := len(planText)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		body := planText[m[0]:end]
		// Title: heading line minus the "### " prefix, trimmed.
		headingLine := planText[m[0]:m[1]]
		title := strings.TrimSpace(strings.TrimPrefix(headingLine, "### "))
		tasks = append(tasks, RawTask{
			Title:               title,
			Body:                body,
			HasStructuredHeader: hasStructuredHeader(body),
		})
	}
	return tasks, preamble
}

// filterFencedMatches removes any match indices that fall inside fenced code blocks.
func filterFencedMatches(planText string, matches [][]int) [][]int {
	lines := strings.Split(planText, "\n")
	inFence := false
	lineStart := 0
	lineFences := make(map[int]bool) // lineNumber -> isInsideFence

	// Build a map of which lines are inside fences.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		lineFences[i] = inFence
		lineStart += len(line) + 1 // +1 for the newline
	}

	// Filter matches: keep only those on lines outside fences.
	var filtered [][]int
	for _, m := range matches {
		// Find which line this match is on.
		lineNum := 0
		pos := 0
		for i, line := range lines {
			if pos <= m[0] && m[0] < pos+len(line)+1 {
				lineNum = i
				break
			}
			pos += len(line) + 1
		}
		if !lineFences[lineNum] {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func hasStructuredHeader(body string) bool {
	return strings.Contains(body, "**Goal:**") && strings.Contains(body, "**Acceptance criteria:**")
}
