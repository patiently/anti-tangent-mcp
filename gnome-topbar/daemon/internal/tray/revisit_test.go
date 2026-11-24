package tray

import (
	"reflect"
	"strings"
	"testing"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
	"github.com/patiently/anti-tangent-mcp/scorecard"
)

func TestRevisitTitle(t *testing.T) {
	cases := []struct {
		n, g int
		want string
	}{
		{2, 0, "🔁 Cycle review due (2)"},
		{2, 4, "🔁 Cycle review due (2) · 4 too small to judge"},
		{0, 4, "🔁 Cycle review: none due · 4 too small to judge"},
		{0, 0, ""},
	}
	for _, c := range cases {
		if got := revisitTitle(c.n, c.g); got != c.want {
			t.Errorf("revisitTitle(%d,%d) = %q, want %q", c.n, c.g, got, c.want)
		}
	}
}

func driftSignal(id string) scorecard.Signal {
	return scorecard.Signal{
		ID: id, Kind: scorecard.RevisitTestVolume, Direction: "up",
		Cohort:   &scorecard.CohortKey{Kind: "plan", ImplementerModel: "impl", ReviewModel: "rev"},
		Recent:   &scorecard.Rate{Value: .41},
		Baseline: &scorecard.Rate{Value: .18},
	}
}

func TestRevisitLabel(t *testing.T) {
	got := revisitLabel(driftSignal("a"))
	for _, w := range []string{"test_volume_drift ↑ plan · impl / rev", "41% vs 18%"} {
		if !strings.Contains(got, w) {
			t.Errorf("label %q missing %q", got, w)
		}
	}
	s := driftSignal("a")
	s.Direction, s.Source = "down", "confirmed"
	if got := revisitLabel(s); !strings.Contains(got, "↓") || !strings.Contains(got, "· confirmed") {
		t.Errorf("label %q", got)
	}
	nm := scorecard.Signal{ID: "new_model:m", Kind: scorecard.RevisitNewModel, Model: "m"}
	if got := revisitLabel(nm); got != "new model: m" {
		t.Errorf("new model label %q", got)
	}
}

func TestRevisitMenuModel(t *testing.T) {
	sigs := []scorecard.Signal{driftSignal("a"), driftSignal("b")}
	if _, show, _, _ := revisitMenu(state.Snapshot{Revisit: sigs}); show {
		t.Error("menu shown while revisit state unknown")
	}
	title, show, ids, labels := revisitMenu(state.Snapshot{RevisitKnown: true, Revisit: sigs})
	if !show || title != "🔁 Cycle review due (2)" || !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("got %v %q %v", show, title, ids)
	}
	if labels[0] != revisitLabel(sigs[0]) {
		t.Errorf("labels %v", labels)
	}
	_, show, ids, _ = revisitMenu(state.Snapshot{RevisitKnown: true, RevisitGated: 3})
	if !show || len(ids) != 0 {
		t.Errorf("gated-only: show=%v ids=%v", show, ids)
	}
}

func TestAckRevisitPrefixes(t *testing.T) {
	if got := AckRevisitIDs("x"); !reflect.DeepEqual(got, []string{"revisit:x"}) {
		t.Errorf("got %v", got)
	}
}
