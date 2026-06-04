package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/atstats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/bm"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/claudestats"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/config"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/mcphttp"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/server"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/tray"
)

// version is set at release build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("gnome-topbar-daemon", version)
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	home, err := os.UserHomeDir()
	if err != nil {
		log.Error("resolve home directory", "err", err)
		os.Exit(1)
	}
	cfgDir := filepath.Join(home, ".config", "gnome-topbar")
	stateDir := filepath.Join(home, ".local", "state", "gnome-topbar")
	cfg, err := config.Load(filepath.Join(cfgDir, "config.toml"), cfgDir)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if cfg.BMUsername == "" {
		log.Warn("bm_username is empty; Basic Memory todos and currently-working-on will not resolve — set it in ~/.config/gnome-topbar/config.toml")
	}

	store, err := state.LoadStore(filepath.Join(stateDir, "seen.json"))
	if err != nil {
		log.Error("store", "err", err)
		os.Exit(1)
	}

	rest, err := api.DefaultRESTClient()
	if err != nil {
		log.Error("github auth (is gh logged in?)", "err", err)
		os.Exit(1)
	}
	gh := github.New(rest)
	mc := mcphttp.New(cfg.BMURL, cfg.BMToken, &http.Client{Timeout: 20 * time.Second})
	bmc := bm.New(mc, cfg.BMProject)

	p := &Poller{
		log: log, cfg: cfg, store: store, gh: gh, bm: bmc,
		snap: state.Snapshot{Sources: map[string]state.SourceStatus{}},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	p.refreshGitHub(ctx)
	go p.loop(ctx, time.Duration(cfg.GitHubIntervalSec)*time.Second, p.refreshGitHub)

	// Skip Basic Memory polling entirely when bm_username is unset — the reads
	// can't resolve, so polling would just produce guaranteed failures. Mark the
	// source so the tray shows why instead of looking healthy-but-empty.
	if cfg.BMUsername != "" {
		p.refreshBM(ctx)
		go p.loop(ctx, time.Duration(cfg.BMIntervalSec)*time.Second, p.refreshBM)
		go p.morningSweep(ctx)
	} else {
		p.mu.Lock()
		p.snap.Sources["basic-memory"] = state.SourceStatus{OK: false, Error: "bm_username not set"}
		p.mu.Unlock()
	}

	p.refreshAntiTangent(ctx)
	go p.loop(ctx, time.Duration(cfg.BMIntervalSec)*time.Second, p.refreshAntiTangent)

	p.refreshClaudeStats(ctx)
	go p.loop(ctx, time.Duration(cfg.BMIntervalSec)*time.Second, p.refreshClaudeStats)

	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", cfg.ListenPort))
	srv := &http.Server{Addr: addr, Handler: server.New(p, cfg.APIToken)}
	go func() {
		log.Info("debug http listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("serve", "err", err)
		}
	}()

	tr := tray.New(p, func(url string) {
		if err := tray.OpenURIOnHost(url); err != nil {
			log.Error("open url", "url", url, "err", err)
		}
	}, func(ids []string) { p.Ack(ids) }, tray.Actions{})

	tr.Run(ctx) // blocks until Quit / ctx cancel
	cancel()
	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopShutdown()
	_ = srv.Shutdown(shutdownCtx)
}

type Poller struct {
	log   *slog.Logger
	cfg   config.Config
	store *state.Store
	gh    *github.Source
	bm    *bm.Client

	mu   sync.RWMutex
	snap state.Snapshot
}

func (p *Poller) loop(ctx context.Context, every time.Duration, fn func(context.Context)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

func (p *Poller) morningSweep(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	done := -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			if now.Hour() == p.cfg.MorningSweepHour && done != now.YearDay() {
				done = now.YearDay()
				p.refreshBM(ctx)
			}
		}
	}
}

// refreshGitHub polls GitHub. ctx is part of the loop's func signature but is
// not threaded through: go-gh's REST client does not accept a context.
func (p *Poller) refreshGitHub(ctx context.Context) {
	authored, err1 := p.gh.FetchAuthored()
	reviews, err2 := p.gh.FetchReviewRequested()
	p.mu.Lock()
	defer p.mu.Unlock()
	st := state.SourceStatus{OK: true}
	if err1 != nil || err2 != nil {
		st = staleStatus(err1, err2)
		p.log.Warn("github refresh failed", "authored_err", err1, "review_err", err2)
	} else {
		p.snap.PRs.Authored = authored
		p.snap.PRs.ReviewRequested = reviews
	}
	p.snap.Sources["github"] = st
	p.recompute()
}

func (p *Poller) refreshBM(ctx context.Context) {
	todoMD, errT := p.bm.ReadNote(ctx, p.cfg.BMUsername+"/todo/main")
	nowMD, errN := p.bm.ReadNote(ctx, p.cfg.BMUsername+"/notes/currently-working-on/main")
	p.mu.Lock()
	defer p.mu.Unlock()
	st := state.SourceStatus{OK: true}
	if errT != nil {
		st = staleStatus(errT, nil)
		p.log.Warn("basic-memory todo read failed", "err", errT)
	} else {
		active, due := bm.ParseTodos(todoMD, time.Now())
		p.snap.Todos.Active = active
		p.snap.Todos.Due = due
	}
	if errN == nil {
		p.snap.NowWorking = bm.ParseNowWorking(nowMD)
	} else {
		// a failed read of either note degrades the source — don't report OK
		st = staleStatus(errT, errN)
		p.log.Warn("basic-memory currently-working-on read failed", "err", errN)
	}
	p.snap.Sources["basic-memory"] = st
	p.recompute()
}

// refreshAntiTangent reads the on-disk stats. ctx is part of the loop's func
// signature but unused: atstats.Read is a local file read.
func (p *Poller) refreshAntiTangent(ctx context.Context) {
	s := atstats.Read(p.cfg.StatsDir)
	p.mu.Lock()
	p.snap.AntiTangent = s
	p.snap.GeneratedAt = time.Now()
	p.mu.Unlock()
}

// refreshClaudeStats reads claude-stats.json from the same StatsDir. ctx is
// unused (local file read). Independent of anti-tangent's own rollup.json per
// the contract. A present-but-broken file (corrupt / unsupported major /
// oversized) registers a failing "claude-stats" source so the tray's "⚠ source"
// row and a stderr Warn make it diagnosable; a legitimately absent file leaves
// no row (the feature is simply off).
func (p *Poller) refreshClaudeStats(ctx context.Context) {
	s, err := claudestats.Read(p.cfg.StatsDir)
	p.mu.Lock()
	p.snap.ClaudeStats = s
	switch {
	case err != nil:
		p.snap.Sources["claude-stats"] = state.SourceStatus{OK: false, Error: err.Error()}
		p.log.Warn("claude-stats read failed", "err", err)
	case s.Present:
		p.snap.Sources["claude-stats"] = state.SourceStatus{OK: true}
	default:
		delete(p.snap.Sources, "claude-stats")
	}
	p.snap.GeneratedAt = time.Now()
	p.mu.Unlock()
}

// recompute refreshes events + timestamp; caller holds p.mu.
func (p *Poller) recompute() {
	p.snap.GeneratedAt = time.Now()
	p.snap.UnackedEvents = state.ComputeEvents(&p.snap, p.store)
}

func staleStatus(errs ...error) state.SourceStatus {
	now := time.Now()
	for _, e := range errs {
		if e != nil {
			return state.SourceStatus{OK: false, Error: e.Error(), StaleSince: &now}
		}
	}
	return state.SourceStatus{OK: true}
}

// server.Provider implementation.
func (p *Poller) Snapshot() state.Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snap := p.snap
	// Sources is mutated in place by the refresh goroutines, so returning the
	// snapshot by value would share the live map and race with a concurrent
	// reader (e.g. the debug HTTP JSON encoder). Copy it. All other fields are
	// reassigned whole on each refresh, so sharing them by value is safe.
	snap.Sources = make(map[string]state.SourceStatus, len(p.snap.Sources))
	for k, v := range p.snap.Sources {
		snap.Sources[k] = v
	}
	return snap
}

// RefreshNow satisfies tray.Provider: force an immediate poll of all sources.
func (p *Poller) RefreshNow(ctx context.Context) {
	p.refreshGitHub(ctx)
	p.refreshBM(ctx)
	p.refreshAntiTangent(ctx)
	p.refreshClaudeStats(ctx)
}

func (p *Poller) Search(ctx context.Context, q string) ([]bm.SearchResult, error) {
	return p.bm.SearchEpicsStories(ctx, q)
}

// ReadNote returns the raw markdown of a Basic Memory note (used by the note view).
func (p *Poller) ReadNote(ctx context.Context, identifier string) (string, error) {
	return p.bm.ReadNote(ctx, identifier)
}

// AppendTodo adds a bullet to the rolling todo note, then refreshes BM so the
// tray reflects it on the next render.
func (p *Poller) AppendTodo(ctx context.Context, text string) error {
	if p.cfg.BMUsername == "" {
		return fmt.Errorf("bm_username not set")
	}
	if err := p.bm.AppendTodo(ctx, p.cfg.BMUsername, text); err != nil {
		return err
	}
	p.refreshBM(ctx)
	return nil
}

// MarkTodoDone ticks a specific bullet, then refreshes BM.
func (p *Poller) MarkTodoDone(ctx context.Context, rawLine string) error {
	if p.cfg.BMUsername == "" {
		return fmt.Errorf("bm_username not set")
	}
	if err := p.bm.MarkTodoDone(ctx, p.cfg.BMUsername, rawLine, time.Now()); err != nil {
		return err
	}
	p.refreshBM(ctx)
	return nil
}

func (p *Poller) Ack(ids []string) {
	if err := p.store.MarkSeen(ids); err != nil {
		p.log.Warn("ack persist failed (events may re-notify after restart)", "err", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recompute()
}
