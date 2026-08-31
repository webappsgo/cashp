# Installation

## Supported operating systems

CasHp's **server** runs on Linux only. There are no macOS, Windows, or BSD
host targets for the server — it manages native host services (systemd/OpenRC
units, Postfix, Dovecot, BIND, nftables, libvirt) that only exist on Linux.
Client binaries (`cashp-cli`) and the remote agent (`cashp-agent`) are built
for all eight platforms, but the control panel itself is a Linux server.

| Family | Floor | Package manager | Notes |
|---|---|---|---|
| Debian (+ derivatives) | 11 (bullseye)+ | `apt` | Debian 8 (jessie) and 10 (buster) are archived and unreachable from default `deb.debian.org` sources — 11 is the real floor |
| Ubuntu (+ derivatives) | 22.04+ | `apt` | |
| Alpine | 3.20+ | `apk` | The `community` repository **must** be enabled — most non-core managed services live there, not in `main` |
| RHEL family (+ derivatives) | 8+ | `dnf` / `yum` | Rocky Linux, AlmaLinux, Oracle Linux, and CentOS Stream are treated identically; EPEL is required for several services |
| Fedora | 40+ | `dnf` | Modularity is gone (`dnf module list php` returns nothing) — one native PHP version, same multi-version problem as RHEL |
| Arch Linux (+ derivatives) | rolling | `pacman` | Manjaro, EndeavourOS, etc. treated identically; no version floor to track |

## Running as root

!!! danger "CasHp runs as root by design"
    The `cashp` server process requires elevated host privileges to manage VMs
    (libvirt/KVM), containers (Docker/Incus/Podman), and system services
    (mail, DNS, firewall). This is a **documented, intentional, non-negotiable
    exception** to least-privilege-by-default — not an oversight and not
    something to "fix" by dropping privileges.

    The mitigation is *internal*: strict RBAC inside CasHp plus per-tenant
    isolation (network, filesystem, resource limits), rather than running the
    whole server unprivileged. Tenant-submitted code (PaaS source, container
    images, VM images) is treated as untrusted and contained by isolation, not
    by content inspection.

Running the binary as a non-root user is supported for evaluation and for the
client/agent binaries, but a non-root server cannot manage host services. In
that case CasHp uses the per-user path set (see
[Configuration](configuration.md#file-locations)) and a `systemd --user`
service instead of a system unit.

## Managed services and OS packages

CasHp installs and manages the following native host services. Package names
below were verified against live containers per distro family, not assumed.
A `-` means no separate package is needed beyond what is already listed.

| Service | Debian / Ubuntu (apt) | Alpine (apk) | RHEL family (dnf) | Fedora (dnf) | Arch (pacman) |
|---|---|---|---|---|---|
| Web server | `nginx` | `nginx` | `nginx` | `nginx` | `nginx` |
| PHP-FPM (per version) | `phpX.Y-fpm` | `phpNN-fpm` | `phpNN-php-fpm` | `phpXY-php-fpm` | `php` (current only, native) |
| Mail transport | `postfix` | `postfix` | `postfix` | `postfix` | `postfix` |
| IMAP/POP3/LMTP | `dovecot-imapd`, `dovecot-pop3d`, `dovecot-lmtpd` | `dovecot`, `dovecot-pop3d`, `dovecot-lmtpd` | `dovecot` (single package) | `dovecot` (single package) | `dovecot` (single package) |
| Mail filtering | `amavisd-new` | `amavis` | `amavis` | `amavis` | `amavisd-new` |
| Anti-spam | `spamassassin` | `spamassassin` | `spamassassin` | `spamassassin` | `spamassassin` |
| Anti-virus daemon | `clamav-daemon` | `clamav-daemon` | `clamd` (`clamav` is CLI/lib only) | `clamd` (`clamav` is CLI/lib only) | `clamav` (includes daemon) |
| DKIM signing | `opendkim` | `opendkim` | `opendkim` | `opendkim` | `opendkim` |
| DMARC | `opendmarc` | `opendmarc` | `opendmarc` | `opendmarc` | `opendmarc` |
| DNS server | `bind9` | `bind` | `bind` | `bind` + `bind-utils` | `bind` |
| Intrusion prevention | `fail2ban` | `fail2ban` | `fail2ban` | `fail2ban` | `fail2ban` |
| Firewall | `nftables` | `nftables` | `nftables` | `nftables` | `nftables` |
| Container engine (Docker) | `docker-ce` + `docker-ce-cli` + `containerd.io` (Docker's official apt repo, not distro `docker.io`) | `docker` + `docker-cli` (Alpine's own package — Docker publishes no official Alpine repo) | `docker-ce` + `docker-ce-cli` + `containerd.io` (Docker's official dnf repo — not in RHEL/EPEL) | `docker-ce` + `docker-ce-cli` + `containerd.io` (Docker's official dnf repo) | `docker` (Arch `extra` — no third-party repo needed) |
| Container engine (Incus) | `incus` | `incus` | not packaged — third-party repo required | `incus` (native) | `incus` |
| Container engine (Podman) | `podman` | `podman` | `podman` | `podman` | `podman` |
| VM hypervisor | `libvirt-daemon-system` | `libvirt` + `libvirt-daemon` + `libvirt-qemu` + `libvirt-client` | `libvirt-daemon-config-network` + `libvirt-daemon-kvm` | `libvirt-daemon-config-network` + `libvirt-daemon-kvm` | `libvirt` |
| VM emulator | `qemu-system-x86` + `qemu-system-arm` (`qemu-kvm` no longer exists as a package) | `qemu-system-x86_64` + `qemu-system-aarch64` | `qemu-kvm` (name still valid on RHEL) | `qemu-kvm` | `qemu-full` (or the lighter `qemu-desktop`) |
| VM install helper | `virtinst` | `virt-install` | `virt-install` | `virt-install` | `virt-install` (native, not AUR) |
| VM UEFI firmware | `ovmf` | `ovmf` | `edk2-ovmf` | `edk2-ovmf` | `edk2-ovmf` |

### Third-party repositories CasHp adds

Some features have no native package on some distros. CasHp adds these
repositories automatically during setup, GPG-key-pinned, without per-repo
operator approval — a deliberate exception to add-nothing-without-consent that
keeps the zero-config promise:

| Repository | Added on | Why |
|---|---|---|
| Docker official apt/dnf repo | Debian, Ubuntu, RHEL family, Fedora | `docker-ce` is not in the distro repos |
| Sury (Debian) / ondrej-php PPA (Ubuntu) | Debian, Ubuntu | Concurrent multi-version PHP-FPM 5.6–8.4 — no distro ships more than one PHP version |
| Remi, in "safe" (non-module) mode | RHEL family, Fedora | Same multi-version PHP problem; installs versions side by side under `/opt/remi/phpNN/` |
| Zabbly | Debian 11/12, Ubuntu 22.04 | Incus (native from Debian 13 / Ubuntu 24.04 onward) |
| COPR-equivalent Incus repo | RHEL family | No official RHEL/EPEL Incus package exists |

Arch is excluded from this mechanism entirely — every service above is already
a native `extra` package there.

#### How a repository is added

!!! warning "CasHp never runs a vendor install script"
    `get.docker.com`, any `curl | bash`, and any vendor-provided shell
    installer are refused outright. Running an arbitrary downloaded script as
    root is exactly the unverified-execution pattern this product will not
    depend on. This applies uniformly to Docker, Sury/ondrej-php, Remi,
    Zabbly, and the COPR equivalent.

- **CasHp writes the repository definition itself** — an apt source entry at
  `/etc/apt/sources.list.d/{name}.sources` (or `.list`, matching current
  Debian/Ubuntu conventions), or a dnf `.repo` file at
  `/etc/yum.repos.d/{name}.repo`, generated from a URL, distro codename, and
  component that CasHp owns. Nothing is copy-pasted from a vendor script's
  output.
- **GPG handling is explicit and verified** — CasHp fetches each repository's
  public signing key, verifies it against a fingerprint compiled into the
  binary (never trust-on-first-use of whatever the network returns), and
  installs it into the correct per-distro keyring:
  `/etc/apt/keyrings/{name}.gpg` referenced by `signed-by=` in the apt source
  entry, or an imported RPM GPG key referenced by `gpgkey=` in the `.repo`
  file.
- **Signature verification is never disabled to make an install succeed.** If
  a required repository is unreachable, that feature's setup step fails with a
  clear message; unrelated core install steps are not blocked.

### Known platform gaps

- **Alpine has no PHP 5.6 or 7.x at all** — those versions were never packaged
  upstream and are unavailable on Alpine hosts, full stop. PHP 8.2–8.3 are
  available from Alpine 3.20; 8.4 needs 3.21+; 8.5 needs 3.24+/latest.
- **RHEL family loses PHP 5.6 and 7.0–7.3** once a major version's Remi repo
  drops EOL streams. Verify availability against your specific major version
  at install time rather than assuming it.
- **Fedora's Remi coverage is narrower than RHEL's** — Remi currently
  maintains builds only for Fedora 43/44. Fedora 40/41 Remi builds are
  archived, so multi-version PHP-FPM is a degraded-support path there.
- **Arch has no official multi-version PHP path.** Anything beyond the single
  current `php`/`php-fpm` package is AUR-only, needs a build helper
  (`yay`/`paru`), and compiles from source. The AUR packages for PHP
  5.6/7.0–7.3 are effectively abandoned — treat legacy multi-version PHP on
  Arch as **unsupported for production**, not merely degraded.

## Install methods

### Docker

The published image is `ghcr.io/webappsgo/cashp`. It listens on port **80**
inside the container and mounts `/config` and `/data`.

```bash
docker run -d \
  --name cashp-app \
  --hostname cashp \
  --restart always \
  -e PORT=80 \
  -e TZ=America/New_York \
  -p 172.17.0.1:64580:80 \
  -v ./volumes/config:/config \
  -v ./volumes/data:/data \
  ghcr.io/webappsgo/cashp:latest
```

Compose files ship in the repository under `docker/`:

| File | Stack |
|---|---|
| `docker/docker-compose.yml` | Production: app + Valkey cache |
| `docker/all-in-one.yml` | App + embedded PostgreSQL + Valkey + Tor (`:latest-aio` image) |
| `docker/docker-compose.dev.yml` | Development stack |
| `docker/docker-compose.test.yml` | Test stack |

```bash
docker compose -f docker/docker-compose.yml up -d
```

The image's health check runs `/usr/local/bin/cashp --status`, which exits `0`
when healthy and `1` when not.

!!! note "Containers and host management"
    A containerized CasHp can manage container and application workloads, but
    managing *host* services (Postfix, Dovecot, BIND, nftables, libvirt) means
    running on the host itself. Use the binary or service install for a full
    hosting-provider deployment.

### Binary

Download the release artifact for your platform and run it. Binaries follow
the `cashp-{os}-{arch}` naming scheme (`.exe` suffix on Windows):

```bash
wget https://github.com/webappsgo/cashp/releases/latest/download/cashp-linux-amd64
chmod +x cashp-linux-amd64
sudo ./cashp-linux-amd64
```

Prebuilt platforms:

| OS | Architectures |
|---|---|
| Linux | `amd64`, `arm64` |
| macOS (Darwin) | `amd64`, `arm64` |
| Windows | `amd64`, `arm64` (`.exe`) |
| FreeBSD | `amd64`, `arm64` |

The server is Linux-only; the non-Linux builds exist for the CLI
(`cashp-cli-{os}-{arch}`) and agent (`cashp-agent-{os}-{arch}`).

### Service install

`--service --install` detects the platform and init system, installs a system
service when run as root (falling back to a per-user service otherwise),
enables auto-start, and starts it.

```bash
sudo cashp --service --install
sudo cashp --service start
```

| Init system | Platforms | Installed to |
|---|---|---|
| systemd | Linux | `/etc/systemd/system/cashp.service` |
| OpenRC | Alpine, Gentoo, Devuan | `/etc/init.d/cashp` |
| SysVinit | legacy Linux | `/etc/init.d/cashp` (only when systemd and OpenRC are absent) |
| runit | Linux | `/etc/sv/cashp/` (`run`, `log/run`, `supervise/`) |
| rc.d | FreeBSD | `/usr/local/etc/rc.d/cashp` |
| launchd | macOS | `/Library/LaunchDaemons/com.webappsgo.cashp.plist` |
| Windows Service | Windows | Registered service running as `NT SERVICE\cashp` |

Service lifecycle commands:

```bash
sudo cashp --service start
sudo cashp --service stop
sudo cashp --service restart
sudo cashp --service reload
sudo cashp --service --disable
sudo cashp --service --uninstall
```

`--service --disable` stops the service and disables auto-start but keeps the
service file, data, and system user/group; re-enable with `--service
--install`. `--service --uninstall` stops and disables the service, removes
the service file, deletes the config/data/cache/log/backup directories and the
PID file, deletes the system user/group, and leaves the binary in place with
manual-removal instructions. It requires interactive confirmation.

Distro-native equivalents, if you prefer them:

=== "systemd"

    ```bash
    sudo systemctl enable --now cashp
    sudo systemctl status cashp
    ```

=== "OpenRC"

    ```bash
    sudo rc-update add cashp default
    sudo rc-service cashp start
    ```

=== "SysVinit"

    ```bash
    sudo update-rc.d cashp defaults
    sudo service cashp start
    ```

=== "launchd"

    ```bash
    sudo launchctl load /Library/LaunchDaemons/com.webappsgo.cashp.plist
    launchctl list | grep cashp
    ```

### Build from source

See the [Development guide](development.md).

## First run

1. **Start the server.** On first run CasHp writes a complete `server.yml` to
   the config directory with every default filled in, generates its at-rest
   encryption key, and picks a listen port (a random port in the range
   `64000`–`64999` when none is configured; `80` inside a container).
2. **Read the setup token from the console.** The server prints a one-time
   setup token, shown once. There is no "first registered user becomes admin"
   behavior — anonymous self-service registration never grants global-admin or
   account-admin privileges.
3. **Redeem the token** in the web UI to create the primary global admin
   account. That account's primary flag is tamper-proof: it can never be
   changed or revoked through the UI or API by anyone, including other global
   admins. Losing its credentials means re-running the setup-token flow, not
   promoting a different account.
4. **Choose a deployment mode** — `hosting`, `vps`, or `full`. Modes are
   mutually exclusive per install but can be changed later without a
   reinstall.
5. **Verify health.**

    ```bash
    cashp --status
    curl -fsS http://127.0.0.1:64580/server/healthz
    ```

## Directory layout

Paths are derived from the frozen internal org/name pair (`webappsgo`/`cashp`)
and never change when the project is renamed.

| Purpose | Root (system) | Non-root (per-user) |
|---|---|---|
| Config | `/etc/webappsgo/cashp/` | `~/.config/webappsgo/cashp/` |
| Data | `/var/lib/webappsgo/cashp/` | `~/.local/share/webappsgo/cashp/` |
| Cache | `/var/cache/webappsgo/cashp/` | `~/.cache/webappsgo/cashp/` |
| Logs | `/var/log/webappsgo/cashp/` | `~/.local/log/webappsgo/cashp/` |
| PID file | `/var/run/webappsgo/cashp.pid` | inside the data directory |

Override any of them with `--config`, `--data`, `--cache`, `--log`,
`--backup`, and `--pid`. See the [CLI Reference](cli.md).

## Next steps

- [Configuration](configuration.md) — tune `server.yml`
- [Security](security.md) — understand the trust boundaries before exposing
  the panel to the internet
- [Admin Panel](admin.md) — day-to-day administration
