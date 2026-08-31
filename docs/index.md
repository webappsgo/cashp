# CasHp

**CasHp** (`cashp`) is a self-hostable, all-in-one hosting control panel — a
single static binary plus an embedded web UI that unifies web hosting, PaaS
deployment, container orchestration, VM management, email, DNS, databases,
security, and clustering under one admin surface.

It is built to replace the stack you would otherwise assemble from cPanel,
Plesk, CyberPanel, Proxmox, Portainer, Dockge, Virtualizor, aaPanel,
ISPConfig, WHMCS (minus domain registration), Heroku, Vercel, Railway, and
Coolify — so one operator can run anything from a single Raspberry Pi to a
multi-node cluster without stitching a dozen separate tools together.

!!! info "Product philosophy"
    Enterprise-grade security with a consumer-friendly interface. Security is
    invisible — always on, never surfaced as a setting a tenant can weaken,
    and identical for every tenant regardless of billing tier. CasHp works
    securely with zero configuration out of the box and fails safe by default.

## Quick Start

=== "Docker"

    ```bash
    docker run -d \
      --name cashp-app \
      -p 172.17.0.1:64580:80 \
      -v ./volumes/config:/config \
      -v ./volumes/data:/data \
      ghcr.io/webappsgo/cashp:latest
    ```

=== "Binary"

    ```bash
    sudo ./cashp-linux-amd64 --config /etc/webappsgo/cashp
    ```

=== "Service"

    ```bash
    sudo cashp --service --install
    ```

On first run the server generates a **one-time setup token** and prints it to
the console. Redeem it in the web UI to create the primary global admin
account. See [Installation](installation.md) for the full first-run flow.

## Features

- 🌐 **Web hosting** — multi-domain virtual hosts, concurrent PHP versions
  (5.6 through latest), automatic Let's Encrypt SSL, git-based deploys,
  FTP/SFTP access.
- 🚀 **PaaS deployment** — auto-detects and runs Node.js, Python, Ruby, PHP,
  Go, Java, .NET, and Rust apps; blue-green deploys with rollback.
- 📦 **Container orchestration** — Docker, Incus, and Podman workloads side by
  side on the same node, per-tenant isolated networks, custom/private OCI
  registry support.
- 🖥️ **VM management** — libvirt/KVM guests, multiple CPU architectures,
  VNC/SPICE console access, live migration, GPU/USB/PCI passthrough with
  capability autodetection (disabled automatically when the host cannot
  virtualize).
- ✉️ **Email stack** — full send/receive/webmail mail server with always-on
  anti-spam and anti-virus scanning.
- 🔤 **DNS management** — authoritative DNS hosting, DNSSEC, dynamic DNS
  updates.
- 🗄️ **Database management** — relational, document, and key-value engines
  with replication/failover where the engine supports it.
- 🛡️ **Security** — continuous malware scanning, intrusion prevention, and web
  application firewalling. Always on, never opt-in, never tenant-disableable.
- 🔗 **Clustering / HA** — multi-node clustering with automatic failover,
  floating IPs, and distributed storage that degrades gracefully on low-power
  hardware such as a Raspberry Pi cluster.
- 👥 **Multi-tenancy & RBAC** — global admin, account admin, and end user
  roles, organizations, and per-tenant custom domains.
- 💳 **Billing & subscriptions** — tiered plans that gate *resource quotas*,
  never feature availability; trials, prepaid balances, usage overage, and an
  invoice for every billing event.
- 💾 **Backup & disaster recovery** — deduplicated, compressed, scheduled
  backups with grandfather-father-son retention and full bare-metal restore.
- 📈 **Monitoring & alerting** — Prometheus-compatible metrics with
  operator-configurable alerting.
- 🎫 **Support system** — ticketing with a defined lifecycle plus a
  self-service knowledge base.
- 🔌 **API / automation** — every control-panel action is reachable through the
  API, so the panel is never the only way to operate the platform.

## Non-goals

CasHp deliberately does **not** do the following:

- **No feature gating tied to payment.** 100% of features are available under
  the MIT license; billing tiers govern resource quotas only. A free-tier
  tenant runs the same code path as a paid tenant, just with lower limits.
- **No phone-home licensing.**
- **No single-backend lock-in.** Docker, Incus, Podman, and libvirt/KVM all
  remain first-class, simultaneously usable options.
- **Not a domain registrar.** CasHp integrates with external registrars for
  nameserver and DNS-record management; it never sells or registers domains.
- **No abandonment of low-power hardware.**
- **No AI/ML-based moderation or decision-making.**

## Deployment modes

An operator picks a mode at install time. Modes are mutually exclusive per
install, and an operator can grow from one into another without reinstalling
from scratch.

| Mode | What it enables |
|---|---|
| `hosting` | Web hosting, mail, DNS, databases — the shared-hosting feature set |
| `vps` | Each tenant receives a full isolated environment (VM/container) |
| `full` | Everything above, enabled together |

## Documentation

- [Installation](installation.md) — supported operating systems, per-distro
  install, Docker, binary, and service setup
- [Configuration](configuration.md) — every `server.yml` key, its default, and
  the environment variables that override it
- [API Reference](api.md) — REST routes, authentication, Swagger, GraphQL
- [CLI Reference](cli.md) — `cashp`, `cashp-cli`, and `cashp-agent` flags
- [Admin Panel](admin.md) — the administration surface and what lives where
- [Security](security.md) — auth, trust boundaries, threat model, public
  security endpoints, and reporting
- [Integrations](integrations.md) — external identity, discovery endpoints,
  registrars, payment providers, and OCI registries
- [Development](development.md) — building, testing, and contributing

## Links

- [Repository](https://github.com/webappsgo/cashp)
- [Issue tracker](https://github.com/webappsgo/cashp/issues)
- [Releases](https://github.com/webappsgo/cashp/releases)

## License

MIT — see
[LICENSE.md](https://github.com/webappsgo/cashp/blob/main/LICENSE.md).
