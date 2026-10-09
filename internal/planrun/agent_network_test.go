package planrun

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/patiently/anti-tangent-mcp/internal/ratedigest"
)

// setAgentNetwork replaces run runID's plan kind and boundary rules.
func (s *Store) setAgentNetwork(runID, planKind string, rules []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	r.PlanKind = planKind
	r.BoundaryRules = append([]string(nil), rules...)
	return true
}

func agentNetworkRun(t *testing.T, s *Store) *Run {
	t.Helper()
	return s.CreateWithTasks("pass", "rigorous", []PlanTask{
		{Index: 1, Title: "Task 1: Mapper", Kind: "build"},
		{Index: 2, Title: "Task 2: Zip prompt", Kind: "experiment", Rung: "prompt"},
	})
}

func TestTaskAgentNetwork_ByIndexAndByTitle(t *testing.T) {
	s := NewStore(time.Hour)
	run := agentNetworkRun(t, s)
	require.True(t, s.setAgentNetwork(run.ID, "agent-network", []string{"no regex over reply text"}))

	got, ok := s.TaskAgentNetwork(run.ID, TaskRef{Index: 2})
	require.True(t, ok)
	require.Equal(t, AgentNetwork{PlanKind: "agent-network", BoundaryRules: []string{"no regex over reply text"}, TaskKind: "experiment", Rung: "prompt", TaskFound: true}, got)

	got, ok = s.TaskAgentNetwork(run.ID, TaskRef{Title: "Zip prompt"})
	require.True(t, ok)
	require.Equal(t, "experiment", got.TaskKind)

	got, ok = s.TaskAgentNetwork(run.ID, TaskRef{Title: "Not in the plan"})
	require.True(t, ok)
	require.Equal(t, "agent-network", got.PlanKind)
	require.False(t, got.TaskFound)
	require.Empty(t, got.TaskKind)
	require.Empty(t, got.Rung)

	got, ok = s.TaskAgentNetwork(run.ID, TaskRef{})
	require.True(t, ok)
	require.Empty(t, got.TaskKind)

	_, ok = s.TaskAgentNetwork("pr_unknown", TaskRef{Index: 1})
	require.False(t, ok)
	require.False(t, s.setAgentNetwork("pr_unknown", "agent-network", nil))
}

func TestTaskAgentNetwork_ReviseReplacesKinds(t *testing.T) {
	s := NewStore(time.Hour)
	run := agentNetworkRun(t, s)
	require.True(t, s.setAgentNetwork(run.ID, "agent-network", []string{"rule"}))
	_, ok := s.Revise(run.ID, "pass", "rigorous", []PlanTask{
		{Index: 1, Title: "Task 1: Mapper", Kind: "build"},
		{Index: 2, Title: "Task 2: Zip prompt", Kind: "build"},
	}, "", nil, nil)
	require.True(t, ok)
	require.True(t, s.setAgentNetwork(run.ID, "", nil))

	got, ok := s.TaskAgentNetwork(run.ID, TaskRef{Index: 2})
	require.True(t, ok)
	require.Equal(t, AgentNetwork{TaskKind: "build", TaskFound: true}, got)
}

func TestTaskAgentNetwork_ReturnsCopies(t *testing.T) {
	s := NewStore(time.Hour)
	run := agentNetworkRun(t, s)
	rules := []string{"rule one"}
	require.True(t, s.setAgentNetwork(run.ID, "agent-network", rules))
	rules[0] = "changed by caller"

	got, _ := s.TaskAgentNetwork(run.ID, TaskRef{Index: 1})
	require.Equal(t, []string{"rule one"}, got.BoundaryRules)
	got.BoundaryRules[0] = "changed by reader"

	snap, ok := s.Snapshot(run.ID)
	require.True(t, ok)
	require.Equal(t, []string{"rule one"}, snap.BoundaryRules)
}

func TestLedgerHeader_CarriesNoAgentNetworkFields(t *testing.T) {
	s := NewStore(time.Hour)
	run := agentNetworkRun(t, s)
	require.True(t, s.setAgentNetwork(run.ID, "agent-network", []string{"secret rule text"}))
	snap, _ := s.Snapshot(run.ID)
	b, err := json.Marshal(snap)
	require.NoError(t, err)
	require.NotContains(t, string(b), "secret rule text")
	require.NotContains(t, string(b), "experiment")
	require.NotContains(t, string(b), "agent-network")
}

func TestSessionAgentNetwork_FollowsTheRowAndARevision(t *testing.T) {
	s := NewStore(time.Hour)
	run := agentNetworkRun(t, s)
	require.True(t, s.setAgentNetwork(run.ID, "agent-network", nil))
	_, ok := s.Attach(run.ID, "sess-1", TaskRef{Index: 2}, "pass")
	require.True(t, ok)

	got, ok := s.SessionAgentNetwork(run.ID, "sess-1")
	require.True(t, ok)
	require.Equal(t, "experiment", got.TaskKind)

	_, ok = s.Revise(run.ID, "pass", "rigorous", []PlanTask{
		{Index: 1, Title: "Task 1: Mapper", Kind: "build"},
		{Index: 2, Title: "Task 2: Zip prompt", Kind: "build"},
	}, "", nil, nil)
	require.True(t, ok)
	got, ok = s.SessionAgentNetwork(run.ID, "sess-1")
	require.True(t, ok)
	require.Equal(t, "build", got.TaskKind, "a revised plan's kinds take effect at once")

	_, ok = s.SessionAgentNetwork(run.ID, "sess-unknown")
	require.False(t, ok)
	_, ok = s.SessionAgentNetwork("pr_unknown", "sess-1")
	require.False(t, ok)
}

func TestRender_RateColumnOnlyWithADigest(t *testing.T) {
	plain := Render(sampleRun())
	require.NotContains(t, plain, "Rate")

	r := sampleRun()
	five, eight := 5, 8
	r.Rows[0].RateDigest = &ratedigest.Digest{N: 10, BeforeK: &five, AfterK: &eight,
		Suite: &ratedigest.Suite{Evals: 42}, RigidityDelta: &ratedigest.RigidityDelta{StrategyLines: 3}}
	got := Render(r)
	lines := strings.Split(got, "\n")
	var header, first, second string
	for i, l := range lines {
		if strings.HasPrefix(l, "  #  Task") {
			header, first, second = l, lines[i+1], lines[i+2]
		}
	}
	require.Contains(t, header, "Rate")
	require.Contains(t, first, "5→8/10 · reg 0 · rig +0/+0/+0")
	column := func(line, word string) int { return utf8.RuneCountInString(line[:strings.Index(line, word)]) }
	require.Equal(t, column(header, "CodeScene"), column(first, "passed"), "the CodeScene column stays aligned")
	require.Equal(t, column(header, "CodeScene"), column(second, "skipped"))
}

func TestCloneRow_DeepCopiesRateDigest(t *testing.T) {
	k := 3
	row := TaskRow{RateDigest: &ratedigest.Digest{N: 10, AfterK: &k}}
	cp := cloneRow(row)
	*cp.RateDigest.AfterK = 9
	require.Equal(t, 3, *row.RateDigest.AfterK)
}

func TestRender_RateColumnFitsItsWidestCell(t *testing.T) {
	r := sampleRun()
	big, small := 10000, 5
	r.Rows[0].RateDigest = &ratedigest.Digest{N: 10000, BeforeK: &big, AfterK: &big,
		Suite: &ratedigest.Suite{Evals: 42}, RigidityDelta: &ratedigest.RigidityDelta{OutboundStrings: 12}}
	r.Rows[1].RateDigest = &ratedigest.Digest{N: 10, AfterK: &small}
	lines := strings.Split(Render(r), "\n")
	var header, first, second string
	for i, l := range lines {
		if strings.HasPrefix(l, "  #  Task") {
			header, first, second = l, lines[i+1], lines[i+2]
		}
	}
	require.Contains(t, first, "10000→10000/10000 · reg 0 · rig +12/+0/+0")
	column := func(line, word string) int { return utf8.RuneCountInString(line[:strings.Index(line, word)]) }
	require.Equal(t, column(header, "CodeScene"), column(first, "passed"))
	require.Equal(t, column(header, "CodeScene"), column(second, "skipped"))
}

func TestSessionAgentNetwork_FindsTheRowsTaskByTitleAfterARenumbering(t *testing.T) {
	s := NewStore(time.Hour)
	run := agentNetworkRun(t, s)
	_, ok := s.Attach(run.ID, "sess-1", TaskRef{Index: 1}, "pass")
	require.True(t, ok)

	_, ok = s.Revise(run.ID, "pass", "rigorous", []PlanTask{
		{Index: 1, Title: "Task 1: Probe", Kind: "experiment", Rung: "variance"},
		{Index: 2, Title: "Task 2: Mapper", Kind: "build"},
		{Index: 3, Title: "Task 3: Zip prompt", Kind: "experiment", Rung: "prompt"},
	}, "agent-network", nil, nil)
	require.True(t, ok)

	got, ok := s.SessionAgentNetwork(run.ID, "sess-1")
	require.True(t, ok)
	require.True(t, got.TaskFound)
	require.Equal(t, "build", got.TaskKind, "the mapper moved to position 2; its title still finds it")
}

func TestCreateForPlanAndRevise_StoreTheDeclarationsWithTheTasks(t *testing.T) {
	s := NewStore(time.Hour)
	run := s.CreateForPlan("pass", "rigorous", []PlanTask{{Index: 1, Title: "Task 1: A"}}, "agent-network", []string{"rule"})
	got, ok := s.TaskAgentNetwork(run.ID, TaskRef{Index: 1})
	require.True(t, ok)
	require.Equal(t, "agent-network", got.PlanKind)
	require.Equal(t, []string{"rule"}, got.BoundaryRules)

	_, ok = s.Revise(run.ID, "pass", "rigorous", []PlanTask{{Index: 1, Title: "Task 1: A"}}, "", nil, nil)
	require.True(t, ok)
	got, _ = s.TaskAgentNetwork(run.ID, TaskRef{Index: 1})
	require.Empty(t, got.PlanKind)
	require.Empty(t, got.BoundaryRules)
}

func TestSessionAgentNetwork_ATaskInsertedByARevisionIsItsOwnSessionsTask(t *testing.T) {
	s := NewStore(time.Hour)
	run := agentNetworkRun(t, s)
	_, ok := s.Attach(run.ID, "sess-mapper", TaskRef{Index: 1, Title: "Map the ZIP fields"}, "pass")
	require.True(t, ok)

	_, ok = s.Revise(run.ID, "pass", "rigorous", []PlanTask{
		{Index: 1, Title: "Task 1: Probe", Kind: "experiment", Rung: "variance"},
		{Index: 2, Title: "Task 2: Mapper", Kind: "build"},
		{Index: 3, Title: "Task 3: Zip prompt", Kind: "experiment", Rung: "prompt"},
	}, "agent-network", nil, nil)
	require.True(t, ok)
	_, ok = s.Attach(run.ID, "sess-probe", TaskRef{Index: 1}, "pass")
	require.True(t, ok)

	got, ok := s.SessionAgentNetwork(run.ID, "sess-probe")
	require.True(t, ok)
	require.Equal(t, "experiment", got.TaskKind, "the probe's session is reviewed as the probe")
	require.Equal(t, "variance", got.Rung)
	got, _ = s.SessionAgentNetwork(run.ID, "sess-mapper")
	require.Equal(t, "build", got.TaskKind, "the mapper's session still follows the mapper")
}
