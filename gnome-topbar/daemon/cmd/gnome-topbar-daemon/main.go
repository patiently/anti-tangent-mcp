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
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/config"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/github"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/mcphttp"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/server"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/state"
	"github.com/patiently/anti-tangent-mcp/gnome-topbar/daemon/internal/tray"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	home, _ := os.UserHomeDir()
	cfgDir := filepath.Join(home, ".config", "gnome-topbar")
	stateDir := filepath.Join(home, ".local", "state", "gnome-topbar")
	cfg, err := config.Load(filepath.Join(cfgDir, "config.toml"), cfgDir)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
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
	p.refreshBM(ctx)
	p.refreshAntiTangent(ctx)
	go p.loop(ctx, time.Duration(cfg.GitHubIntervalSec)*time.Second, p.refreshGitHub)
	go p.loop(ctx, time.Duration(cfg.BMIntervalSec)*time.Second, p.refreshBM)
	go p.loop(ctx, time.Duration(cfg.BMIntervalSec)*time.Second, p.refreshAntiTangent)
	go p.morningSweep(ctx)

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
	}, func(ids []string) { p.Ack(ids) })

	tr.Run(ctx) // blocks until Quit / ctx cancel
	cancel()
	sc, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	_ = srv.Shutdown(sc)
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

func (p *Poller) refreshGitHub(ctx context.Context) {
	authored, err1 := p.gh.FetchAuthored()
	reviews, err2 := p.gh.FetchReviewRequested()
	p.mu.Lock()
	defer p.mu.Unlock()
	st := state.SourceStatus{OK: true}
	if err1 != nil || err2 != nil {
		st = staleStatus(err1, err2)
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
	} else {
		active, due := bm.ParseTodos(todoMD, time.Now())
		p.snap.Todos.Active = active
		p.snap.Todos.Due = due
	}
	if errN == nil {
		p.snap.NowWorking = bm.ParseNowWorking(nowMD)
	}
	p.snap.Sources["basic-memory"] = st
	p.recompute()
}

func (p *Poller) refreshAntiTangent(ctx context.Context) {
	s := atstats.Read(p.cfg.StatsDir)
	p.mu.Lock()
	p.snap.AntiTangent = s
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
	return p.snap
}

// RefreshNow satisfies tray.Provider: force an immediate poll of all sources.
func (p *Poller) RefreshNow(ctx context.Context) {
	p.refreshGitHub(ctx)
	p.refreshBM(ctx)
	p.refreshAntiTangent(ctx)
}

func (p *Poller) Search(ctx context.Context, q string) ([]bm.SearchResult, error) {
	return p.bm.SearchEpicsStories(ctx, q)
}

func (p *Poller) Ack(ids []string) {
	_ = p.store.MarkSeen(ids)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recompute()
}
