package stats

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const stateFile = "state.json"

// State persists cadence + the session-hash salt across process restarts so a
// freshly-launched stdio server neither re-summarizes immediately nor loses the
// interval.
type State struct {
	LastSummaryAt      time.Time `json:"last_summary_at"`
	EventsSinceSummary int       `json:"events_since_summary"`
	Salt               string    `json:"salt"`
}

// loadState reads state.json from dir. A missing or corrupt file yields a zero
// State with a freshly-generated salt, so a bad file never blocks recording.
func loadState(dir string) State {
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return State{Salt: newSalt()}
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil || st.Salt == "" {
		return State{Salt: newSalt()}
	}
	return st
}

func saveState(dir string, st State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, stateFile), b, 0o644)
}

func newSalt() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read effectively never fails; degrade to a time-derived
		// salt rather than returning an empty (unsalted) value.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// due reports whether a compaction should run now, per the hybrid trigger. The
// Recorder seeds LastSummaryAt on first enable, so the interval branch is
// measured from enable time, not the zero epoch.
func due(now time.Time, st State, interval time.Duration, threshold int) bool {
	return st.EventsSinceSummary >= threshold || now.Sub(st.LastSummaryAt) >= interval
}
