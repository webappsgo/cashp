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
  VNC/SPICE console access, live migration, GPU/USB/PCI passthrough with autodetection of capabilities(IE: if host does not support virtualization it will be disabled).
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
  the same node — not a choice made once at install time
- The frontend and backend reflects what is actually avaliable/enabled.

### Supported operating systems

Linux only — no macOS/Windows/BSD host targets for the server (cashp's
build matrix in AI.md PART 7 covers client binaries, not where the server
manages host services).

| Family | Floor | Package manager | Notes |
|---|---|---|---|
| Debian (+ derivatives) | 11 (bullseye)+ | apt | 8 (jessie) and 10 (buster) are unreachable from default `deb.debian.org` sources (archived) — not supported, even though older releases were originally targeted; 11 is the real floor |
| Ubuntu (+ derivatives) | 22.04+ | apt | |
| Alpine | 3.20+ | apk | `community` repo must be enabled — most non-core managed services live there, not `main` |
| RHEL family (+ derivatives) | 8+ | dnf/yum | Rocky Linux, AlmaLinux, Oracle Linux, CentOS Stream all treated identically; EPEL required for several services |
| Fedora | 40+ | dnf | Modularity is gone (`dnf module list php` returns nothing) — one PHP version natively, same multi-version problem as RHEL |
| Arch Linux (+ derivatives) | rolling | pacman | Manjaro, EndeavourOS, etc. treated identically; rolling release, no version floor to track |

### Managed services & OS package mapping

Package names verified against live containers per distro family, not
assumed. `-` means no separate package is needed beyond what's already
listed.

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
| Container engine | `docker-ce` + `docker-ce-cli` + `containerd.io` (Docker's official apt repo, not distro `docker.io`) | `docker` + `docker-cli` (Alpine's own package — Docker publishes no official Alpine repo) | `docker-ce` + `docker-ce-cli` + `containerd.io` (Docker's official dnf repo — not in RHEL/EPEL) | `docker-ce` + `docker-ce-cli` + `containerd.io` (Docker's official dnf repo) | `docker` (Arch's own `extra` package — no third-party repo needed) |
| Container engine (Incus) | `incus` | `incus` | not packaged — third-party repo required | `incus` (native, no third-party repo needed) | `incus` |
| Container engine (Podman) | `podman` | `podman` | `podman` | `podman` | `podman` |
| VM hypervisor | `libvirt-daemon-system` | `libvirt` + `libvirt-daemon` + `libvirt-qemu` + `libvirt-client` | `libvirt-daemon-config-network` + `libvirt-daemon-kvm` | `libvirt-daemon-config-network` + `libvirt-daemon-kvm` | `libvirt` |
| VM emulator | `qemu-system-x86` + `qemu-system-arm` (`qemu-kvm` no longer exists as a package) | `qemu-system-x86_64` + `qemu-system-aarch64` | `qemu-kvm` (name still valid on RHEL) | `qemu-kvm` | `qemu-full` (or lighter `qemu-desktop`) |
| VM install helper | `virtinst` | `virt-install` | `virt-install` | `virt-install` | `virt-install` (native, not AUR) |
| VM UEFI firmware | `ovmf` | `ovmf` | `edk2-ovmf` | `edk2-ovmf` | `edk2-ovmf` |

**Third-party repos cashp adds automatically** (GPG-key-pinned, not
arbitrary): Docker's official apt/dnf repo on every apt/dnf family (Debian,
Ubuntu, RHEL family, Fedora) for `docker-ce`; Sury (Debian) / ondrej-php PPA
(Ubuntu) for concurrent multi-version PHP-FPM 5.6–8.4 — no distro ships
more than one PHP version in its default repos; Remi in "safe" (non-module)
mode for the same reason on RHEL family and on Fedora (Fedora dropped PHP
modularity entirely — it has the identical single-version problem as
RHEL), installing versions side-by-side under `/opt/remi/phpNN/`; Zabbly
for Incus on Debian 11/12 and Ubuntu 22.04 (native from Debian 13 / Ubuntu
24.04 onward); a COPR-equivalent Incus repo on RHEL family (no official
RHEL/EPEL package exists — Fedora, unlike RHEL, packages Incus natively
and needs no third-party repo for it).

**How cashp adds a third-party repo (implementation contract, not just
intent):**

- **Never runs a vendor install script** (e.g. `get.docker.com`,
  `curl | bash`, or any vendor-provided shell installer) — running an
  arbitrary downloaded script as root is exactly the "phone home" /
  unverified-execution pattern this product refuses to depend on. This
  applies uniformly to Docker, Sury/ondrej-php, Remi, Zabbly, and any COPR
  equivalent.
- **cashp writes the repo definition file itself**: an apt source entry
  under `/etc/apt/sources.list.d/{name}.sources` (or `.list`, matching
  current Debian/Ubuntu conventions) or a dnf `.repo` file under
  `/etc/yum.repos.d/{name}.repo`, generated from a value cashp owns (URL,
  distro codename, component), not copy-pasted from a vendor script's
  output.
- **GPG key handling is explicit and verified, not implicit**: cashp
  fetches each repo's public signing key, verifies it against a pinned
  fingerprint compiled into cashp (never trust-on-first-use of whatever the
  network returns), and installs it into the correct per-distro keyring —
  `/etc/apt/keyrings/{name}.gpg` referenced by `signed-by=` in the apt
  source entry (apt), or an imported RPM GPG key referenced by `gpgkey=` in
  the `.repo` file (dnf). Signature verification is never disabled to make
  an install succeed.
- Arch is excluded from this mechanism entirely — Docker, Incus, and every
  other service needed are already native `extra` packages there (see the
  Arch Linux row below); no third-party repo is added on Arch for anything
  this table covers except the AUR path for legacy multi-version PHP, which
  is documented separately as a degraded-support path, not an automated
  repo add.

**Known platform gaps:**

- Alpine cannot provide PHP 5.6 or any 7.x version at all (never packaged
  upstream) — those versions are unavailable on Alpine hosts, full stop.
  PHP 8.2–8.3 are available from Alpine 3.20; 8.4 needs 3.21+; 8.5 needs
  3.24+/latest.
- RHEL family has no PHP 5.6/7.0–7.3 support once a major version's Remi
  repo drops EOL streams — verify availability against the specific major
  version at install time rather than assuming it.
- Fedora's Remi coverage is version-gated and narrower than RHEL's: Remi
  currently maintains builds only for Fedora 43/44. Fedora 40/41 Remi
  builds are archived and effectively unsupported, so multi-version PHP-FPM
  is a degraded-support path on Fedora 40/41 — verify the running Fedora
  version has an active Remi build before relying on it, same as the RHEL
  EOL-stream check above.
- Arch has no official multi-version PHP path at all: anything beyond the
  single current `php`/`php-fpm` package is AUR-only, requires a build
  helper (`yay`/`paru`), and is compiled from source rather than installed
  from a binary repo. The AUR packages for PHP 5.6/7.0–7.3 are effectively
  abandoned/unmaintained — treat legacy multi-version PHP on Arch as
  unsupported for production use, not merely degraded.

### Service hosting model

- **Native host services** (real distro packages, managed via
  systemd/OpenRC): web server, PHP-FPM, mail stack (Postfix, Dovecot,
  Amavis/amavisd-new, SpamAssassin, ClamAV, OpenDKIM, OpenDMARC), DNS server
  (BIND), fail2ban, nftables, and the container/VM engines themselves
  (Docker, Incus, Podman, libvirt/QEMU).
- **App-managed containers** (cashp-orchestrated, never tenant-defined):
  PostgreSQL, MariaDB, MongoDB, Valkey/Redis run as containers managed
  entirely by cashp (via whichever container backend is configured) so
  every node in a cluster runs an identical, cashp-controlled version —
  this is an application-level deployment choice, not a feature exposed to
  tenants. Container volumes map to standard OS data directories (AI.md
  PART 4 path conventions), never to project-local paths.
- **Tenant-defined workloads** (containers/VMs a tenant creates through
  PaaS/container/VM features) are a separate category from both of the
  above and are already covered by this document's existing container
  orchestration and VM management scope.

### Data model & sensitivity

Core entities:

- **Tenant (Account)**: id, owner user, deployment mode assignment, billing
  plan, resource quotas, created_at
- **User**: id, username (cluster-unique, tombstoned on delete), email,
  password hash, role (global_admin / account_admin / end_user), 2FA state,
  status
- **Site**: id, tenant_id, domain(s), PHP version, SSL cert ref, git deploy
  source
- **App (PaaS)**: id, tenant_id, language/runtime, deploy source, current
  release, rollback history
- **Container workload**: id, tenant_id, backend (Docker/Incus/Podman),
  network namespace ref
- **VM**: id, tenant_id, hypervisor config, architecture, passthrough
  devices, console access ref
- **Database instance**: id, tenant_id, engine, replication config,
  credentials ref
- **Mailbox**: id, tenant_id, domain, quota, AV/spam scan status
- **DNS Zone**: id, tenant_id, records, DNSSEC key material
- **Billing Account**: id, tenant_id, plan, payment provider ref, balance,
  auto-recharge setting
- **Invoice**: id, billing_account_id, line items, status
- **Backup Job**: id, tenant_id, schedule, retention policy, encryption key
  ref
- **Support Ticket**: id, tenant_id, state, priority, messages

Sensitivity classification:

- **Highest** (encrypted at rest, never logged): password hashes, 2FA
  secrets, API/session tokens, database credentials, backup encryption keys,
  DNSSEC private keys, VM/container root credentials, payment provider
  tokens
- **High** (tenant-isolated, access-controlled): site/app source code and
  env vars, mailbox contents, database contents, VM disk images, DNS zone
  records
- **Moderate**: billing/invoice data (financial PII), support ticket
  contents
- **Low**: usernames (still governed by tombstone/anti-enumeration rules
  below), public site content, resource usage metrics

### Trust boundaries & external services

- **Third-party OS package repos** (Sury/ondrej-php PPA, Remi, Zabbly,
  COPR-equivalent — see "Managed services & OS package mapping" above):
  trusted only as a package delivery channel for features no distro
  packages natively (concurrent multi-version PHP-FPM, Incus on
  Debian/Ubuntu/RHEL where absent from default repos); every such repo is
  added with its GPG signing key pinned, never with signature checking
  disabled. Failure mode: if a required third-party repo is unreachable,
  the specific feature's setup step fails with a clear message — it never
  silently disables signature verification to make the install succeed,
  and it never blocks unrelated core install steps.
- **Let's Encrypt (ACME)**: trusted for domain-validated certificate
  issuance only. Failure mode: SSL provisioning fails gracefully — the site
  keeps serving on its existing cert or falls back to HTTP with a warning,
  and issuance is retried on schedule (see PART 15/19 of AI.md).
- **External DNS registrars**: trusted only for nameserver/DNS-record
  read-write via their API; cashp never stores registrar account
  credentials beyond that API scope. Failure mode: DNS changes queue and
  retry, operator is alerted, no fallback to unauthenticated registrar
  access.
- **Payment provider(s)** (pluggable, operator-selected): trusted for
  payment processing and webhook-driven billing events only; cashp never
  stores raw card/bank data (PCI scope stays with the provider). Failure
  mode: on provider outage, billing events queue for retry — service is not
  suspended solely due to a transient billing-check failure.
- **External/custom OCI registry** (tenant-configured): untrusted by
  default. Images pulled from a custom registry run inside the same
  per-tenant isolation as any other container workload; cashp does not vet
  third-party image contents.
- **Tenant-submitted PaaS source / container / VM images**: untrusted;
  treated as arbitrary code execution by design and contained via
  per-tenant isolation (network, filesystem, resource limits), not by
  static review of the content.
- **GeoIP database feed**: trusted only as an IP-to-location data source.
  Failure mode: a stale or missing database degrades gracefully and never
  blocks core hosting functionality.

### Threat model & abuse cases

**Primary assets**: tenant site/app source and data, tenant databases and
mailboxes, tenant VM/container workloads, DNS zones, billing/payment data,
credentials and tokens, the host/cluster nodes themselves.

**Trusted inputs/integrations**: Let's Encrypt ACME responses, the
operator's own admin actions, cashp's own scheduler/internal services.

**Untrusted inputs/integrations**: all tenant-submitted content (site
files, PaaS source, container/VM images, DNS record values, email content),
all external API responses from registrars/payment providers (validated,
never blindly trusted), all inbound network traffic.

**Attacker/abuser goals**: cross-tenant data access or takeover;
container/VM escape to host or other tenants; privilege escalation from
end-user to account-admin/global-admin; billing fraud (quota bypass,
chargeback abuse); DNS/mail hijack of another tenant's domain; resource
exhaustion/DoS against shared nodes; credential theft (session/API tokens);
username enumeration for targeted attacks.

**Abuse cases and required defenses:**

- A tenant must not be able to access, enumerate, or affect another
  tenant's sites, containers, VMs, databases, email, or DNS zones through
  any surface (UI, API, container escape, shared-network leakage) —
  defended by per-tenant network isolation and mandatory tenant-scoped
  authorization checks on every resource access (AI.md PART 11).
- A tenant on the free/lowest billing tier must not be able to bypass quota
  enforcement to consume resources reserved for paid tiers — defended by
  server-enforced quota checks at every resource-creation path, never
  client-trusted.
- An end user granted access to one narrow service (e.g. a single mailbox)
  must not be able to escalate to account-admin or global-admin
  capabilities — defended by strict RBAC boundary enforcement with no
  implicit trust elevation.
- The always-on security stack (AV/IPS/WAF) must not be disableable by a
  tenant for their own account in a way that endangers other tenants
  sharing the same node/cluster — defended by keeping the security stack
  platform-controlled, not tenant-configurable, at any billing tier.
- Username squatting/enumeration: a prospective tenant must not be able to
  determine whether a specific username or email is already registered by
  probing signup/username-availability endpoints — defended by a generic
  "unavailable" response for taken-but-not-blacklisted names, with a
  distinct "reserved" message only for blacklisted names.
- A deleted tenant's username must never become available for reuse,
  preventing a new registrant from impersonating a former tenant's identity
  (e.g. inheriting old inbound email/DNS references) — defended by
  permanent tombstoning.
- Malicious PaaS/container/VM payloads attacking the host — isolation is
  the primary defense, not content inspection; malware scanning covers
  stored files, not a guarantee against zero-day container/VM escapes.
- Payment/billing fraud (fake webhooks, replay) — defended by mandatory
  payment-provider webhook signature verification; billing state changes
  only from verified provider events.

### Security decisions & exceptions

- **Root/elevated host privileges**: the cashp server process requires
  elevated privileges to manage VMs (libvirt/KVM), containers
  (Docker/Incus/Podman), and system services (mail, DNS, firewall). This is
  an intentional, non-negotiable exception to least-privilege-by-default —
  mitigated by strict internal RBAC and per-tenant isolation, not by
  running the whole server unprivileged.
- **Tenant code execution is intentional**: PaaS/container/VM features mean
  cashp intentionally runs untrusted tenant-submitted code by design (not a
  vulnerability) — isolation (network, filesystem, resource limits) is the
  control, not code review or content inspection.
- **External registrar API access is minimally scoped**: cashp is granted
  only the registrar API scope needed for DNS record management; it is
  explicitly not a domain registrar and never handles domain
  purchase/transfer/registration credentials beyond DNS scope.
- **Custom/private OCI registries are a tenant-side trust extension**:
  allowing a tenant to configure their own registry extends trust from that
  tenant to their own workloads, not from cashp to the registry — cashp
  does not scan or vet pulled image contents beyond its existing AV
  integration.
- **Third-party OS package repos are added automatically, not on request**:
  to keep the product's "zero-config, secure by default" promise, cashp
  adds and pins (by GPG key) the third-party repos listed in "Managed
  services & OS package mapping" during setup, without requiring per-repo
  operator approval. This is an intentional exception to
  add-nothing-without-consent — mitigated by cashp writing the repo file
  itself and pinning exact signing-key fingerprints (never a vendor
  `curl | bash` install script, never disabled signature verification) and
  by only ever adding repos needed for features already documented in this
  file, never arbitrary ones.

### Constraints

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

