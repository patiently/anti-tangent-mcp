package tray

import (
	"fmt"
	"strings"

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
