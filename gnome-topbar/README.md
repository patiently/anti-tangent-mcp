# gnome-topbar

A GNOME top-bar tray showing your GitHub PRs, Basic Memory todos, a "currently
working on" summary, and (optionally) anti-tangent/CodeScene stats — backed by a
small Go daemon. See `../docs/superpowers/specs/2026-06-02-gnome-topbar-mvp-design.md`.

## Prerequisites
- GNOME Shell 45/46/47 (Wayland or X11)
- `gh` CLI logged in (`gh auth status`)
- Basic Memory reachable; `BM_URL` + `BM_BEARER_TOKEN` available
- Go 1.25 to build

## Run modes

### Sandbox (development / Claude Code session)

The sandbox shares the host session bus (`$DBUS_SESSION_BUS_ADDRESS`) and
display (`$DISPLAY`), so the tray appears on the host top bar without any
special setup:

1. Copy and fill in the config:
   ```bash
   cp config.example.toml ~/.config/gnome-topbar/config.toml
   # set bm_username in the file
   ```
2. Export the Basic Memory env vars (or add to your shell profile):
   ```bash
   export BM_URL=...
   export BM_BEARER_TOKEN=...
   ```
3. Start the tray:
   ```bash
   cd packaging && make run
   ```
   A tray icon appears on the host top bar. The process inherits
   `DBUS_SESSION_BUS_ADDRESS` and `DISPLAY` from the sandbox environment.

### Normal host (permanent install via systemd)

Runs the same binary as a `systemd --user` service on your host machine.
No GNOME Shell extension to install or enable.

1. Copy and fill in the config (same as above).
2. Install and enable the service:
   ```bash
   cd packaging && make install-daemon enable
   ```
3. Watch logs:
   ```bash
   make logs
   ```

To stop: `systemctl --user stop gnome-topbar-daemon`.

## Currently-working-on note

Add to your AI assistant config (e.g. `~/.claude/CLAUDE.md`): when you start or
switch tasks, update the Basic Memory note `<username>/notes/currently-working-on/main`
with frontmatter `updated: <RFC3339 timestamp>` and a 1–3 sentence body. The
tray renders the body with a staleness indicator.
