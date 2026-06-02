package tray

import (
	"github.com/godbus/dbus/v5"
)

// Notify raises a host desktop notification via org.freedesktop.Notifications.
// Returns the notification id (or error). Best-effort.
func Notify(summary, body string) (uint32, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, err
	}
	obj := conn.Object("org.freedesktop.Notifications", dbus.ObjectPath("/org/freedesktop/Notifications"))
	// Notify(app_name, replaces_id, app_icon, summary, body, actions, hints, expire_timeout)
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"gnome-topbar", uint32(0), "", summary, body,
		[]string{}, map[string]dbus.Variant{}, int32(-1))
	if call.Err != nil {
		return 0, call.Err
	}
	var id uint32
	_ = call.Store(&id)
	return id, nil
}
