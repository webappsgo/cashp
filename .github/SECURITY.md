# Security policy

## Supported versions

There are no stable releases yet. Security fixes target the current `main`
branch. When stable releases are published, the latest stable release will be
the supported release; upgrade before reporting an issue against an older
version.

## Report a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/webappsgo/cashp/security/advisories/new)
as the primary channel. On a mirror without private vulnerability reporting,
submit the report to the GitHub origin repository above. Do not file
vulnerabilities as public issues or pull requests.

Each deployed instance also publishes `/.well-known/security.txt` and
`/server/security`. Its private report form is `/server/contact?security_id=...`;
the instance operator may configure an email address for notification or CC,
but that address does not replace the GitHub reporting channel for source-code
vulnerabilities. See the [security documentation](../docs/security.md).

Maintainers will acknowledge a report, assess its impact, coordinate a fix and
disclosure with the reporter, and publish an advisory when affected users can
take action. Please include affected versions, impact, and reproducible details
in the private report. Avoid sharing exploit details publicly before
coordinated disclosure.
