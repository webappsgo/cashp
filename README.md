# CasHp

[![License](https://img.shields.io/github/license/webappsgo/cashp)](LICENSE.md)

CasHp is a self-hostable, all-in-one hosting control panel — a single
static binary (plus embedded web UI) that replaces cPanel, Plesk,
CyberPanel, Proxmox, Portainer, Dockge, Virtualizor, aaPanel, ISPConfig,
WHMCS (minus domain registration), Heroku, Vercel, Railway, and Coolify for
individuals, small teams, and hosting providers. It unifies web hosting,
PaaS deployment, container orchestration, VM management, email, DNS,
databases, security, and clustering under one admin surface, so an operator
can run anything from a single Raspberry Pi to a multi-node cluster without
stitching together a dozen separate tools.

See [IDEA.md](IDEA.md) for the full product definition and business logic,
and [AI.md](AI.md) for the technical specification.

---

## 📦 Install

Prebuilt binaries and installation instructions will be published once the
first release ships. In the meantime, build from source:

```sh
git clone https://github.com/webappsgo/cashp
cd cashp
make build/local
```

---

## 🛠️ Development

```sh
# Clone and build
git clone https://github.com/webappsgo/cashp
cd cashp
make dev
```

See `TODO.AI.md` for the current implementation backlog.

---

## 📄 License

MIT — see [LICENSE.md](LICENSE.md)

---

## Author

🤖 casjay: [Github](https://github.com/casjay) 🤖
