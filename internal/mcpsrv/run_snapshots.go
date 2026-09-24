package mcpsrv

import (
	"time"

	"github.com/patiently/anti-tangent-mcp/internal/config"
	"github.com/patiently/anti-tangent-mcp/internal/planrun"
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

// onPlanRunMinted records the models behind a freshly minted run and writes
// its snapshot header.
func (h *handlers) onPlanRunMinted(runID string, call planrun.ToolCall) {
	h.deps.PlanRuns.SetMeta(runID, planrun.RunMeta{
		ConfiguredModels: h.configuredModels(),
		ServerVersion:    Version,
		PlanCall:         &call,
	})
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
		PlanVerdict:      run.PlanVerdict,
		PlanQuality:      run.PlanQuality,
		TaskCount:        run.TaskCount,
		ConfiguredModels: run.ConfiguredModels,
		PlanCall:         run.PlanCall,
	})
}

// snapshotRow writes the content-free form of a changed row. TaskTitle and
// SessionID are deliberately not copied.
func (h *handlers) snapshotRow(runID string, row planrun.TaskRow) {
	if h.deps.Stats == nil {
		return
	}
	h.deps.Stats.RecordRunLine(scorecard.RunLine{
		Ts:            time.Now().UTC(),
		RunHash:       h.deps.Stats.RunHash(runID),
		ServerVersion: Version,
		Task: &scorecard.TaskSnapshot{
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
		},
	})
}
