# Basic Memory on a shared VM

Operator-facing setup guide for a team running [Basic Memory](https://github.com/basicmachines-co/basic-memory) (BM) v0.21.1 on a single shared VM, accessed from each developer's Claude Code over stdio-via-SSH. Pairs with [`INTEGRATION.md`](../../INTEGRATION.md) and the [`examples/project-knowledge/`](../../examples/project-knowledge/) note templates.

> This doc covers only the shared-VM topology. Solo developers running BM locally on their workstation should follow upstream BM's quickstart and skip this entire doc — `anti-tangent-mcp` has no opinion about local-only BM deployments.

---

## 1. What this doc is and isn't

**Is.** A paste-ready setup runbook for a senior SRE provisioning a shared BM VM for a small engineering team (target topology: 2–8 developers, one VM). Covers: VM baseline, BM install, remote-MCP transport via stdio-over-SSH, per-developer auth, git-backed storage of the markdown KB, day-2 ops, license compatibility, and the seven troubleshooting tickets you will actually get.

**Isn't.** A replacement for BM's upstream README. A guide for solo BM deployments. A multi-VM / HA topology. A how-to for any specific vault product (1Password, HashiCorp Vault, AWS Secrets Manager). A git-provider tutorial — pick whichever forge your team already uses and substitute it for the generic `git@<remote>:<org>/<repo>.git` placeholder throughout.

This doc is intentionally narrow: it documents the topology `anti-tangent-mcp` recommends for teams (one shared VM, git-backed markdown, per-dev SSH keys) and nothing else. If your team needs HTTP-based MCP transport, multi-region replication, or per-tenant isolation, treat this doc as a starting point and diverge as needed — anti-tangent-mcp itself never contacts BM directly, so it has no opinion on the network shape past §5.

---

## 2. Topology overview

```
+----------------------+         +---------------------------+
| dev-1 workstation    |  ssh    |  shared VM (`bm-host`)    |
|  Claude Code         | ------> |  user `bm`                |
|  ~/.ssh/basic-memory |         |  /usr/bin/basic-memory    |
+----------------------+         |  /var/lib/basic-memory/kb |
                                 |    (git working tree)     |
+----------------------+  ssh    |                           |
| dev-2 workstation    | ------> |  systemd timer            |
|  Claude Code         |         |    every 60s: commit+push |
+----------------------+         +---------------------------+
                                              |
                                              | git push (deploy key)
                                              v
                                 +---------------------------+
                                 | private git remote        |
                                 |  git@<remote>:<org>/<r>   |
                                 +---------------------------+
```

Key invariants:

- **`anti-tangent-mcp` does not talk to BM directly.** It is a separate advisory MCP server that runs in the developer's Claude Code session. The implementer agent talks to BM (over SSH) and to anti-tangent (locally) independently. No network path between the two services exists.
- **One process per session.** Each developer's Claude Code spawns a fresh `basic-memory mcp` stdio process over SSH per session. BM v0.21.1 is fine with multiple concurrent stdio sessions sharing the same data directory because writes go through SQLite + filesystem locks.
- **The KB directory is the source of truth.** BM's SQLite index is regenerated from the markdown on startup; only the `.md` files are committed.
- **Commits flow one direction.** The systemd timer pushes the VM's local commits to the remote. Developers do not push to that remote directly; if they want to hand-edit a note, they edit it via BM (which writes to the same working tree) and let the next timer tick commit it.

---

## 3. VM baseline

Sizing and OS guidance for a team of up to ~8 developers writing ~hundreds of notes/day:

- **OS.** Linux distribution with systemd (Ubuntu 22.04+ LTS, Debian 12+, RHEL 9+). The git-sync timer assumes systemd; for systemd-less hosts see the cron fallback in §8.
- **CPU / RAM.** 2 vCPU, 4 GiB RAM is comfortable. BM's heaviest operation is its full-text index rebuild on startup; that scales with KB size, not concurrent sessions.
- **Disk.** Provision the KB directory on its own volume (or an LVM logical volume) so you can snapshot the working tree without touching the OS root. 20 GiB is more than enough for the markdown KB itself; size up if you expect very large attached notes. The `.git/` directory grows linearly with commit count — run `git gc --aggressive` quarterly (§9).
- **Network.** Outbound TCP/22 to the git remote (for `git push`). Inbound TCP/22 from each developer's workstation (for SSH-based MCP). No other ports should be open to the team's VPN/intranet; nothing should be public.

Firewall rules (`ufw` shown; translate to your hosts' tooling):

```bash
ufw default deny incoming
ufw default allow outgoing
ufw allow from <team-vpn-cidr> to any port 22 proto tcp comment 'bm: SSH MCP'
ufw enable
```

Confirm: `ss -tlnp` should show `sshd` and nothing else on a fresh provision.

---

## 4. Installing Basic Memory on the VM

Follow [upstream BM's install guide](https://github.com/basicmachines-co/basic-memory) for the actual install. Verified-good version for this doc: **BM v0.21.1** (released 2026-05-16). Recap of the shared-VM-specific bits:

```bash
# 1. Create the service user. Use /bin/bash (NOT /usr/sbin/nologin) —
#    sshd invokes the user's shell to run forced-commands and the
#    `ssh bm@<vm-host> basic-memory mcp` invocation from §7, so a
#    `nologin` shell would silently break the MCP transport. The
#    forced-command + restrictions in §6's authorized_keys (no-pty,
#    no-port-forwarding, etc.) is what blocks interactive shell access,
#    not the login shell field on the user record.
useradd --system --create-home --home-dir /var/lib/basic-memory --shell /bin/bash bm

# 2. Install BM as the `bm` user via the upstream-preferred channel
#    (pip, pipx, or distribution package — see upstream README). If you
#    install system-wide via pipx-as-root, make sure the resulting
#    binary is on PATH for user `bm` (e.g. /usr/local/bin/basic-memory).
sudo -u bm pipx install basic-memory==0.21.1
# or equivalent for your install channel

# 3. Sanity-check the binary resolves under user `bm`:
sudo -u bm which basic-memory
sudo -u bm basic-memory --version   # expect 0.21.1
```

A bare `systemd` unit for BM itself is **not required** for the SSH-proxy transport (§5) — each developer's session launches BM on demand over SSH. If your team prefers a long-running BM process (for log centralisation, resource limits, or a future HTTP transport), wrap it in your own unit; that path is out of scope here.

The KB data directory lives at `/var/lib/basic-memory/kb`. §8 turns that directory into a git working tree.

---

## 5. Configuring remote MCP transport — stdio-via-SSH-proxy

**Verified transport (BM v0.21.1, 2026-05-20):** BM ships only an stdio MCP server (`basic-memory mcp`). Upstream does not currently prescribe a remote-MCP transport for shared-VM deployments. The conventional pattern — and the one this guide recommends — is **stdio-via-SSH-proxy**: each developer's Claude Code config invokes `ssh -i <key> bm@<vm-host> basic-memory mcp` to launch a per-session stdio MCP process on the shared VM.

Why SSH-proxy:

- Requires no extra transport infrastructure beyond OpenSSH (already on the VM, already on every developer's workstation).
- Works against BM's default invocation mode (no upstream patches).
- Per-developer SSH keypairs are a familiar auth model; revocation is `authorized_keys` line removal.
- The MCP framing rides directly over SSH's stdio channels — there is no HTTP listener to harden, no Bearer-token to rotate, no reverse proxy to operate.

Teams that prefer URL/token-based transport (SSE or streamable-HTTP) can run BM behind a reverse proxy of their choice; that path is out of scope for this doc and may be revisited if upstream ships a first-class remote transport.

Source: upstream [BM README](https://github.com/basicmachines-co/basic-memory/blob/main/README.md) (no explicit remote-transport guidance as of v0.21.1; SSH-proxy is the conventional pattern for stdio MCP servers).

---

## 6. Auth & access control — per-developer SSH keypairs

Each developer gets their own SSH keypair pinned to the `bm` user on the shared VM. Revocation is a one-line edit to `authorized_keys`.

**Per-developer setup (developer side):**

```bash
# On the developer's workstation. Use a passphrase OR rely on the
# ssh-agent your team already runs.
ssh-keygen -t ed25519 -f ~/.ssh/basic-memory-<dev> -C "basic-memory:<dev>@<team>"
cat ~/.ssh/basic-memory-<dev>.pub
# Hand the .pub line to the VM operator (Slack DM, secrets manager, whatever
# channel your team uses for trusted small payloads).
```

**Operator side (one entry per dev):**

```bash
# Append the developer's pubkey to bm's authorized_keys. Restrict the
# entry to running the BM MCP server only — no shell access, no port
# forwards, no PTY. The `command="..."` prefix forces this regardless
# of what the client requests.
install -d -o bm -g bm -m 0700 /var/lib/basic-memory/.ssh
touch /var/lib/basic-memory/.ssh/authorized_keys
chown bm:bm /var/lib/basic-memory/.ssh/authorized_keys
chmod 0600 /var/lib/basic-memory/.ssh/authorized_keys

# Append, with the forced-command + restrictions:
cat >> /var/lib/basic-memory/.ssh/authorized_keys <<'EOF'
command="/usr/local/bin/basic-memory mcp",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAAC3... basic-memory:<dev>@<team>
EOF
```

Forced-command is load-bearing: without it, a developer's SSH key would grant full shell access to the `bm` user, defeating the principle-of-least-privilege boundary. Audit your `authorized_keys` file periodically (`grep -v '^#' /var/lib/basic-memory/.ssh/authorized_keys | wc -l`) and reconcile it against your team roster.

**Rotation.** Treat the keypair like any other developer credential: rotate when a developer leaves, when a workstation is lost, or on a regular cadence (90 days is generous, 180 days is the upper bound most teams find tolerable). Rotation procedure in §9.

---

## 7. Per-developer Claude Code MCP config

Each developer adds a single MCP server entry to their Claude Code config. Adjust the `<dev>` and `<vm-host>` placeholders; everything else is fixed.

```json
{
  "mcpServers": {
    "basic-memory": {
      "command": "ssh",
      "args": [
        "-i", "~/.ssh/basic-memory-<dev>",
        "-o", "StrictHostKeyChecking=yes",
        "bm@<vm-host>",
        "basic-memory", "mcp"
      ]
    }
  }
}
```

Notes:

- `-i` pins the key file. Avoid relying on `ssh-agent` here so the developer can grep their MCP config to know exactly which key is in use.
- `StrictHostKeyChecking=yes` requires that the developer accept the VM's host key on first connect (`ssh bm@<vm-host>` once before adding the MCP entry). On a host-key rotation event, the developer must `ssh-keygen -R <vm-host>` and reconnect.
- The forced-command on the server side (§6) means `args` after the hostname (`basic-memory mcp`) is technically redundant — the server will run the forced command regardless. Keeping it in the config is intentional: it documents the intent for the next reader of the config file.

Test the connection before wiring it into Claude Code:

```bash
# This should print the BM MCP server's JSON handshake to stderr and
# block waiting for stdin. Ctrl+C to exit; that's expected.
ssh -i ~/.ssh/basic-memory-<dev> bm@<vm-host> basic-memory mcp
```

If you see a clean MCP banner, Claude Code will work. If you see a shell prompt instead, the forced-command in §6 is misconfigured.

---

## 8. Storage & backup — git-backed KB

The KB working tree at `/var/lib/basic-memory/kb` is committed to a private git remote on a 60-second timer (primary), with two alternatives (inotify-recursive for per-edit attribution; 5-minute long-cadence for systemd-less hosts).

> **Secrets policy.** Treat the KB git repo as private. Anything you would not paste into a private Slack channel does not belong in a BM note. The repo's access boundary IS the policy boundary for notes; the git-backing does not add new access controls beyond what your git provider enforces.

> **Scope of the git-backing.** Only the markdown source under `kb/` is committed. BM's SQLite index regenerates from the markdown on startup, so committing it would bloat history and introduce non-deterministic diffs. If your operator instinct says "commit everything," resist it here.

### 8a. One-time setup (working tree + remote + deploy key)

Run as root during VM provisioning:

```bash
# 1. Ensure the bm user/group exists. Adjust UID/GID to whatever your team uses.
#    Shell is /bin/bash (NOT /usr/sbin/nologin) — see §4 for why; sshd uses
#    the login shell to execute forced-commands and the §7 ssh invocation,
#    and §6's authorized_keys restrictions (no-pty etc.) block interactive
#    shell access regardless of the shell field.
id -u bm >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/basic-memory --shell /bin/bash bm
# 2. The KB working tree, owned by bm:bm. BM writes notes here; the systemd
#    service commits here. Both run as bm, so the tree itself plus .git must
#    be bm-owned; otherwise git commit fails with "fatal: Unable to create
#    '.../.git/index.lock'" the moment the service runs.
install -d -o bm -g bm -m 0750 /var/lib/basic-memory/kb
# 3. Initialise as a working tree pointing at the team's private remote.
sudo -u bm git -C /var/lib/basic-memory/kb init -b main
sudo -u bm git -C /var/lib/basic-memory/kb remote add origin git@<remote>:<org>/<repo>.git
sudo -u bm git -C /var/lib/basic-memory/kb config user.email "basic-memory@<team>.local"
sudo -u bm git -C /var/lib/basic-memory/kb config user.name  "Basic Memory"
```

The `sudo -u bm` invocations matter — running `git init` as root would leave `.git/` root-owned and the service would fail on first commit. If you forget and the service errors, run `chown -R bm:bm /var/lib/basic-memory/kb` to recover.

Install the deploy key (a per-VM SSH key the git provider authorises to push to the team's private repo):

```bash
install -d -o bm -g bm -m 0700 /etc/basic-memory
install -o bm -g bm -m 0600 /path/to/deploy_key /etc/basic-memory/deploy_key
# Provision known_hosts for the bm user against the team's git remote.
# Replace <remote-host> with the hostname/port of your git provider (e.g.
# github.com, gitlab.example.com:22, or your self-hosted host). Use port
# 22 implicitly or specify it explicitly with `-p`. Run as bm so the
# resulting file ends up owned bm:bm with mode 0600.
install -d -o bm -g bm -m 0700 /var/lib/basic-memory/.ssh
sudo -u bm ssh-keyscan -H <remote-host> >> /var/lib/basic-memory/.ssh/known_hosts
sudo -u bm chmod 0600 /var/lib/basic-memory/.ssh/known_hosts
```

If the team's git provider rotates its host key, the next sync run will fail with "REMOTE HOST IDENTIFICATION HAS CHANGED" — operator must run `sudo -u bm ssh-keygen -R <remote-host>` then re-keyscan (after confirming with the provider that the rotation is legitimate, not a MITM).

**Why `bm:bm 0600` for the deploy key.** The service runs as user `bm` and must be able to read the key. `root:bm 0600` would silently break the service because group-read is off; if a future operator prefers root ownership they must use `root:bm 0640`. `bm:bm 0600` is the simpler default and what this guide prescribes.

### 8b. The commit-and-push script (shared by all variants)

Save as `basic-memory-commit-and-push.sh` in your provisioning bundle; install it on the VM (next subsection).

```bash
#!/usr/bin/env bash
# /usr/local/bin/basic-memory-commit-and-push.sh
set -euo pipefail
cd /var/lib/basic-memory/kb
export GIT_SSH_COMMAND="ssh -i /etc/basic-memory/deploy_key -o StrictHostKeyChecking=yes"

git add -A

# Commit if there are staged changes. Skip the commit step quietly when
# there's nothing to stage — but DO NOT exit yet. A previous tick may
# have committed locally and then failed to push (network blip, deploy
# key issue, etc.). Those commits are still ahead of origin/main and
# this tick is responsible for flushing them.
if ! git diff --staged --quiet; then
  git commit -m "bm: $(date -Iseconds)"
fi

# Check whether we have anything to push. `git rev-list --count
# origin/main..HEAD` returns the number of local commits that origin/main
# is missing. If it's zero we're fully synced and can exit silently;
# otherwise (either a fresh commit above or a queued backlog from a
# previous failed push) we push. We fetch first so the ahead-count is
# correct even after the remote moved.
#
# Bootstrap case: on the first run, the team's remote may be an empty
# repository (no `main` branch yet). `git fetch origin main` will fail
# silently because there is no remote branch to fetch, leaving no
# `origin/main` ref. We detect that case and fall through to the push
# path so the first commit lands as `origin/main`. Without this branch
# the script would compute `ahead=0` from the missing ref and exit
# without pushing — leaving the team with a local-only commit history.
git fetch --quiet origin main || true
if git rev-parse --quiet --verify origin/main >/dev/null; then
  ahead=$(git rev-list --count "origin/main..HEAD")
  if [[ "$ahead" -eq 0 ]]; then
    exit 0
  fi
else
  # No origin/main yet — bootstrap path. Use `git push -u origin HEAD:main`
  # below so the first push both creates the remote branch and sets the
  # local upstream tracking ref. Skip the ahead-count gate entirely.
  exec_bootstrap_push=1
fi

# Bootstrap path: no origin/main exists yet — create it and set upstream.
if [[ -n "${exec_bootstrap_push:-}" ]]; then
  git push -u origin HEAD:main
  exit 0
fi

# Normal sync uses a fast-forward push. If the remote has diverged (rare:
# only when a second writer hit the same repo), pull-rebase once and try
# again. We do NOT use --force-with-lease for the normal push because this
# is a backup/sync job — rewriting remote history on every tick would be
# surprising and could trip branch-protection rules. Only after a clean
# local rebase do we re-push with --force-with-lease so the rebase's
# rewritten history can land.
if ! git push origin main; then
  git pull --rebase --autostash origin main
  git push --force-with-lease origin main
fi
```

Install the script(s):

```bash
install -o root -g root -m 0755 ./basic-memory-commit-and-push.sh /usr/local/bin/basic-memory-commit-and-push.sh
# Only for the inotify-recursive alternative:
install -o root -g root -m 0755 ./basic-memory-watch-and-commit.sh /usr/local/bin/basic-memory-watch-and-commit.sh
```

Both scripts are owned by `root:root` mode `0755` (world-read+exec, root-write) — the systemd units invoke them as user `bm`, which only needs read+exec. World-write would allow any unprivileged user on the VM to substitute a malicious script before the next timer tick; `0755` blocks that.

### 8c. Primary variant — 60-second systemd timer (recommended)

Why a timer instead of a `.path` unit: systemd's `.path` units use `inotify_add_watch(2)` non-recursively, so a single `PathChanged=/var/lib/basic-memory/kb` triggers only on changes to the kb/ directory itself, not on edits to nested notes (BM stores under `kb/decisions/*.md`, `kb/modules/*.md`, etc.). A path-unit-per-subdir is brittle because BM creates new subdirs at runtime. The 60-second timer is the simplest correct shape.

`/etc/systemd/system/basic-memory-git-sync.service`:

```ini
[Unit]
Description=Commit and push BM KB changes
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/basic-memory-commit-and-push.sh
User=bm
Group=bm
```

`/etc/systemd/system/basic-memory-git-sync.timer`:

```ini
[Unit]
Description=Run basic-memory-git-sync every minute

[Timer]
OnBootSec=60s
OnUnitActiveSec=60s
Unit=basic-memory-git-sync.service
Persistent=true

[Install]
WantedBy=timers.target
```

The 60-second cadence is the natural debounce — concurrent writes within the window coalesce into one commit. Drop to 30s or raise to 120s based on observed write volume.

Enable:

```bash
systemctl daemon-reload
systemctl enable --now basic-memory-git-sync.timer
systemctl list-timers basic-memory-git-sync.timer   # confirm next-run time
```

### 8d. Alternative variant — inotify-recursive watcher (per-edit attribution)

Use this only if you need every BM agent action recorded as its own commit (compliance / audit / replay use cases). Cost: an extra installed package (`inotify-tools`) and a slightly more complex unit to operate. Teams that don't need per-edit attribution should NOT pick this.

`/etc/systemd/system/basic-memory-git-watcher.service`:

```ini
[Unit]
Description=Watch BM KB recursively and commit on edits
After=network-online.target
Requires=network-online.target

[Service]
Type=simple
User=bm
Group=bm
ExecStart=/usr/local/bin/basic-memory-watch-and-commit.sh
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

`/usr/local/bin/basic-memory-watch-and-commit.sh`:

```bash
#!/usr/bin/env bash
# Requires inotify-tools (apt-get install inotify-tools).
set -euo pipefail
DIR=/var/lib/basic-memory/kb
DEBOUNCE_SEC=15
last_commit_pid=
# --exclude '(^|/)\.git(/|$)' is load-bearing: DIR is the git working tree,
# so every `git commit` (invoked by commit-and-push.sh below) writes to
# .git/index / .git/objects/ / .git/HEAD / refs/. Without the exclude the
# watcher would self-trigger on its own commits in a tight loop. The
# regex matches `.git` as the leading directory component OR any nested
# `.git` (worktrees / submodules — unlikely here but cheap to handle).
inotifywait -r -m -q \
  -e close_write -e create -e move -e delete \
  --exclude '(^|/)\.git(/|$)' \
  --format '%w%f' "$DIR" \
| while read -r _; do
    # Debounce: kill any pending committer; schedule a new one.
    if [[ -n "$last_commit_pid" ]] && kill -0 "$last_commit_pid" 2>/dev/null; then
      kill "$last_commit_pid" 2>/dev/null || true
    fi
    ( sleep "$DEBOUNCE_SEC" && /usr/local/bin/basic-memory-commit-and-push.sh ) &
    last_commit_pid=$!
  done
```

`-r` is the recursive flag — without it the watcher misses edits to nested note files, which is the failure mode the primary variant avoids by polling on a timer. `--exclude '(^|/)\.git(/|$)'` prevents the watcher from triggering on its own commits.

Enable:

```bash
apt-get install -y inotify-tools     # or your distro's equivalent
systemctl daemon-reload
systemctl enable --now basic-memory-git-watcher.service
journalctl -fu basic-memory-git-watcher.service   # tail to confirm
```

Do **not** enable both the timer and the watcher — they race on `commit-and-push.sh`. Pick one.

### 8e. Long-cadence fallback — 5-minute timer or cron

Use this on hosts without systemd, on container images that ship without it, or when commit volume is a hard constraint.

**With systemd** — only the timer changes; the service file from §8c is reused unchanged.

`/etc/systemd/system/basic-memory-git-sync.timer` (5-min variant):

```ini
[Timer]
OnCalendar=*:0/5
Persistent=true
Unit=basic-memory-git-sync.service

[Install]
WantedBy=timers.target
```

**Without systemd (crontab fallback)** — useful for older hosts, container images that ship without systemd, or operators who simply prefer cron. The log file MUST be provisioned with `bm:bm` ownership BEFORE installing the crontab; `/var/log/` is root-writable on most distros, so a naive `>> /var/log/basic-memory-git-sync.log` from a `bm`-owned crontab would fail with "Permission denied" on the first run and the user would never see the error (it's just lost). Provision under `/var/log/` if you want it alongside other system logs (chown required), or under `/var/lib/basic-memory/` if you'd rather keep everything bm-owned in one tree.

```bash
# Provision the log file (one-time, as root) so bm can append to it:
install -o bm -g bm -m 0644 /dev/null /var/log/basic-memory-git-sync.log

# Then install the crontab as the bm user:
sudo -u bm crontab -l 2>/dev/null > /tmp/bm.cron || true
echo '*/5 * * * * /usr/local/bin/basic-memory-commit-and-push.sh >> /var/log/basic-memory-git-sync.log 2>&1' >> /tmp/bm.cron
sudo -u bm crontab /tmp/bm.cron
rm /tmp/bm.cron
```

If you prefer to keep everything bm-owned without touching `/var/log/`, swap the log path for `/var/lib/basic-memory/git-sync.log` (no separate `install` step needed — the parent dir is already bm-owned from §8a setup).

`>>` to a log file replaces the structured `journalctl` view that systemd users get. Rotate the log via the host's standard `logrotate` config — §9 covers this.

The service script is the same in both forms; only the trigger differs.

### 8f. Verification (smoke test)

After applying any of the three variants:

1. From a developer workstation, ask Claude Code to write a test note via BM (or run `sudo -u bm basic-memory write_note --permalink test/verify-sync --body "verification $(date)"` directly on the VM).
2. Wait one cadence-window (60s for primary, ~20s for inotify, 5m for fallback).
3. On the VM:
   ```bash
   cd /var/lib/basic-memory/kb && sudo -u bm git log -1 --stat
   ```
4. Confirm the new file appears in the most-recent commit.
5. On the git remote (your provider's UI or `git ls-remote origin main`), confirm the commit has been pushed.

If steps 3 or 5 fail, jump to §12 troubleshooting.

---

## 9. Day-2 ops

### 9a. Upgrading Basic Memory

```bash
# 1. Read the upstream changelog first — BM is in early-version
#    territory and minor versions can carry schema migrations.
#    https://github.com/basicmachines-co/basic-memory/blob/main/CHANGELOG.md
#
# 2. Snapshot the KB volume (your hypervisor's tooling) before the upgrade.
#    The git-backed remote is also a recovery point, but a volume
#    snapshot rewinds the SQLite index in lockstep with the markdown.
#
# 3. Stop the git-sync service so no commits land mid-upgrade.
systemctl stop basic-memory-git-sync.timer       # or basic-memory-git-watcher.service
#
# 4. Upgrade BM as the bm user.
sudo -u bm pipx upgrade basic-memory
sudo -u bm basic-memory --version    # confirm the new version
#
# 5. Run any upstream-prescribed migration command (check the release notes).
#
# 6. Smoke-test from one developer workstation (a single `read_note` will do).
#
# 7. Re-enable the sync.
systemctl start basic-memory-git-sync.timer
```

If anything goes wrong, restore the volume snapshot from step 2 — the SQLite index and the markdown stay consistent because they were snapshotted together.

### 9b. Rotating a developer's SSH keypair

```bash
# 1. Have the developer generate a new keypair (§6 procedure) and share
#    the new .pub line.
# 2. Append the new entry to authorized_keys with the same forced-command.
# 3. Remove the old entry — locate by the trailing comment (e.g.
#    `basic-memory:<dev>@<team>`):
sudo -u bm sed -i.bak '/basic-memory:<dev>@<team>/d' /var/lib/basic-memory/.ssh/authorized_keys
# 4. Manually re-add ONLY the new pubkey line so the entry order is clean.
# 5. The developer updates `-i ~/.ssh/basic-memory-<dev>` in their MCP
#    config to point at the new key file. No SSH server restart needed.
```

Verify by tailing `journalctl -u ssh` and confirming the developer's next session authenticates against the new key fingerprint.

### 9c. Rotating the deploy key

```bash
# 1. Generate a new deploy key locally:
ssh-keygen -t ed25519 -f /tmp/deploy_key_new -N '' -C "basic-memory-deploy:<team>:$(date +%Y%m%d)"
# 2. Add the new public key to the git remote's deploy-key list (provider UI).
# 3. Swap the file on the VM, then restart the timer:
install -o bm -g bm -m 0600 /tmp/deploy_key_new /etc/basic-memory/deploy_key
systemctl restart basic-memory-git-sync.timer
# 4. Verify the next push lands:
systemctl status basic-memory-git-sync.service
sudo -u bm git -C /var/lib/basic-memory/kb log -1 origin/main
# 5. Once a successful push has been observed, revoke the old deploy
#    key in the provider UI. Securely delete /tmp/deploy_key_new.
shred -u /tmp/deploy_key_new /tmp/deploy_key_new.pub
```

### 9d. Adding a developer

1. Developer generates `~/.ssh/basic-memory-<dev>` per §6.
2. Operator appends the pubkey to `authorized_keys` with the forced-command prefix.
3. Developer adds the MCP config block from §7 to their Claude Code config.
4. Developer runs the §7 smoke test to confirm.

### 9e. Removing a developer

1. Operator removes the developer's line from `/var/lib/basic-memory/.ssh/authorized_keys` (locate by the trailing comment).
2. Developer deletes their local `~/.ssh/basic-memory-<dev>{,.pub}` and removes the MCP server block from their Claude Code config.
3. No git-side revocation needed — the developer never had direct git access; only the VM did (via the deploy key in §8a).

### 9f. VM restore (from snapshot or git remote)

If the VM is lost entirely:

1. Provision a fresh VM following §3 and §4.
2. Restore one of:
   - **Volume snapshot** (preferred): both markdown and SQLite index restore in lockstep, no rebuild needed.
   - **git remote** (fallback): `sudo -u bm git clone git@<remote>:<org>/<repo>.git /var/lib/basic-memory/kb`. BM rebuilds the SQLite index from the markdown on first startup; expect a delay proportional to KB size.
3. Re-install the deploy key (§8a) and the systemd units (§8c or §8e).
4. Re-distribute developer pubkeys into `authorized_keys` (§6 + §9d).
5. Smoke-test from one developer workstation.

### 9g. `git gc` cadence

The `.git/` directory grows linearly with commit count (~one commit per minute under the primary variant ≈ 525,600 commits/year). Run quarterly:

```bash
sudo -u bm git -C /var/lib/basic-memory/kb gc --aggressive --prune=now
```

Schedule via cron or a separate systemd timer if your team prefers.

### 9h. Log rotation

If you chose the cron fallback (§8e), the log file at `/var/log/basic-memory-git-sync.log` (or `/var/lib/basic-memory/git-sync.log`) grows unbounded. Add a `logrotate(8)` snippet:

```
/var/log/basic-memory-git-sync.log {
  weekly
  rotate 8
  compress
  missingok
  notifempty
  create 0644 bm bm
}
```

The systemd variants don't need this — `journalctl` handles rotation automatically per the host's `journald.conf`.

---

## 10. Verification checklist (5-step smoke test)

Run this after initial setup and after any meaningful change (BM upgrade, key rotation, sync variant swap):

1. **MCP transport.** From a developer workstation, `ssh -i ~/.ssh/basic-memory-<dev> bm@<vm-host> basic-memory mcp` returns the BM MCP banner. Ctrl+C to exit.
2. **Tool surface.** Within a Claude Code session, ask the agent to call `search_notes` against the VM. Expect a non-error response (an empty result is fine on a fresh KB).
3. **Note write round-trip.** Ask the agent to `write_note` a small test note. Confirm the file appears at `/var/lib/basic-memory/kb/<permalink>.md` on the VM.
4. **Local commit.** Wait one cadence-window. On the VM: `sudo -u bm git -C /var/lib/basic-memory/kb log -1 --stat` shows the new file in the most-recent commit.
5. **Remote push.** `sudo -u bm git -C /var/lib/basic-memory/kb log -1 origin/main` shows the same commit on the remote. Equivalently, the commit is visible in the git provider's UI.

If all 5 pass, the deployment is healthy.

---

## 11. License compatibility note

Basic Memory is licensed under [AGPL-3.0](https://github.com/basicmachines-co/basic-memory/blob/main/LICENSE). The AGPL's "network-service" clause (§13) requires that anyone who interacts with a modified version over a network has access to the source.

For the topology this guide prescribes:

- **The team runs an unmodified upstream BM.** No source modifications, no fork-and-patch.
- **The only network surface is intra-team SSH.** Developers within the team interact with BM over the team's private SSH transport; nobody outside the team has network access.
- **`anti-tangent-mcp` does not link against or modify BM.** They are two independent MCP servers running in the same Claude Code session; there is no code sharing, no shared address space, and no derived work.

Under those conditions the deployment is **trivially compliant**: there is no modified version, so there is nothing extra to publish.

**Team policy:** bugs and feature requests for Basic Memory go upstream (`gh issue create -R basicmachines-co/basic-memory ...`). No internal fork. If a critical fix is blocking your team and upstream has not landed it, prefer a workaround in your team's wrapping code (anti-tangent prompts, INTEGRATION.md guidance, etc.) over patching BM directly. If patching BM is genuinely the only path, treat that as a moment to revisit this compliance note — a forked, network-accessible BM ships with stricter AGPL obligations.

---

## 12. Troubleshooting

The seven failure modes you will actually see, in roughly the order operators encounter them. Each entry has a symptom, a cause, and a resolution.

### 12.1 MCP transport handshake failure

**Symptom.** Claude Code reports `MCP server "basic-memory" failed to start` or `connection closed before initialization`. From the developer's shell, `ssh -i ~/.ssh/basic-memory-<dev> bm@<vm-host> basic-memory mcp` exits immediately with an error, or hangs forever without a banner.

**Causes (in order of likelihood).**

1. The developer's pubkey is not in `/var/lib/basic-memory/.ssh/authorized_keys` (§6 not done for this dev).
2. The forced-command prefix in `authorized_keys` is malformed — the line is silently ignored by `sshd`.
3. The `basic-memory` binary is not on PATH for user `bm` (re-check `sudo -u bm which basic-memory` from §4).
4. The VM's firewall (§3) is rejecting the developer's source IP.

**Resolution.** Test in isolation: `ssh -v -i ~/.ssh/basic-memory-<dev> bm@<vm-host>` and read the verbose output. `Permission denied (publickey)` → pubkey not authorised. `Connection refused` → firewall. Clean SSH handshake but no MCP banner → forced-command misconfigured or BM binary missing. Fix and retest.

### 12.2 SSH keypair rotation (per-dev)

**Symptom.** A developer reports their MCP connection started failing after they reissued their key, or you need to revoke a departing developer's access.

**Resolution.** §9b for rotation, §9e for revocation. Verify by tailing `journalctl -u ssh` while the developer attempts a new connection.

### 12.3 BM index out-of-sync with markdown source

**Symptom.** `search_notes` returns stale or missing results; a freshly-written note is on disk under `kb/` but BM doesn't surface it. Or BM startup logs a SQLite-corruption error.

**Cause.** BM's SQLite index drifted from the markdown source. Most often: a hand-edit landed in the markdown without going through BM, or a snapshot restore brought back the markdown but not the index.

**Resolution.** BM regenerates the index from markdown on startup. Stop any active session, then on the VM:

```bash
sudo -u bm rm -f /var/lib/basic-memory/<index-file>     # path per upstream README
sudo -u bm basic-memory  # or systemctl restart whatever BM unit you use
```

Wait for the rebuild to finish (logged to stdout/journal). For large KBs this takes a few seconds per thousand notes.

### 12.4 `git push` failure (commits queue locally)

**Symptom.** Notes are committing locally but not landing on the remote. `systemctl status basic-memory-git-sync.service` shows `failed` (primary variant) or the unit keeps restarting (watcher variant). `journalctl -u basic-memory-git-sync` shows one of: a network error, `! [remote rejected]`, `Permission denied (publickey)`, or `key has expired`.

**Cause.** Network blip, the deploy key was revoked/expired/rotated upstream, or the remote has a branch-protection rule rejecting the push.

**What happens to the data in the meantime.** Local commits queue up — the script does NOT block on push failures. Each timer tick adds another commit on top of the existing queue. Inspect the queue:

```bash
sudo -u bm git -C /var/lib/basic-memory/kb log origin/main..HEAD --oneline
```

**Resolution.** Fix the underlying cause (rotate the deploy key per §9c, allow the push under the branch-protection rule, wait for the network to come back). Then run the script once manually as `bm` to flush the queue:

```bash
sudo -u bm /usr/local/bin/basic-memory-commit-and-push.sh
```

Or wait for the next timer tick — the script's catch-up logic will push the entire queue in one go.

### 12.5 Rebase conflict on the shared remote

**Symptom.** `journalctl -u basic-memory-git-sync` shows `CONFLICT (content): Merge conflict in <path>` after a `git pull --rebase --autostash`. The service enters `failed` state and stays there.

**Cause.** Another writer (a second VM, or a developer hand-editing the remote) pushed a conflicting change while the VM was offline or pending a backlog flush. Notes are append-mostly so conflicts are rare, but they happen.

**Resolution.** Hand-merge as the `bm` user:

```bash
cd /var/lib/basic-memory/kb && sudo -u bm git status
# Resolve the conflict in your editor — preserve both edits where
# possible; BM notes are markdown so most conflicts are trivial.
sudo -u bm git add <resolved-files>
sudo -u bm git rebase --continue
systemctl restart basic-memory-git-sync.service
```

If the rebase reveals systematic conflicts (e.g. two writers competing on the same note repeatedly), revisit the "one shared VM" topology assumption in §2 — anti-tangent-mcp's BM guidance assumes a single writer.

### 12.6 `.git/index.lock` permission denied

**Symptom.** `journalctl -u basic-memory-git-sync` shows `fatal: Unable to create '/var/lib/basic-memory/kb/.git/index.lock': Permission denied`.

**Cause.** Somebody ran `git init` (or a manual `git` command) as root during setup or troubleshooting, leaving part of the `.git/` tree root-owned. The systemd service runs as `bm` and can't write the lockfile.

**Resolution.**

```bash
chown -R bm:bm /var/lib/basic-memory/kb && systemctl restart basic-memory-git-sync.service
```

Then audit how root touched the tree (`grep root /var/log/auth.log` or your distro's equivalent) and update your runbook so it doesn't happen again.

### 12.7 `Host key verification failed`

**Symptom.** `journalctl -u basic-memory-git-sync` shows `Host key verification failed.` or `REMOTE HOST IDENTIFICATION HAS CHANGED!`.

**Causes.**

- The bm user's `known_hosts` was never provisioned (initial-setup error — §8a was skipped or partial).
- The git remote rotated its host key (legitimately) since `known_hosts` was provisioned.
- A MITM attack is in progress (rare but possible — do not blindly accept the new key).

**Resolution.**

For first-time setup (no prior entry exists), rerun the §8a `ssh-keyscan` block as user `bm`:

```bash
sudo -u bm ssh-keyscan -H <remote-host> >> /var/lib/basic-memory/.ssh/known_hosts
sudo -u bm chmod 0600 /var/lib/basic-memory/.ssh/known_hosts
systemctl restart basic-memory-git-sync.service
```

For a rotated host key, **confirm with the git provider out-of-band that the rotation is legitimate** (provider status page, support ticket, signed announcement). Only then:

```bash
sudo -u bm ssh-keygen -R <remote-host>
sudo -u bm ssh-keyscan -H <remote-host> >> /var/lib/basic-memory/.ssh/known_hosts
systemctl restart basic-memory-git-sync.service
```

If you cannot confirm the rotation, treat it as a potential MITM, halt the sync, and escalate.

---

## See also

- [`INTEGRATION.md`](../../INTEGRATION.md) — the project-knowledge integration playbook (how anti-tangent and BM compose in a Claude Code session).
- [`examples/project-knowledge/`](../../examples/project-knowledge/) — note templates (decision, module, feature, epic, glossary) the team's agents should produce.
- [Basic Memory upstream](https://github.com/basicmachines-co/basic-memory) — install, tool reference, schema details.
