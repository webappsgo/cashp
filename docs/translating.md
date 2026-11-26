# Translation Guide

This guide describes the locale catalog workflow for translators and
reviewers. The catalogs are embedded in the Go package at
`src/common/i18n/locales/`.

## Current status

The repository contains seven locale catalogs: Arabic (`ar`), German (`de`),
English (`en`), Spanish (`es`), French (`fr`), Japanese (`ja`), and Chinese
(`zh`). Catalog lookup, language negotiation, formatting, and catalog
integrity tests exist. The server, administration pages, CLI, and agent are
not yet fully connected to this package, and the default server configuration
currently enables only English. Do not advertise the other catalogs as
available in the running application until that integration is complete.

No individual translator names are recorded in the current locale metadata.
The repository therefore credits zero named translators across seven locale
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
2. Translate every value and fill the locale metadata. Add the locale to the
   language registry and the server's supported-language configuration when
   that integration is enabled.
3. Keep the translation key set, interpolation tokens, and required plural
   categories identical to the English catalog.
4. Run `make test`; catalog tests check completeness, named tokens, plural
   rules, and that translations are not unchanged English copies.
5. Review the application at 320, 768, and 1280 pixels once locale rendering
   is wired through the pages. Check right-to-left layouts in their mirrored
   direction.
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
