package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sort"
	"time"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
)

// Usage-icon geometry. Rendered at ~2x the typical 22px top-bar slot so it stays
// crisp when the StatusNotifier host scales it down.
const (
	iconSize        = 44   // square canvas, px
	iconMaxBars     = 4    // legibility ceiling at top-bar size
	iconMargin      = 4    // padding around the bar group
	iconBarGap      = 3    // gap between bars
	iconMinFillFrac = 0.08 // a present-but-tiny utilization still shows a sliver
)

// iconTrackColor is the faint full-height bar background (matches the dark UI
// panel), so the unfilled portion reads as headroom.
var iconTrackColor = color.RGBA{0x2c, 0x31, 0x3c, 0xff}

// barColorFor maps a utilization percent to the same severity as the menu bars
// (utilYellowPct / utilWarnPct, shared from claude.go). A stale snapshot dims to
// gray so the bars don't read as live.
func barColorFor(pct float64, stale bool) color.RGBA {
	switch {
	case stale:
		return color.RGBA{0x6b, 0x72, 0x80, 0xff} // gray
	case pct >= utilWarnPct:
		return color.RGBA{0xe5, 0x39, 0x35, 0xff} // red
	case pct >= utilYellowPct:
		return color.RGBA{0xe6, 0xb0, 0x00, 0xff} // amber
	default:
		return color.RGBA{0x3f, 0xb9, 0x50, 0xff} // green
	}
}

// accountWorstUtil returns the higher of an account's 5h / weekly window
// utilizations and whether either was known. Unknown (nil) windows are skipped.
func accountWorstUtil(a claudestats.Account) (float64, bool) {
	if a.Limits == nil {
		return 0, false
	}
	worst, ok := 0.0, false
	for _, w := range []*claudestats.Window{a.Limits.FiveHour, a.Limits.SevenDay} {
		if w != nil && w.Utilization != nil {
			if !ok || *w.Utilization > worst {
				worst = *w.Utilization
			}
			ok = true
		}
	}
	return worst, ok
}

// usageIconPNG renders the per-account Claude-usage bar icon as PNG bytes: one
// vertical bar per account that has a known rate-limit utilization, height ∝ that
// account's worst (5h vs weekly) window, colored green/amber/red by the menu
// thresholds (gray when the snapshot is stale), over a faint track so headroom
// reads visually. Accounts are ordered by key and capped at iconMaxBars.
//
// Returns ok=false when there is nothing to show (stats absent, or no account
// has a known utilization) so the caller falls back to the static icon.
func usageIconPNG(cs claudestats.Stats, now time.Time) ([]byte, bool) {
	if !cs.Present {
		return nil, false
	}
	keys := make([]string, 0, len(cs.Accounts))
	for k := range cs.Accounts {
		if _, ok := accountWorstUtil(cs.Accounts[k]); ok {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, false
	}
	sort.Strings(keys)
	if len(keys) > iconMaxBars {
		keys = keys[:iconMaxBars]
	}
	stale := cs.Stale(now)

	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize)) // transparent bg

	n := len(keys)
	span := iconSize - 2*iconMargin
	barW := (span - iconBarGap*(n-1)) / n
	if barW < 1 {
		barW = 1
	}
	x := iconMargin
	for _, k := range keys {
		util, _ := accountWorstUtil(cs.Accounts[k])
		frac := util / 100
		switch {
		case frac > 1:
			frac = 1
		case frac > 0 && frac < iconMinFillFrac:
			frac = iconMinFillFrac
		}
		fillH := int(float64(span) * frac)
		fillRect(img, x, iconMargin, barW, span, iconTrackColor)                         // full-height track
		fillRect(img, x, iconMargin+(span-fillH), barW, fillH, barColorFor(util, stale)) // bottom-anchored fill
		x += barW + iconBarGap
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// fillRect paints a solid w×h rectangle with top-left (x,y), clipped to img.
func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	b := img.Bounds()
	for yy := y; yy < y+h; yy++ {
		if yy < b.Min.Y || yy >= b.Max.Y {
			continue
		}
		for xx := x; xx < x+w; xx++ {
			if xx >= b.Min.X && xx < b.Max.X {
				img.SetRGBA(xx, yy, c)
			}
		}
	}
}
