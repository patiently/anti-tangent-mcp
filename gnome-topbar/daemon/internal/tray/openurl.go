package tray

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// OpenURIOnHost opens url in the host's default browser via the desktop portal
// (org.freedesktop.portal.Desktop), which is reachable on the shared session
// bus. Best-effort: errors are returned for logging, never fatal.
func OpenURIOnHost(url string) error {
	// Only hand ordinary web links to the host opener — refuse other schemes
	// (file://, etc.) as defense-in-depth even though callers pass PR URLs.
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("refusing to open non-http(s) URL: %q", url)
	}
	// SessionBus returns a shared, lazily-initialized connection (cached by
	// godbus), not a per-call socket open.
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	obj := conn.Object("org.freedesktop.portal.Desktop", dbus.ObjectPath("/org/freedesktop/portal/desktop"))
	// OpenURI(parent_window string, uri string, options a{sv}) -> handle o
	call := obj.Call("org.freedesktop.portal.OpenURI.OpenURI", 0, "", url, map[string]dbus.Variant{})
	return call.Err
}

// OpenLocal opens a loopback URL in the in-container browser (Chrome over X11)
// via xdg-open. The daemon's own UI pages live on the container's 127.0.0.1,
// which the host browser cannot reach, so these must NOT go through the host
// portal. Restricted to the http loopback host as defense-in-depth (parsed, not
// prefix-matched, so lookalike hosts like localhost.evil.com are refused).
// Non-blocking.
func OpenLocal(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return fmt.Errorf("refusing non-loopback URL: %q", rawURL)
	}
	cmd := exec.Command("xdg-open", rawURL)
	// Detach into a new session so the browser isn't tied to the daemon's
	// process group, and reap the launcher in a goroutine so it doesn't linger
	// as a zombie — the daemon is long-lived, so a bare Start() without Wait()
	// would accumulate defunct xdg-open children over a session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
