# Browser Pages

CasHp serves its public, account, organization, tenant, and administration
interfaces as server-rendered HTML. Forms submit directly to the server, so
the primary flows remain usable without JavaScript; scripts add optional
progressive enhancements. Paths below are relative to the instance root and
honor the configured base path where one is set.

## Public pages

| Page | Path | Purpose |
|---|---|---|
| Home | `/` | Instance landing page and links to enabled services |
| About | `/server/about` | Instance and operator information |
| Privacy | `/server/privacy` | Privacy practices, cookie controls, and applicable privacy choices |
| Data protection contact | `/server/dpo` | Data protection officer contact information when configured |
| Contact | `/server/contact` | Contact and report submissions |
| Help | `/server/help` | Public help and registered guides |
| Terms | `/server/terms` | Terms of service |
| Security | `/server/security` | Security contacts and coordinated disclosure instructions |
| Disclosure policy | `/server/security/policy` | Vulnerability disclosure scope and policy |
| Researcher acknowledgements | `/server/security/thanks` | Published security acknowledgements |

Security report status links use `/server/security/report/{tracking_id}` and a
private status token. See [Security](security.md) for the encrypted report and
administrator review process. The security page shows alternate channels when
the private form is unavailable.

The server also serves the privacy-preference pages and actions under
`/server/preferences`, theme and consent actions under `/server/theme` and
`/server/consent`, and the conditional California privacy choice at
`/server/ccpa`. Public static and discovery resources include `/robots.txt`,
`/sitemap.xml`, `/offline.html`, `/manifest.json`, `/sw.js`, and the documented
well-known files; see [Integrations](integrations.md).

The privacy page describes the current instance's data handling, cookie
categories, sharing, retention, third-party services, and contact options.
Essential cookies are always on. Preferences and analytics cookies remain off
until the visitor accepts them; categories disabled by the operator are never
offered or enabled by a submitted form. The consent banner and privacy-page
controls use ordinary POST forms and work without JavaScript. When the operator
sets `server.privacy.data.sold` to `true`, the page adds the California opt-out
control and the consent and analytics copy reflects that setting. Built-in
privacy and terms text is localized; operator-authored page additions are
configured separately. See the [Privacy API](api.md#server-routes) for the
machine-readable privacy state.

The banner's Manage Preferences link opens a native dialog when JavaScript is
available. Without JavaScript, the same link opens the privacy page's inline
preferences form. The selected analytics provider is automatically listed in
the privacy disclosures alongside manually configured third-party services.

## Sign-in and account pages

The account sign-in and recovery pages are under `/server/auth/`: login,
registration, password recovery and reset, email verification, and invitation
acceptance. A signed-in user can manage their profile, password, TOTP, active
sessions, API tokens, and account deletion at `/users/account`. The `/users`
path opens the account home. Custom-domain management is available at
`/users/domains`.

Organization pages are rooted at `/orgs` and `/orgs/{slug}`. Depending on the
user's organization role, these pages support organization settings, members,
invitations, API tokens, and ownership transfer. Organization custom domains
are managed at `/orgs/{slug}/domains` by organization administrators.

## Tenant and service pages

Signed-in users and organization members can manage hosting resources through
the pages under `/users/` and `/orgs/{slug}/`. The enabled service sections
include sites, DNS, hosted mail, applications, usage, and managed databases.
Billing pages are under `/orgs/{slug}/billing`; customer support pages are
under `/support/`. Page availability depends on the feature and backend
configuration. See [API Reference](api.md) for the corresponding route
families and the running server's OpenAPI document for the enabled contract.

## Administration

The protected panel is served below `/server/{admin_path}`; the default path
is `/server/administration`. It includes setup and login, dashboard, server
settings, administrator accounts, logs and audit history, system information,
help, preferences, and security reports. Agent management appears when the
optional agent component is enabled. See the [Admin Panel](admin.md) guide for
access and operational details.

Configured analytics providers load only after the visitor accepts the
analytics cookie category. The server serves provider setup code from
`/server/tracking.js` with `Cache-Control: no-cache`, and does not render
provider scripts or serve that code for Tor or I2P requests. The consent
banner's fetch enhancement activates the same external scripts after the
server confirms consent, without reloading the page. The page CSP allows only
the configured provider sources when analytics is enabled; allowing a source
does not request it before consent.
