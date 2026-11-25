# Installation and First Run

CasHp is in active development and does not yet have a supported production
installation or release process. The repository currently provides a source
build for local evaluation. Several advertised management features, including
service installation and complete first-run account setup, are not implemented
end to end yet.

## Build from source

Use Docker with the Go toolchain image required by the project. From the
repository root:

```sh
make local
```

This creates `cashp`, `cashp-cli`, and `cashp-agent` in `binaries/`. To build
release artifacts for all supported target pairs:

```sh
make build
```

The release build covers Linux, macOS, Windows, and FreeBSD on amd64 and arm64.
The server is presently intended to run on Linux; cross-compilation alone does
not establish that a target has been integration-tested.

## Run the server for evaluation

Run the server in the foreground:

```sh
./binaries/cashp -mode development
```

The binary reads `server.yml` from the detected configuration directory. The
configuration package supplies defaults and can create the file when needed.
Current server command-line options are `-color`, `-debug`, `-mode`, `-v`, and
`-version`; see the [command reference](cli.md#server-cashp). Configure the
listen address and ports in `server.yml` rather than relying on undocumented
flags.

The HTTP server currently mounts the public server-rendered pages, admin panel,
health/readiness/version endpoints, metrics, and API documentation endpoints.
Account, organization, billing, support, and hosting workflows are not yet
fully connected. Do not expose this development build as a production service.

## Configuration and data

Configuration defaults and detected file locations are documented in the
[configuration guide](configuration.md). Review the generated `server.yml`
before starting the server. Do not place credentials in source control.

## Current limitations

- There is no supported package installation, published binary release, or
  verified production Docker deployment yet.
- The service-management options described by older documentation are not
  available in the current server binary.
- Building a target binary does not verify service integration on that OS.
- Feature names shown by `cashp-cli` do not imply that their server APIs are
  already implemented; see the [API reference](api.md).

See the [development guide](development.md) for build and contributor details.
