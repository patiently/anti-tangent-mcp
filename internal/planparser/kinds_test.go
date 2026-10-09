package planparser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanKind(t *testing.T) {
	cases := []struct {
		name, preamble, kind, unknown string
	}{
		{"absent", "# Plan\n\n**Goal:** g\n", "", ""},
		{"exact", "# Plan\n\n**Plan kind:** agent-network\n", PlanKindAgentNetwork, ""},
		{"case and backticks", "**PLAN KIND:** `Agent-Network`.\n", PlanKindAgentNetwork, ""},
		{"bullet", "- **Plan kind:** agent-network\n", PlanKindAgentNetwork, ""},
		{"crlf", "**Plan kind:** agent-network\r\n", PlanKindAgentNetwork, ""},
		{"unknown", "**Plan kind:** chatbot\n", "", "chatbot"},
		{"empty value", "**Plan kind:**\n", "", ""},
		{"fenced only", "```markdown\n**Plan kind:** agent-network\n```\n", "", ""},
		{"first wins", "**Plan kind:** agent-network\n**Plan kind:** other\n", PlanKindAgentNetwork, ""},
		{"mid-line", "Set **Plan kind:** agent-network in the header.\n", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, unknown := PlanKind(c.preamble)
			require.Equal(t, c.kind, kind)
			require.Equal(t, c.unknown, unknown)
		})
	}
}

func TestSplitTasks_KindAndRung(t *testing.T) {
	plan := "# P\n\n**Plan kind:** agent-network\n\n" +
		"### Task 1: Build\n\n**Goal:** g\n\n" +
		"### Task 2: Experiment\n\n**Kind:** Experiment\n**Rung:** prompt\n\n" +
		"### Task 3: Unknown\n\n**Kind:** spike\n**Rung:** ladder-top\n\n" +
		"### Task 4: Fenced\n\n```markdown\n**Kind:** experiment\n**Rung:** facts\n```\n\n" +
		"### Task 5: Build with rung\n\n- **Kind:** build\n- **Rung:** `commitment`\n"
	tasks, preamble := SplitTasks(plan)
	require.Len(t, tasks, 5)
	kind, _ := PlanKind(preamble)
	require.Equal(t, PlanKindAgentNetwork, kind)

	want := []struct{ kind, rung, unknownKind, unknownRung string }{
		{TaskKindBuild, "", "", ""},
		{TaskKindExperiment, "prompt", "", ""},
		{TaskKindBuild, "", "spike", "ladder-top"},
		{TaskKindBuild, "", "", ""},
		{TaskKindBuild, "commitment", "", ""},
	}
	for i, w := range want {
		require.Equal(t, w.kind, tasks[i].Kind, "task %d kind", i+1)
		require.Equal(t, w.rung, tasks[i].Rung, "task %d rung", i+1)
		require.Equal(t, w.unknownKind, tasks[i].UnknownKind, "task %d unknown kind", i+1)
		require.Equal(t, w.unknownRung, tasks[i].UnknownRung, "task %d unknown rung", i+1)
	}
}
