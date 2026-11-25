# Security

CasHp is under active development and is not ready for production deployment.
Security packages and feature modules exist in the source tree, but several
are not yet connected to the running server. Their presence in source code or
this guide does not mean a protection is active in a deployed build. The
generated API route list and the current configuration are better indicators
of what is actually running.

## Current server protections

The HTTP server uses bounded request timeouts, configured security headers,
optional HTTPS/TLS handling, and request rate limiting. The admin panel has
session-based access checks and CSRF protection for its forms. The auth package
contains account/session logic and credential protections, but is not yet
composed into the main HTTP application. Do not expose the development server
to untrusted networks.

Startup creates the `app_secrets` records for the installation, cookie-signing,
and CSRF keys in the server database and preserves them across restarts. Their
full use by shared authentication and the specified rotation flows is not yet
integrated.

## Not yet implemented end to end

The current server does not provide the full multi-tenant isolation and
authorization model, externally authenticated user workflows, feature-wide
rate limiting, automated firewall/antivirus management, GeoIP-based policy,
tenant workload isolation, payment processing, or security event pipeline
described by the product plan. Tor and I2P publication are also not complete.

The `src/auth`, `src/security`, and feature packages contain functionality that
is being integrated and verified. Until those paths are wired into the
composition root and covered by route-level tests, treat their controls as
unavailable.

## Configuration and operational safety

Keep database files and configuration readable only by the service account.
Do not commit credentials or publish bearer tokens in logs, support requests,
or documentation examples. Use TLS termination you control for any local
evaluation involving credentials. The project does not yet provide a supported
production service installation procedure.

## Reporting a vulnerability

For vulnerabilities in CasHp itself, use the
[project security contact](https://github.com/webappsgo/cashp/security). Please
avoid posting details of an unpatched vulnerability in a public issue.
