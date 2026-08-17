package planrun

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

const ledgerFile = "plan-runs.jsonl"

// ledgerLine is one completed task row, denormalized with its run's header so
// the file can be replayed without a separate index.
//
// PRIVACY: unlike events.jsonl and codescene-events.jsonl, which are
// deliberately content-free, this record carries TaskTitle. That is why it
// requires its own opt-in (ANTI_TANGENT_PLAN_LEDGER=1) on top of
// ANTI_TANGENT_STATS_DIR rather than inheriting the stats opt-in. Silently
// changing the privacy posture of every operator who already set
// ANTI_TANGENT_STATS_DIR would be wrong, so this is a second, explicit flag —
// do not fold it into the stats opt-in as a "simplification".
//
// Row embeds TaskRow, whose SessionID field carries `json:"-"`: that tag is
// deliberate (SessionID is an in-process join key, not for disk) and must be
// preserved, not overridden, when this type is marshalled.
type ledgerLine struct {
	PlanRunID   string  `json:"plan_run_id"`
	PlanVerdict string  `json:"plan_verdict,omitempty"`
	PlanQuality string  `json:"plan_quality,omitempty"`
	TaskCount   int     `json:"task_count,omitempty"`
	Row         TaskRow `json:"row"`
}

// Ledger appends completed task rows to plan-runs.jsonl. A nil *Ledger is a
// no-op, so the disabled path is a single nil check.
type Ledger struct {
	Dir string
}

func (l *Ledger) Append(run *Run, row TaskRow) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	b, err := json.Marshal(ledgerLine{
		PlanRunID: run.ID, PlanVerdict: run.PlanVerdict,
		PlanQuality: run.PlanQuality, TaskCount: run.TaskCount, Row: row,
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.Dir, ledgerFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Load reconstructs a run from the ledger. Returns false when the ledger is
// disabled, unreadable, or holds no rows for the id.
func (l *Ledger) Load(planRunID string) (*Run, bool) {
	if l == nil || l.Dir == "" {
		return nil, false
	}
	f, err := os.Open(filepath.Join(l.Dir, ledgerFile))
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var run *Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ln ledgerLine
		if err := json.Unmarshal(sc.Bytes(), &ln); err != nil {
			continue // tolerate a torn trailing line
		}
		if ln.PlanRunID != planRunID {
			continue
		}
		if run == nil {
			run = &Run{
				ID: ln.PlanRunID, PlanVerdict: ln.PlanVerdict,
				PlanQuality: ln.PlanQuality, TaskCount: ln.TaskCount,
			}
		}
		run.Rows = append(run.Rows, ln.Row)
	}
	if run == nil {
		return nil, false
	}
	return run, true
}
