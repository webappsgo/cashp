# cashp

## Project description

CasHp is a self-hostable, all-in-one hosting control panel — a single static
binary (plus embedded web UI) that replaces cPanel, Plesk, CyberPanel,
Proxmox, Portainer, Dockge, Virtualizor, aaPanel, ISPConfig, WHMCS (minus
domain registration), Heroku, Vercel, Railway, and Coolify for individuals,
small teams, and hosting providers. It unifies web hosting, PaaS deployment,
container orchestration, VM management, email, DNS, databases, security, and
clustering under one admin surface, so an operator can run anything from a
single Raspberry Pi to a multi-node cluster without stitching together a
dozen separate tools.

Product philosophy: enterprise-grade security with a consumer-friendly
interface. Security is invisible — always on, never surfaced as a setting a
tenant can weaken, and identical for every tenant regardless of billing tier.
The product works securely zero-config out of the box, fails safe by default,
and never exposes technical jargon in end-user-facing errors.

Target users:

- Solo developers and small teams self-hosting their own sites/apps
- Hosting providers and resellers who need multi-tenant billing and RBAC
- Homelab / cluster operators (including Raspberry Pi clusters) wanting HA
  without a Kubernetes-scale learning curve
- Agencies managing many client sites/apps from one control panel

## Project variables

```
project_name:     cashp
project_org:      webappsgo
internal_name:    cashp
app_name:         CasHp
maintainer_name:  CasjaysDev
maintainer_email: git-admin@casjaysdev.pro
binary_name:      cashp
cli_binary_name:  cashp-cli
```

`internal_name` is FROZEN — it was set once at first-time setup and is never
edited. A project rename only changes `project_name`; `internal_name` stays so
`{config_dir}` / `{data_dir}` / `{log_dir}` / `{cache_dir}` / systemd unit /
`{plist_name}` remain stable on every host.

## Business logic

### Product scope & non-goals

In scope:

- **Web hosting**: multi-domain virtual hosts, multiple concurrent PHP
  versions (5.6 through latest), automatic Let's Encrypt SSL, git-based
  deploys, FTP/SFTP access.
- **PaaS deployment**: auto-detect and run Node.js, Python, Ruby, PHP, Go,
  Java, .NET, and Rust apps; blue-green deploys with rollback.
- **Container orchestration**: run Docker, Incus, and Podman workloads
  simultaneously; per-user isolated networks; support for a custom/private
  OCI registry.
- **VM management**: libvirt/KVM-based VMs, multiple CPU architectures,
  VNC/SPICE console access, live migration, GPU/USB/PCI passthrough.
- **Email stack**: full mail server (send/receive/webmail), anti-spam/AV
  scanning, always on.
- **DNS management**: authoritative DNS hosting, DNSSEC, dynamic DNS updates.
- **Database management**: provision and manage relational, document, and
  key-value databases, with replication/failover for supported engines.
- **Security**: continuous malware scanning, intrusion prevention, and
  web-application firewalling — always on, not opt-in.
- **Clustering / HA**: multi-node clustering with automatic failover and
  floating IPs; distributed storage across nodes; must run acceptably on
  low-power clusters (e.g. Raspberry Pi).
- **Deployment modes**: operator chooses a mode at install time —
  hosting-only, VPS-style (tenant gets a full isolated environment), or full
  (everything enabled).
- **User management / RBAC**: role-based access with at least a global
  administrator role, a per-account administrator role, and an end-user role.
- **Billing / subscriptions**: tiered plans (at minimum a free tier plus paid
  tiers) that gate resource limits, not core functionality; self-service
  upgrade/downgrade.
- **Backup & disaster recovery**: scheduled backups of hosted sites, apps,
  databases, and configuration; restorable without vendor lock-in.
- **Monitoring & alerting**: resource and service health visibility, with
  operator-configurable alerts.
- **Support system**: a way for tenants to raise and track issues with the
  operator.
- **API / automation**: every control-panel action must also be reachable
  through an API, so the panel is not the only way to operate the platform.
- **Username system**: cluster-wide-unique usernames; a deleted username is
  never reused (tombstoned); reserved/blacklisted names (system, service,
  brand, offensive) are rejected with a specific message, while an
  already-taken (non-blacklisted) name gets a generic "unavailable" message so
  account existence can't be enumerated by username probing.
- **Billing / subscriptions**: at least one payment provider must be
  supported, with the architecture able to support more than one
  simultaneously; trial periods with automatic conversion; prepaid/credit
  account balances with optional auto-recharge; usage-based overage billing;
  invoices issued for every billing event; compliance-framework toggles
  (data-privacy, security, financial, healthcare, accessibility, etc.) that an
  operator can enable per their regulatory environment.
- **Backup & disaster recovery**: space-efficient by design (deduplication +
  compression so backups consume a small fraction of primary storage);
  grandfather-father-son-style retention (recent dailies, several weeklies, a
  few monthlies) rather than keeping every snapshot forever; full
  system/bare-metal restore, not just per-service restore.
- **Support system**: ticketing with a defined lifecycle (open → in progress
  → awaiting tenant → resolved → closed, reopenable), priority levels, and a
  self-service knowledge base a tenant can search before filing a ticket.

Non-goals:

- No "phone home" licensing or feature gating tied to payment — 100% of
  features available under the project's open-source license; billing tiers
  govern resource quotas, not feature availability.
- No requirement to depend on any single container/virtualization backend —
  Docker, Incus, Podman, and libvirt/KVM must all remain first-class,
  simultaneously usable options.
- No abandonment of low-power hardware support — the product must remain
  usable on small clusters, not just large multi-core servers.
- **Not a domain registrar** — cashp integrates with external registrars for
  nameserver/DNS-record management; it never sells or registers domains
  itself.
- No AI/ML-based moderation or decision-making in the product.

### Roles & permissions

| Role | Scope | Powers |
|---|---|---|
| Global admin | Whole installation / cluster | Full control: all tenants, all nodes, billing plans, security policy, platform-wide settings |
| Account admin | Their own hosting account / tenant | Manage their own sites, apps, containers, VMs, databases, email domains, DNS zones, users under their account |
| End user | Whatever an account admin grants them | Use the specific services granted (e.g. a single site's file manager, a single mailbox, a single database) |

Global admin setup is token-based, not registration-order-based: on first run
the server generates a one-time setup token (shown once, in console); whoever
redeems it creates the primary global admin account. There is no "first
registered user automatically becomes admin" behavior — anonymous/self-service
registration never grants global-admin or account-admin privileges. The
primary global admin is tamper-proof: that flag can never be changed or
revoked through the UI or API by anyone, including other global admins;
recovery (lost credentials) re-runs the setup-token flow rather than
promoting another account.

Global admins cannot view another global admin's credentials or account
details (password, API token, 2FA secret) — only total admin count and
online-status/username are visible to peers. A locked-out non-primary global
admin can only be recovered by delete-and-re-invite; even the primary admin
cannot reset another admin's password, view their credentials, or disable
their 2FA directly.

### Compatibility / parity requirements

- Must be a credible self-hosted replacement for cPanel/Plesk/CyberPanel-class
  panels for the web-hosting feature set (vhosts, multi-PHP, SSL, FTP/SFTP,
  git deploy) — an operator migrating from one of those tools should find
  equivalent functionality.
- PaaS auto-detection must recognize and correctly run standard idiomatic
  project layouts for each supported language (Node.js, Python, Ruby, PHP,
  Go, Java, .NET, Rust) without hand-written buildpacks.
- Must support running Docker, Incus, and Podman workloads side by side on
  the same node — not a choice made once at install time.

### Constraints & trust boundaries

- Deployment modes are mutually exclusive per install (hosting / vps / full)
  and must be selectable without reinstalling from scratch when an operator
  grows from one mode into another.
- Multi-tenant isolation is required at every layer the product touches:
  per-tenant containers get isolated networks; per-tenant VMs are fully
  separate guests; per-tenant databases and mailboxes must not be readable by
  other tenants.
- Security features (malware scanning, intrusion prevention, WAF) are
  always-on baseline protection, not an optional add-on a tenant can disable
  for the whole platform.
- Clustering/HA behavior (floating IPs, distributed storage, node failover)
  must degrade gracefully on constrained hardware (e.g. a Raspberry Pi
  cluster) rather than requiring datacenter-class nodes.
- Billing tiers may restrict resource quotas (storage, bandwidth, number of
  sites/VMs/containers, etc.) but must never restrict which features exist —
  a free-tier tenant runs the same code path as a paid tenant, just with
  lower limits.

### Abuse cases

- A tenant must not be able to access, enumerate, or affect another tenant's
  sites, containers, VMs, databases, email, or DNS zones through any surface
  (UI, API, container escape, shared-network leakage).
- A tenant on the free/lowest billing tier must not be able to bypass quota
  enforcement to consume resources reserved for paid tiers.
- An end user granted access to one narrow service (e.g. a single mailbox)
  must not be able to escalate to account-admin or global-admin capabilities.
- The always-on security stack (AV/IPS/WAF) must not be disableable by a
  tenant for their own account in a way that endangers other tenants sharing
  the same node/cluster.
- Username squatting/enumeration: a prospective tenant must not be able to
  determine whether a specific username or email is already registered by
  probing signup/username-availability endpoints (blacklisted names get a
  specific "reserved" message; taken-but-not-blacklisted names get the same
  generic "unavailable" response as any other taken name).
- A deleted tenant's username must never become available for reuse
  (tombstoned), preventing a new registrant from impersonating a former
  tenant's identity (e.g. inheriting old inbound email/DNS references).

