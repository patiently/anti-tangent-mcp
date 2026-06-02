DAEMON_BIN := $(HOME)/.local/bin/gnome-topbar-daemon
UNIT := gnome-topbar-daemon.service

.PHONY: build run install-daemon install enable logs

build:
	mkdir -p $(dir $(DAEMON_BIN))
	cd ../daemon && go build -o $(DAEMON_BIN) ./cmd/gnome-topbar-daemon

run: build
	$(DAEMON_BIN)

# One binary (data layer + tray). No gnome-shell extension — the tray
# registers on the session bus at runtime.
install-daemon: build
	mkdir -p $(HOME)/.config/systemd/user
	cp systemd/$(UNIT) $(HOME)/.config/systemd/user/$(UNIT)
	systemctl --user daemon-reload

install: install-daemon

enable:
	systemctl --user enable --now $(UNIT)

logs:
	journalctl --user -u $(UNIT) -f
