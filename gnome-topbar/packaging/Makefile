DAEMON_BIN := $(HOME)/.local/bin/gnome-topbar-daemon
EXT_UUID := gnome-topbar@localhost
EXT_DIR := $(HOME)/.local/share/gnome-shell/extensions/$(EXT_UUID)
UNIT := gnome-topbar-daemon.service

.PHONY: build run install-daemon install-extension install enable logs

build:
	cd ../daemon && go build -o $(DAEMON_BIN) ./cmd/gnome-topbar-daemon

run: build
	$(DAEMON_BIN)

install-daemon: build
	mkdir -p $(HOME)/.config/systemd/user
	cp systemd/$(UNIT) $(HOME)/.config/systemd/user/$(UNIT)
	systemctl --user daemon-reload

install-extension:
	mkdir -p $(EXT_DIR)
	cp -r ../extension/* $(EXT_DIR)/

install: install-daemon install-extension

enable:
	systemctl --user enable --now $(UNIT)

logs:
	journalctl --user -u $(UNIT) -f
