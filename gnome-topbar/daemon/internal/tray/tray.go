package tray

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
)

// Provider is the slice of the daemon the tray needs (implemented by *main.Poller).
type Provider interface {
	Snapshot() state.Snapshot
	RefreshNow(ctx context.Context) // force an immediate poll of all sources
}

// Per-section item-pool caps. The systray menu is append-only, so we
// pre-allocate slots once and Show/Hide them each refresh.
const (
	capReviewReq = 15
	capMyPRs     = 40
	capDue       = 15
	capActive    = 40
	capStat      = 3
)

type Tray struct {
	prov   Provider
	cancel context.CancelFunc
	opener func(url string)
	ack    func(ids []string)
	raised map[string]bool
	mu     sync.Mutex

	nowItem *systray.MenuItem

	rrHeader *systray.MenuItem
	rrPool   []*systray.MenuItem
	rrURLs   []string

	myPRsParent *systray.MenuItem // collapsible submenu (collapsed by default)
	myPRsPool   []*systray.MenuItem
	myPRsURLs   []string

	dueHeader *systray.MenuItem
	duePool   []*systray.MenuItem

	activeParent *systray.MenuItem // collapsible submenu (collapsed by default)
	activePool   []*systray.MenuItem

	statPool []*systray.MenuItem

	refreshItem *systray.MenuItem
	quitItem    *systray.MenuItem
}

// New returns a Tray. opener opens a URL on the host; ack marks event IDs seen
// after notifications are raised (may be nil).
func New(p Provider, opener func(string), ack func([]string)) *Tray {
	return &Tray{prov: p, opener: opener, ack: ack, raised: map[string]bool{}}
}

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

	// currently-working-on (inline header)
	t.nowItem = systray.AddMenuItem("", "")
	t.nowItem.Disable()
	// Review requested — inline (high priority, short)
	t.rrHeader = systray.AddMenuItem("", "")
	t.rrHeader.Disable()
	t.rrPool, t.rrURLs = t.makeClickPool(capReviewReq, nil)

	// My open PRs — collapsed submenu (the long, lower-priority list)
	t.myPRsParent = systray.AddMenuItem("🟣 My open PRs", "your open pull requests")
	t.myPRsPool, t.myPRsURLs = t.makeClickPool(capMyPRs, t.myPRsParent)

	// Due / overdue todos — inline (high priority)
	t.dueHeader = systray.AddMenuItem("", "")
	t.dueHeader.Disable()
	t.duePool = t.makeDisabledPool(capDue, nil)

	// Active todos — collapsed submenu
	t.activeParent = systray.AddMenuItem("📋 Active todos", "your active todos")
	t.activePool = t.makeDisabledPool(capActive, t.activeParent)

	// anti-tangent / CodeScene stats — inline, shown only when present
	t.statPool = t.makeDisabledPool(capStat, nil)

	t.refreshItem = systray.AddMenuItem("↻ Refresh", "")
	t.quitItem = systray.AddMenuItem("Quit", "")
	go func() {
		for range t.refreshItem.ClickedCh {
			t.prov.RefreshNow(ctx)
			t.render()
		}
	}()
	go func() {
		for range t.quitItem.ClickedCh {
			systray.Quit()
		}
	}()

	t.render()
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

// makeClickPool creates n hidden items (top-level if parent is nil, else under
// parent) whose clicks open the URL currently backing their slot.
func (t *Tray) makeClickPool(n int, parent *systray.MenuItem) ([]*systray.MenuItem, []string) {
	pool := make([]*systray.MenuItem, n)
	urls := make([]string, n)
	for i := 0; i < n; i++ {
		mi := t.addItem(parent)
		mi.Hide()
		pool[i] = mi
		idx := i
		go func() {
			for range mi.ClickedCh {
				t.mu.Lock()
				u := urls[idx]
				t.mu.Unlock()
				if u != "" {
					t.opener(u)
				}
			}
		}()
	}
	return pool, urls
}

// makeDisabledPool creates n hidden, non-clickable display-only items.
func (t *Tray) makeDisabledPool(n int, parent *systray.MenuItem) []*systray.MenuItem {
	pool := make([]*systray.MenuItem, n)
	for i := 0; i < n; i++ {
		mi := t.addItem(parent)
		mi.Disable()
		mi.Hide()
		pool[i] = mi
	}
	return pool
}

func (t *Tray) addItem(parent *systray.MenuItem) *systray.MenuItem {
	if parent == nil {
		return systray.AddMenuItem("", "")
	}
	return parent.AddSubMenuItem("", "")
}

func (t *Tray) render() {
	snap := t.prov.Snapshot()
	now := time.Now()

	t.mu.Lock()
	t.nowItem.SetTitle(nowWorkingLabel(snap.NowWorking, now))

	t.rrHeader.SetTitle(fmt.Sprintf("🔵 Review requested (%d)", len(snap.PRs.ReviewRequested)))
	fillPRPool(t.rrPool, t.rrURLs, snap.PRs.ReviewRequested)

	t.myPRsParent.SetTitle(fmt.Sprintf("🟣 My open PRs (%d)", len(snap.PRs.Authored)))
	fillPRPool(t.myPRsPool, t.myPRsURLs, snap.PRs.Authored)

	t.dueHeader.SetTitle(fmt.Sprintf("✅ Due / overdue (%d)", len(snap.Todos.Due)))
	fillTodoPool(t.duePool, snap.Todos.Due, "⚠ ")

	t.activeParent.SetTitle(fmt.Sprintf("📋 Active todos (%d)", len(snap.Todos.Active)))
	fillTodoPool(t.activePool, snap.Todos.Active, "")

	var stats []string
	if at := snap.AntiTangent; at.Present {
		stats = append(stats, antiTangentLabel(at))
		if at.CodeScene != nil {
			stats = append(stats, codeSceneLabel(at.CodeScene))
		}
	}
	for i, mi := range t.statPool {
		if i < len(stats) {
			mi.SetTitle(stats[i])
			mi.Show()
		} else {
			mi.Hide()
		}
	}
	t.mu.Unlock()

	if n := len(snap.PRs.ReviewRequested) + len(snap.Todos.Due); n > 0 {
		systray.SetTooltip("gnome-topbar · " + strconv.Itoa(n) + " need attention")
	} else {
		systray.SetTooltip("gnome-topbar")
	}

	for _, ev := range snap.UnackedEvents {
		if t.raised[ev.ID] {
			continue
		}
		t.raised[ev.ID] = true
		title := "Todo due"
		if ev.Kind == "review_request" {
			title = "Review requested: " + ev.Title
		}
		_, _ = Notify(title, ev.Body)
	}
	if ids := unackedIDs(snap); len(ids) > 0 && t.ack != nil {
		t.ack(ids)
	}
}

// fillPRPool maps PRs onto a pool (caller holds t.mu); urls[i] backs slot i's
// click. Excess slots are hidden.
func fillPRPool(pool []*systray.MenuItem, urls []string, prs []github.PR) {
	for i, mi := range pool {
		if i < len(prs) {
			mi.SetTitle(prLabel(prs[i].Repo, prs[i].Number, prs[i].Title))
			urls[i] = prs[i].URL
			mi.Show()
		} else {
			urls[i] = ""
			mi.Hide()
		}
	}
}

func fillTodoPool(pool []*systray.MenuItem, todos []bm.TodoItem, prefix string) {
	for i, mi := range pool {
		if i < len(todos) {
			mi.SetTitle(prefix + oneLine(todos[i].Text, 80))
			mi.Show()
		} else {
			mi.Hide()
		}
	}
}

func unackedIDs(s state.Snapshot) []string {
	ids := make([]string, 0, len(s.UnackedEvents))
	for _, e := range s.UnackedEvents {
		ids = append(ids, e.ID)
	}
	return ids
}
