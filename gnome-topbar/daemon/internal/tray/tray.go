package tray

import (
	"context"
	"fmt"
	"sort"
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

// Actions are the side-effecting callbacks the tray triggers on clicks. Any
// field may be nil (treated as a no-op) so tests can construct a bare Tray.
type Actions struct {
	OpenSearch  func()               // open the search page in the in-container browser
	OpenNewTodo func()               // open the new-todo page
	MarkDone    func(rawLine string) // tick a todo bullet in Basic Memory
	OpenStats   func()               // open the /ui/stats detail page
	OpenClaude  func()               // open the /ui/claude detail page
}

// Per-section item-pool caps. The systray menu is append-only, so we
// pre-allocate slots once and Show/Hide them each refresh.
const (
	capReviewReq = 15
	capMyPRs     = 40
	capDue       = 15
	capActive    = 40
	capStat      = 3
	capErr       = 4  // per-source error rows
	capClaude    = 8  // per-account bar overview rows (+ stale marker)
	capClaudeUse = 30 // Claude usage submenu detail rows across accounts

	renderInterval = 30 * time.Second
)

type Tray struct {
	prov   Provider
	cancel context.CancelFunc
	opener func(url string)
	ack    func(ids []string)
	act    Actions
	raised map[string]bool
	mu     sync.Mutex

	nowItem *systray.MenuItem
	errPool []*systray.MenuItem // per-source error rows (shown only on failure)

	rrParent *systray.MenuItem // collapsible submenu (hidden when empty)
	rrPool   []*systray.MenuItem
	rrURLs   []string

	myPRsParent *systray.MenuItem // collapsible submenu (hidden when empty)
	myPRsPool   []*systray.MenuItem
	myPRsURLs   []string

	dueParent *systray.MenuItem // collapsible submenu (hidden when empty)
	duePool   []*systray.MenuItem
	dueRaw    []string

	activeParent *systray.MenuItem // collapsible submenu (hidden when empty)
	activePool   []*systray.MenuItem
	activeRaw    []string

	statsParent *systray.MenuItem // collapsible submenu (hidden when absent)
	statPool    []*systray.MenuItem

	claudePool      []*systray.MenuItem // inline Claude per-account bar overview
	claudeParent    *systray.MenuItem   // collapsible "Claude usage" submenu
	claudeUsagePool []*systray.MenuItem

	statsDetailItem  *systray.MenuItem // opens /ui/stats (shown only when AntiTangent stats present)
	claudeDetailItem *systray.MenuItem // opens /ui/claude (shown only when Claude stats present)

	refreshItem *systray.MenuItem
	quitItem    *systray.MenuItem
}

// New returns a Tray. opener opens a URL on the host; ack marks event IDs seen
// after notifications are raised (may be nil).
func New(p Provider, opener func(string), ack func([]string), act Actions) *Tray {
	return &Tray{prov: p, opener: opener, ack: ack, act: act, raised: map[string]bool{}}
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

	// Quick actions pinned to the top of the menu (closest the dbusmenu model
	// gets to "top-right corner buttons"; true top-bar buttons need a host
	// GNOME Shell extension — out of scope).
	t.refreshItem = systray.AddMenuItem("↻ Refresh", "force an immediate poll")
	t.quitItem = systray.AddMenuItem("✕ Quit", "")
	searchItem := systray.AddMenuItem("🔎 Search epics/stories…", "open BM search in the browser")
	newTodoItem := systray.AddMenuItem("➕ New todo…", "create a todo in Basic Memory")
	systray.AddSeparator()
	go func() {
		for range searchItem.ClickedCh {
			if t.act.OpenSearch != nil {
				t.act.OpenSearch()
			}
		}
	}()
	go func() {
		for range newTodoItem.ClickedCh {
			if t.act.OpenNewTodo != nil {
				t.act.OpenNewTodo()
			}
		}
	}()

	// currently-working-on (inline header)
	t.nowItem = systray.AddMenuItem("", "")
	t.nowItem.Disable()

	// per-source error rows, shown only when a source is failing
	t.errPool = t.makeDisabledPool(capErr, nil)

	// Review requested — collapsed submenu (hidden when count is 0)
	t.rrParent = systray.AddMenuItem("🔵 Review requested", "PRs awaiting your review")
	t.rrParent.Hide()
	t.rrPool, t.rrURLs = t.makeClickPool(capReviewReq, t.rrParent)

	// My open PRs — collapsed submenu (hidden when count is 0)
	t.myPRsParent = systray.AddMenuItem("🟣 My open PRs", "your open pull requests")
	t.myPRsParent.Hide()
	t.myPRsPool, t.myPRsURLs = t.makeClickPool(capMyPRs, t.myPRsParent)

	// Due / overdue todos — collapsed submenu (hidden when count is 0)
	t.dueParent = systray.AddMenuItem("✅ Due / overdue", "todos due or overdue")
	t.dueParent.Hide()
	t.duePool, t.dueRaw = t.makeDonePool(capDue, t.dueParent)

	// Active todos — collapsed submenu (hidden when count is 0)
	t.activeParent = systray.AddMenuItem("📋 Active todos", "your active todos")
	t.activeParent.Hide()
	t.activePool, t.activeRaw = t.makeDonePool(capActive, t.activeParent)

	// anti-tangent / CodeScene stats — collapsed submenu, shown only when present,
	// with a "details…" item right after it that opens the full /ui/stats page.
	t.statsParent = systray.AddMenuItem("📊 Stats", "anti-tangent / CodeScene stats")
	t.statsParent.Hide()
	t.statPool = t.makeDisabledPool(capStat, t.statsParent)
	t.statsDetailItem = systray.AddMenuItem("📊 Stats details…", "open the stats detail page")
	t.statsDetailItem.Hide()
	go func() {
		for range t.statsDetailItem.ClickedCh {
			if t.act.OpenStats != nil {
				t.act.OpenStats()
			}
		}
	}()

	// Claude usage — inline per-account bar overview (shown only when present), a
	// collapsed submenu with per-account detail, and a "details…" item (right after
	// it) that opens the full /ui/claude page.
	t.claudePool = t.makeDisabledPool(capClaude, nil)
	t.claudeParent = systray.AddMenuItem("🤖 Claude usage", "Claude Code usage + rate limits")
	t.claudeParent.Hide()
	t.claudeUsagePool = t.makeDisabledPool(capClaudeUse, t.claudeParent)
	t.claudeDetailItem = systray.AddMenuItem("🤖 Claude usage details…", "open the Claude usage detail page")
	t.claudeDetailItem.Hide()
	go func() {
		for range t.claudeDetailItem.ClickedCh {
			if t.act.OpenClaude != nil {
				t.act.OpenClaude()
			}
		}
	}()

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
		tk := time.NewTicker(renderInterval)
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

// makeDonePool creates n hidden clickable items under parent; clicking one calls
// Actions.MarkDone with the raw todo line backing that slot, then re-renders.
func (t *Tray) makeDonePool(n int, parent *systray.MenuItem) ([]*systray.MenuItem, []string) {
	pool := make([]*systray.MenuItem, n)
	raw := make([]string, n)
	for i := 0; i < n; i++ {
		mi := t.addItem(parent)
		mi.Hide()
		pool[i] = mi
		idx := i
		go func() {
			for range mi.ClickedCh {
				t.mu.Lock()
				line := raw[idx]
				t.mu.Unlock()
				if line != "" && t.act.MarkDone != nil {
					t.act.MarkDone(line)
					t.render()
				}
			}
		}()
	}
	return pool, raw
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
	fillSourceErrors(t.errPool, snap.Sources)

	t.rrParent.SetTitle(fmt.Sprintf("🔵 Review requested (%d)", len(snap.PRs.ReviewRequested)))
	fillPRPool(t.rrPool, t.rrURLs, snap.PRs.ReviewRequested)
	showIf(t.rrParent, len(snap.PRs.ReviewRequested) > 0)

	t.myPRsParent.SetTitle(fmt.Sprintf("🟣 My open PRs (%d)", len(snap.PRs.Authored)))
	fillPRPool(t.myPRsPool, t.myPRsURLs, snap.PRs.Authored)
	showIf(t.myPRsParent, len(snap.PRs.Authored) > 0)

	t.dueParent.SetTitle(fmt.Sprintf("✅ Due / overdue (%d)", len(snap.Todos.Due)))
	fillTodoPool(t.duePool, t.dueRaw, snap.Todos.Due, "⚠ ")
	showIf(t.dueParent, len(snap.Todos.Due) > 0)

	t.activeParent.SetTitle(fmt.Sprintf("📋 Active todos (%d)", len(snap.Todos.Active)))
	fillTodoPool(t.activePool, t.activeRaw, snap.Todos.Active, "")
	showIf(t.activeParent, len(snap.Todos.Active) > 0)

	var stats []string
	if at := snap.AntiTangent; at.Present {
		stats = append(stats, antiTangentLabel(at))
		if at.CodeScene != nil {
			stats = append(stats, codeSceneLabel(at.CodeScene))
		}
	}
	fillLabelPool(t.statPool, stats)
	showIf(t.statsParent, len(stats) > 0)
	showIf(t.statsDetailItem, snap.AntiTangent.Present)

	// Claude usage: inline per-account bar overview + per-account detail submenu.
	fillLabelPool(t.claudePool, claudeOverviewLabels(snap.ClaudeStats, now))
	fillLabelPool(t.claudeUsagePool, claudeUsageRows(snap.ClaudeStats, now))
	if cs := snap.ClaudeStats; cs.Present && len(cs.Accounts) > 0 {
		t.claudeParent.SetTitle(fmt.Sprintf("🤖 Claude usage (%d)", len(cs.Accounts)))
		t.claudeParent.Show()
	} else {
		t.claudeParent.Hide()
	}
	showIf(t.claudeDetailItem, snap.ClaudeStats.Present && len(snap.ClaudeStats.Accounts) > 0)
	// Dedup notifications under the lock (render runs from the ticker, the
	// Refresh click, and startup concurrently — t.raised must not be touched
	// without t.mu). Raise the actual notifications after unlocking, since
	// Notify does DBus I/O.
	toRaise := selectUnraised(t.raised, snap.UnackedEvents)
	t.mu.Unlock()

	if n := len(snap.PRs.ReviewRequested) + len(snap.Todos.Due); n > 0 {
		systray.SetTooltip("gnome-topbar · " + strconv.Itoa(n) + " need attention")
	} else {
		systray.SetTooltip("gnome-topbar")
	}

	// Reflect Claude usage in the tray icon itself: one bar per account, height ∝
	// its worst rate-limit window, green/amber/red by threshold (gray when stale).
	// Falls back to the static icon when there are no usage stats to show.
	if icon, ok := usageIconPNG(snap.ClaudeStats, now); ok {
		systray.SetIcon(icon)
	} else {
		systray.SetIcon(trayIcon)
	}

	// Ack only events whose notification was actually delivered, so a failed
	// Notify isn't marked seen (it re-notifies after a restart instead of being
	// silently lost).
	var delivered []string
	for _, ev := range toRaise {
		title := "Todo due"
		if ev.Kind == "review_request" {
			title = "Review requested: " + ev.Title
		}
		if _, err := Notify(title, ev.Body); err == nil {
			delivered = append(delivered, ev.ID)
		}
	}
	if len(delivered) > 0 && t.ack != nil {
		t.ack(delivered)
	}
}

// selectUnraised marks each not-yet-raised event's ID in raised and returns
// those events. The caller must hold the mutex guarding raised. Extracted so
// the notification dedup is unit-testable without the systray tree.
func selectUnraised(raised map[string]bool, events []state.Event) []state.Event {
	var out []state.Event
	for _, ev := range events {
		if !raised[ev.ID] {
			raised[ev.ID] = true
			out = append(out, ev)
		}
	}
	return out
}

// showIf shows mi when cond is true, else hides it — used to drop a zero-count
// submenu from the menu entirely (caller holds t.mu).
func showIf(mi *systray.MenuItem, cond bool) {
	if cond {
		mi.Show()
	} else {
		mi.Hide()
	}
}

// fillSourceErrors shows one "⚠ <source>: <reason>" row per failing source
// (caller holds t.mu); slots for healthy/absent sources are hidden. Sources are
// listed in a stable order so rows don't reshuffle between refreshes.
func fillSourceErrors(pool []*systray.MenuItem, sources map[string]state.SourceStatus) {
	var failing []string
	for name, st := range sources {
		if !st.OK {
			failing = append(failing, name)
		}
	}
	sort.Strings(failing)
	for i, mi := range pool {
		if i < len(failing) {
			mi.SetTitle("⚠ " + failing[i] + ": " + oneLine(sources[failing[i]].Error, labelWidth))
			mi.Show()
		} else {
			mi.Hide()
		}
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

func fillTodoPool(pool []*systray.MenuItem, raw []string, todos []bm.TodoItem, prefix string) {
	for i, mi := range pool {
		if i < len(todos) {
			mi.SetTitle(prefix + oneLine(todos[i].Text, labelWidth))
			raw[i] = todos[i].Raw
			mi.Show()
		} else {
			raw[i] = ""
			mi.Hide()
		}
	}
}

// fillLabelPool shows one display-only row per label (caller holds t.mu),
// hiding the unused slots. Labels beyond the pool's capacity are dropped.
func fillLabelPool(pool []*systray.MenuItem, labels []string) {
	for i, mi := range pool {
		if i < len(labels) {
			mi.SetTitle(oneLine(labels[i], labelWidth))
			mi.Show()
		} else {
			mi.Hide()
		}
	}
}
