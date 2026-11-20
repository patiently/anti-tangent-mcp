package mcpsrv

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
	"github.com/patiently/anti-tangent-mcp/internal/session"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func callFromEnvelope(tool string, env Envelope) planrun.ToolCall {
	return planrun.ToolCall{
		Tool:     tool,
		Model:    env.ModelUsed,
		Verdict:  env.Verdict,
		Findings: len(env.Findings),
		MS:       env.ReviewMS,
		Partial:  env.Partial,
	}
}

// configuredModels is the server's model per reviewing role. A role whose
// model is unset is recorded as "" rather than omitted, so a reader can tell
// an unset worker from a record that predates the role.
func (h *handlers) configuredModels() map[string]string {
	cfg := h.deps.Cfg
	ref := func(m config.ModelRef) string {
		if m.Model == "" {
			return ""
		}
		return m.String()
	}
	return map[string]string{
		"plan":   ref(cfg.PlanModel),
		"pre":    ref(cfg.PreModel),
		"mid":    ref(cfg.MidModel),
		"post":   ref(cfg.PostModel),
		"worker": ref(cfg.WorkerModel),
	}
}

// onPlanRunHeader records the models behind a run a validate_plan round just
// minted or revised, and writes the round's snapshot header. call is the zero
// value for a round that made no reviewer call. tasksCarried is how many tasks
// the round took from the round before.
func (h *handlers) onPlanRunHeader(runID string, call planrun.ToolCall, tasksCarried int) {
	meta := planrun.RunMeta{
		ConfiguredModels: h.configuredModels(),
		ServerVersion:    Version,
		PlanCall:         &call,
	}
	if call.Tool == "" {
		// The round made no reviewer call: keep the plan call of the last
		// round that did, so the header's model and latency stay those of a
		// real review.
		meta.PlanCall = nil
		if run, ok := h.deps.PlanRuns.Snapshot(runID); ok {
			meta.PlanCall = run.PlanCall
		}
	}
	h.deps.PlanRuns.SetMeta(runID, meta)
	if h.deps.Stats == nil {
		return
	}
	run, ok := h.deps.PlanRuns.Snapshot(runID)
	if !ok {
		return
	}
	h.deps.Stats.RecordRunLine(scorecard.RunLine{
		Ts:               time.Now().UTC(),
		RunHash:          h.deps.Stats.RunHash(run.ID),
		ServerVersion:    Version,
		Header:           true,
		Kind:             scorecard.KindPlan,
		PlanVerdict:      run.PlanVerdict,
		PlanQuality:      run.PlanQuality,
		TaskCount:        run.TaskCount,
		ConfiguredModels: run.ConfiguredModels,
		PlanCall:         run.PlanCall,
		Revision:         run.Revision,
		TasksCarried:     tasksCarried,
	})
}

// recordSessionRow applies update to the row of a session with no plan run
// and, once the row carries a post verdict, writes its run lines: a header on
// the first and the task line on every one. A session that never reaches
// validate_completion writes nothing.
func (h *handlers) recordSessionRow(sess *session.Session, update func(*planrun.TaskRow)) {
	row, firstWrite, ok := h.deps.Sessions.UpdateRow(sess.ID, update)
	if !ok {
		return
	}
	if h.deps.Stats == nil || row.PostVerdict == "" {
		return
	}
	hash := h.deps.Stats.SessionRunHash(sess.ID)
	if firstWrite {
		h.deps.Stats.RecordRunLine(scorecard.RunLine{
			Ts:               time.Now().UTC(),
			RunHash:          hash,
			ServerVersion:    Version,
			Header:           true,
			Kind:             scorecard.KindSession,
			TaskCount:        1,
			ConfiguredModels: h.configuredModels(),
		})
	}
	h.deps.Stats.RecordRunLine(scorecard.RunLine{
		Ts:            time.Now().UTC(),
		RunHash:       hash,
		ServerVersion: Version,
		Task:          taskSnapshot(row),
	})
}

// snapshotRow writes the content-free form of a changed row.
func (h *handlers) snapshotRow(runID string, row planrun.TaskRow) {
	if h.deps.Stats == nil {
		return
	}
	h.deps.Stats.RecordRunLine(scorecard.RunLine{
		Ts:            time.Now().UTC(),
		RunHash:       h.deps.Stats.RunHash(runID),
		ServerVersion: Version,
		Task:          taskSnapshot(row),
	})
}

// taskSnapshot is the content-free form of a row. TaskTitle and SessionID are
// deliberately not copied.
func taskSnapshot(row planrun.TaskRow) *scorecard.TaskSnapshot {
	return &scorecard.TaskSnapshot{
		Index:          row.Index,
		PreVerdict:     row.PreVerdict,
		PostVerdict:    row.PostVerdict,
		Checkpoints:    row.Checkpoints,
		Attempts:       row.Attempts,
		Severity:       row.Severity,
		Waived:         row.Waived,
		Escalated:      row.Escalated,
		Lite:           row.Lite,
		Unmatched:      row.Unmatched,
		CodesceneState: row.CodesceneState,
		Calls:          row.Calls,
		CallsDropped:   row.CallsDropped,
		Categories:     row.Categories,
		LinesAdded:     row.LinesAdded,
		LinesRemoved:   row.LinesRemoved,

		OverBuildingRuled: row.OverBuildingRuled,
		ImplementerModel:  row.ImplementerModel,
		TestsAdded:        row.TestsAdded,
		ACCount:           row.ACCount,
		LedgerEntries:     row.LedgerEntries,
		TestsDeclined:     row.TestsDeclined,
		Criteria:          row.Criteria,
	}
}
