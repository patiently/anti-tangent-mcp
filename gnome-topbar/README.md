# gnome-topbar

A GNOME Shell top-bar extension showing your GitHub PRs, Basic Memory todos,
epic/story search, and a "currently working on" summary, backed by a small Go
daemon. See `../docs/superpowers/specs/2026-06-02-gnome-topbar-mvp-design.md`.

## Prerequisites
- GNOME Shell 45/46/47 (Wayland or X11)
- `gh` CLI logged in (`gh auth status`)
- Basic Memory reachable; `BM_URL` + `BM_BEARER_TOKEN` available
- Go 1.25 to build

## Install
1. `cp config.example.toml ~/.config/gnome-topbar/config.toml` and set `bm_username`.
2. Create `~/.config/gnome-topbar/env`:
   ```
   BM_URL=...
   BM_BEARER_TOKEN=...
   ```
3. `cd packaging && make install enable`
4. `gnome-extensions enable gnome-topbar@localhost` (log out/in if the shell
   doesn't pick it up on Wayland).

## Currently-working-on note
Add to your AI assistant config (e.g. `~/.claude/CLAUDE.md`): when you start or
switch tasks, update the Basic Memory note `<username>/notes/currently-working-on/main`
with frontmatter `updated: <RFC3339 timestamp>` and a 1–3 sentence body. The
panel renders the body with a staleness indicator.
