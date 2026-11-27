# Translation Guide

This guide describes the locale catalog workflow for translators and
reviewers. The catalogs are embedded in the Go package at
`src/common/i18n/locales/`.

## Current status

The package embeds eight locale catalogs: Arabic (`ar`), German (`de`), English
(`en`), Spanish (`es`), French (`fr`), Italian (`it`), Japanese (`ja`), and
Chinese (`zh`). English, Spanish, French, Arabic, German, and Italian are the
required minimum; Japanese and Chinese are additional complete catalogs. A
single registry defines each locale's code, English name, native name,
direction, and required CLDR plural categories. Loading the embedded catalogs
checks that the registry and locale files have the same languages and that
catalog metadata and plural forms match the registry. The runtime default
language list and language selector are derived from that registry.

The package provides catalog lookup and fallback, request language
negotiation, formatting, plural handling, accessibility helpers, and template
functions. The HTTP server resolves `?lang=`, the language cookie, and
`Accept-Language`, and public pages emit the selected `lang` and `dir` metadata.
Application translation coverage is still incomplete: many web templates,
including auth, admin, support, billing, hosting, database, and orchestrator
pages, contain English literals. The CLI and agent also have human-readable
output that is not wired to these catalogs. The locale foundation does not
make those application strings translated.

No individual translator names are recorded in the current locale metadata.
The repository therefore credits zero named translators across eight locale
files; names must not be guessed or assigned to an automated system. When a
human translator chooses to be credited, add their name or handle to that
locale's `meta.translators` and update this page.

## Editing a catalog

English (`en.json`) is the source catalog. A locale file is UTF-8 JSON with
the same nested key structure and key order. Keep keys unchanged and translate
values only. Preserve the metadata fields `language`, `name`, `native_name`,
`direction`, and `version`; `direction` is `ltr` or `rtl`.

Keep every named interpolation token exactly as written, including braces.
For example, `{count}`, `{total}`, and `{datetime}` must remain unchanged,
although their position in the translated sentence may move. Translate whole
sentences rather than joining fragments. For plural messages, provide every
category required by the locale's CLDR rule, including `other`.

Use one consistent register and a neutral tone. Keep product names, protocol
names, code, file names, configuration keys, URLs, and literal values such as
`dark` and `auto` unchanged. Follow each language's punctuation, spacing,
quotation, and numeral conventions.

## Adding a language

1. Copy `src/common/i18n/locales/en.json` to a lowercase BCP 47 filename such
   as `pt.json` or `pt-br.json`.
2. Translate every value and fill the locale metadata. Add one entry to
   `src/common/i18n/registry.go` with the language code, English name, native
   name, direction, and required CLDR plural categories. Keep the registry,
   embedded catalog files, and `server.i18n.available_languages` defaults in
   sync; runtime loading rejects differences. Runtime UI integration also needs
   to pass the bundle's functions to the relevant templates and resolve a locale
   for each request or command.
3. Keep the translation key set, interpolation tokens, and required plural
   categories identical to the English catalog.
4. Run `make test`; catalog tests check language registry parity, message-key
   parity, named tokens, plural rules, and that translations are not unchanged
   English copies. A source scanner also checks static translation calls in Go,
   Rust, JavaScript, HTML, and Go templates against `en.json`.
5. Review the translated UI at 320, 768, and 1280 pixels after locale
   rendering is wired through the pages. Check right-to-left layouts in their
   mirrored direction.
6. Update the locale count and translator credits in this file and the README
   language list when that list is added.

## Review and credits

Reviewers should verify meaning, placeholders, plural categories, glossary
terms, and layout. A native speaker review is welcome. Human corrections take
precedence over earlier translations; no translator should be credited
without their consent. Add a contributor's chosen name or handle to
`meta.translators` and this page when they request credit.

## Glossary

| English term | Translation guidance |
|---|---|
| account | Use the term for a user account; do not confuse it with an organization. |
| API token | Keep the technical meaning distinct from a session cookie. |
| paste | Use the action of inserting copied text, not adhesive. |
| rotate | Use the security meaning of replacing a key or credential. |
| revoke | Use the action of invalidating a credential or permission. |
| tenant | Use the software-isolation meaning; do not translate as a physical tenant. |

Add project-specific recurring terms here before translating new interface
areas, and reuse the agreed term in every catalog.
