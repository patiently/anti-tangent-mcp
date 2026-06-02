package tray

import (
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
)

func TestNowWorkingLabelNotSet(t *testing.T) {
	if got := nowWorkingLabel(bm.NowWorking{NotFound: true}, time.Now()); got != "🛠 Currently working on — (not set up)" {
		t.Fatalf("not-found should be not-set: %q", got)
	}
	if got := nowWorkingLabel(bm.NowWorking{Body: "   "}, time.Now()); got != "🛠 Currently working on — (not set up)" {
		t.Fatalf("blank body should be not-set: %q", got)
	}
}

func TestNowWorkingLabelWithAge(t *testing.T) {
	nw := bm.NowWorking{Body: "Wiring the tray", HasUpdated: true,
		Updated: time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)}
	now := time.Date(2026, 6, 2, 8, 5, 0, 0, time.UTC)
	if got := nowWorkingLabel(nw, now); got != "🛠 Wiring the tray (⟳ 5m ago)" {
		t.Fatalf("got %q", got)
	}
}

func TestPRLabel(t *testing.T) {
	if got := prLabel("o/r", 42, "Fix the thing"); got != "o/r #42  Fix the thing" {
		t.Fatalf("got %q", got)
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("a\nb", 10); got != "a b" {
		t.Fatalf("newline->space: %q", got)
	}
	if got := oneLine("0123456789ABCDEF", 10); got != "012345678…" {
		t.Fatalf("truncate: %q", got)
	}
}

func TestHumanAge(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "just now",
		5 * time.Minute:  "5m ago",
		3 * time.Hour:    "3h ago",
		50 * time.Hour:   "2d ago",
	}
	for d, want := range cases {
		if got := humanAge(d); got != want {
			t.Fatalf("humanAge(%v)=%q want %q", d, got, want)
		}
	}
}
