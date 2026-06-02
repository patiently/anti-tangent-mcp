package tray

import (
	"context"
	"strconv"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

// Provider is the slice of the daemon the tray needs (implemented by *main.Poller).
type Provider interface {
	Snapshot() state.Snapshot
	RefreshNow(ctx context.Context) // force an immediate poll of all sources
}

const slots = 30 // max rendered rows per dynamic section pool

type Tray struct {
	prov   Provider
	cancel context.CancelFunc
	opener func(url string) // injected: open a URL on the host (Task T4)

	pool []*systray.MenuItem // flat pool of pre-allocated items, reused each refresh
	urls []string            // url backing each pool slot (for click handling)
	mu   sync.Mutex
}

// New returns a Tray. opener is the host open-URL function (tray/openurl.go).
func New(p Provider, opener func(string)) *Tray { return &Tray{prov: p, opener: opener} }

// Run blocks, running the systray event loop until Quit. Call on the main
// goroutine. ctx cancellation also stops it.
func (t *Tray) Run(ctx context.Context) {
	ctx, t.cancel = context.WithCancel(ctx)
	systray.Run(func() { t.onReady(ctx) }, func() {})
}

func (t *Tray) onReady(ctx context.Context) {
	systray.SetIcon(trayIcon)
	systray.SetTitle("")
	systray.SetTooltip("gnome-topbar")

	// Pre-allocate a flat pool of items + click handlers. Items beyond the
	// current row count are hidden each refresh (the lib's menu is append-only).
	for i := 0; i < slots; i++ {
		mi := systray.AddMenuItem("", "")
		mi.Hide()
		t.pool = append(t.pool, mi)
		t.urls = append(t.urls, "")
		idx := i
		go func() {
			for range mi.ClickedCh {
				t.onClick(ctx, idx)
			}
		}()
	}

	t.render() // immediate
	go func() {
		tk := time.NewTicker(30 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				systray.Quit()
				return
			case <-tk.C:
				t.render()
			}
		}
	}()
}

func (t *Tray) render() {
	snap := t.prov.Snapshot()
	rows := BuildMenu(snap, time.Now())
	t.mu.Lock()
	for i, mi := range t.pool {
		if i < len(rows) {
			r := rows[i]
			label := r.Label
			if r.Kind == RowSeparator {
				label = "────────"
			}
			mi.SetTitle(label)
			if r.Disabled {
				mi.Disable()
			} else {
				mi.Enable()
			}
			t.urls[i] = r.URL
			mi.Show()
		} else {
			mi.Hide()
			t.urls[i] = ""
		}
	}
	t.mu.Unlock()

	if n := len(snap.PRs.ReviewRequested) + len(snap.Todos.Due); n > 0 {
		systray.SetTooltip("gnome-topbar · " + strconv.Itoa(n) + " need attention")
	} else {
		systray.SetTooltip("gnome-topbar")
	}
	// (Task T4 appends notification-raising here, using `snap`.)
}

func (t *Tray) onClick(ctx context.Context, idx int) {
	t.mu.Lock()
	url := t.urls[idx]
	t.mu.Unlock()
	if url != "" {
		t.opener(url)
		return
	}
	// Non-URL rows: dispatch the current Refresh/Quit actions by row label.
	rows := BuildMenu(t.prov.Snapshot(), time.Now())
	if idx < len(rows) {
		switch rows[idx].Label {
		case "↻ Refresh":
			t.prov.RefreshNow(ctx)
			t.render()
		case "Quit":
			systray.Quit()
		}
	}
}
