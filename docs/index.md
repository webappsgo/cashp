# CasHp

CasHp is a self-hosted hosting control panel for web hosting, applications,
containers, virtual machines, email, DNS, databases, security, backups,
monitoring, and support. It runs as a Go server and includes the required
`cashp-cli` companion plus the optional `cashp-agent` for remote machines.

The server renders its public pages and administration interface with Go
templates. Features are available without JavaScript; browser scripts provide
progressive enhancements. The versioned REST API, OpenAPI document, and
GraphQL explorer are served by the running server.

## Build and run

Requirements: Docker and GNU Make. Builds run in the project Go toolchain
container.

```sh
git clone https://github.com/webappsgo/cashp.git
cd cashp
make local
```

The local build writes `cashp`, `cashp-cli`, and `cashp-agent` to
`binaries/`. Run `binaries/cashp --help` for server options. The server creates
its configuration and runtime directories on first start. See the
[installation](installation.md) and [configuration](configuration.md) guides.

## Guides

- [Installation](installation.md)
- [Configuration](configuration.md)
- [API reference](api.md)
- [CLI reference](cli.md)
- [Admin panel](admin.md)
- [Security](security.md)
- [Integrations](integrations.md)
- [Development](development.md)
- [Translation and accessibility](translating.md)

## Live API contract

The server generates OpenAPI from its registered routes. Use the versioned
document for the complete method, path, authentication, and response
information. Its URL is `/api/{api_version}/server/swagger`; the interactive
explorer is at `/server/docs/swagger`. The configured API version and base path
apply to all generated links.

## Project resources

- [Source repository](https://github.com/webappsgo/cashp)
- [Issue tracker](https://github.com/webappsgo/cashp/issues)
- [Product requirements](https://github.com/webappsgo/cashp/blob/main/IDEA.md)
- [License](https://github.com/webappsgo/cashp/blob/main/LICENSE.md)
