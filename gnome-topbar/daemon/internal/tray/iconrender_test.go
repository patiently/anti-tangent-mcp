package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

// fiveHourStats builds a present snapshot with one account per entry, each
// carrying only a 5h window at the given utilization.
func fiveHourStats(util map[string]float64, gen time.Time) claudestats.Stats {
	accts := map[string]claudestats.Account{}
	for k, v := range util {
		vv := v
		accts[k] = claudestats.Account{Limits: &claudestats.Limits{FiveHour: &claudestats.Window{Utilization: &vv}}}
	}
	return claudestats.Stats{Present: true, GeneratedAt: gen, Accounts: accts}
}

func decodePNG(t *testing.T, raw []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img
}

// countColor counts pixels exactly matching c (colors used are opaque, so the
// premultiplied RGBA() high bytes equal the straight 8-bit components).
func countColor(img image.Image, c color.RGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if uint8(r>>8) == c.R && uint8(g>>8) == c.G && uint8(bl>>8) == c.B && uint8(a>>8) == c.A {
				n++
			}
		}
	}
	return n
}

func TestUsageIconPNG_AbsentFallsBack(t *testing.T) {
	if _, ok := usageIconPNG(claudestats.Stats{}, time.Now()); ok {
		t.Error("absent stats should not render (caller falls back to static icon)")
	}
}

func TestUsageIconPNG_NoKnownUtilFallsBack(t *testing.T) {
	cs := claudestats.Stats{Present: true, Accounts: map[string]claudestats.Account{
		"a": {Limits: &claudestats.Limits{FiveHour: &claudestats.Window{}}}, // window present, utilization nil
	}}
	if _, ok := usageIconPNG(cs, time.Now()); ok {
		t.Error("no known utilization should fall back to static icon")
	}
}

func TestUsageIconPNG_RendersPerAccountColoredBars(t *testing.T) {
	now := time.Now()
	cs := fiveHourStats(map[string]float64{"a": 90, "b": 10}, now) // a=red (≥80), b=green (<60)
	raw, ok := usageIconPNG(cs, now)
	if !ok {
		t.Fatal("expected an icon")
	}
	img := decodePNG(t, raw)
	if dx, dy := img.Bounds().Dx(), img.Bounds().Dy(); dx != iconSize || dy != iconSize {
		t.Fatalf("size %dx%d, want %dx%d", dx, dy, iconSize, iconSize)
	}
	if countColor(img, barColorFor(90, false)) == 0 {
		t.Error("expected red pixels for the 90%% account")
	}
	if countColor(img, barColorFor(10, false)) == 0 {
		t.Error("expected green pixels for the 10%% account")
	}
	// the 90%% bar fills more than the 10%% bar
	if countColor(img, barColorFor(90, false)) <= countColor(img, barColorFor(10, false)) {
		t.Error("higher utilization should fill more pixels")
	}
}

func TestUsageIconPNG_StaleIsGray(t *testing.T) {
	now := time.Now()
	cs := fiveHourStats(map[string]float64{"a": 90}, now.Add(-20*time.Minute)) // older than staleAfter
	raw, ok := usageIconPNG(cs, now)
	if !ok {
		t.Fatal("expected an icon")
	}
	img := decodePNG(t, raw)
	if countColor(img, barColorFor(90, true)) == 0 {
		t.Error("stale snapshot should render gray bars")
	}
	if countColor(img, barColorFor(90, false)) != 0 {
		t.Error("stale snapshot must not render live (red) bars")
	}
}

func TestUsageIconPNG_AmberBand(t *testing.T) {
	now := time.Now()
	raw, ok := usageIconPNG(fiveHourStats(map[string]float64{"a": 70}, now), now) // 60 ≤ 70 < 80
	if !ok {
		t.Fatal("expected an icon")
	}
	if countColor(decodePNG(t, raw), barColorFor(70, false)) == 0 {
		t.Error("70%% should render an amber bar")
	}
}

func TestUsageIconPNG_CapsAtMaxBarsByKeyOrder(t *testing.T) {
	now := time.Now()
	// 5 accounts; the only red one (e=90) sorts last, so the iconMaxBars=4 cap
	// drops it — proving both the cap and the deterministic key ordering.
	cs := fiveHourStats(map[string]float64{"a": 10, "b": 10, "c": 10, "d": 10, "e": 90}, now)
	raw, ok := usageIconPNG(cs, now)
	if !ok {
		t.Fatal("expected an icon")
	}
	img := decodePNG(t, raw)
	if countColor(img, barColorFor(90, false)) != 0 {
		t.Error("5th account (e=90) should be dropped by the iconMaxBars cap")
	}
	if countColor(img, barColorFor(10, false)) == 0 {
		t.Error("the first four (green) accounts should render")
	}
}

func TestUsageIconPNG_ZeroRendersTrackOnly(t *testing.T) {
	now := time.Now()
	raw, ok := usageIconPNG(fiveHourStats(map[string]float64{"a": 0}, now), now)
	if !ok {
		t.Fatal("a known 0%% utilization is still renderable (track only)")
	}
	img := decodePNG(t, raw)
	if countColor(img, iconTrackColor) == 0 {
		t.Error("0%% account should still draw the track")
	}
	if countColor(img, barColorFor(0, false)) != 0 {
		t.Error("0%% account must draw no fill")
	}
}

func TestUsageIconPNG_TinyUtilShowsSliver(t *testing.T) {
	now := time.Now()
	raw, ok := usageIconPNG(fiveHourStats(map[string]float64{"a": 1}, now), now)
	if !ok {
		t.Fatal("expected an icon")
	}
	if countColor(decodePNG(t, raw), barColorFor(1, false)) == 0 {
		t.Error("a present-but-tiny 1%% utilization should still show a fill sliver")
	}
}
