# Contributing to CasHp

## Local setup and checks

CasHp builds and tests Go code in Docker using `casjaysdev/go:latest`; GNU Make
and Docker are required. Python and the packages in `docs/requirements.txt`
are needed to build the documentation.

```sh
make local
make build
make test
python3 -m pip install -r docs/requirements.txt
mkdocs build --strict
```

See the [development guide](../docs/development.md) for the repository layout,
test suites, and build variables.

## Changes

The long-lived branch is `main`. Maintainers and automation push changes to
`main` through the repository's `gitcommit` workflow. External contributors
should work in a fork and open a pull request against `main`. Do not force-push
or delete `main`.

Include tests for behavior changes and update the relevant documentation,
configuration examples, and API inventory with code changes. Keep changes
focused and describe the commands and results used to verify them. Pull
requests must complete the checklist in the repository's pull request template.

## Vulnerabilities

Do not report security vulnerabilities in public issues or pull requests. Use
[GitHub private vulnerability reporting](https://github.com/webappsgo/cashp/security/advisories/new)
and follow the process in [SECURITY.md](SECURITY.md). Deployed instances also
publish `/.well-known/security.txt`, `/server/security`, and
`/server/contact?security_id=...` for their security-reporting flow.
