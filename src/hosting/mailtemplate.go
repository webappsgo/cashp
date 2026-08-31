package hosting

import (
	"strings"
	"text/template"
)

// mailFuncs guard every value written into a Postfix map, a Dovecot
// passwd-file, or an OpenDKIM table. A map file is line and whitespace
// delimited, so the guards refuse whitespace, colons, and control characters
// rather than trying to escape them.
var mailFuncs = template.FuncMap{
	"maddr":   tmplMailAddress,
	"mdomain": tmplHost,
	"mpath":   tmplMailPath,
	"mhash":   tmplPasswordHash,
	"mtoken":  tmplToken,
}

// tmplMailAddress re-validates a full mailbox or alias address.
func tmplMailAddress(v string) (string, error) {
	local, domain, ok := strings.Cut(v, "@")
	if !ok {
		return "", invalid("address", "must be local@domain")
	}
	if err := ValidateLocalPart(local); err != nil {
		return "", err
	}
	d, err := ValidateDomain(domain)
	if err != nil {
		return "", err
	}
	return strings.ToLower(local) + "@" + d, nil
}

// tmplMailPath renders an unquoted path for a map file. Postfix and Dovecot
// split their map lines on whitespace, so a path carrying whitespace, a colon,
// or a line break is refused outright.
func tmplMailPath(v string) (string, error) {
	if v == "" {
		return "", invalid("path", "must not be empty")
	}
	if strings.ContainsAny(v, " \t\n\r:\"'\\") {
		return "", invalid("path", "contains an unsupported character")
	}
	return v, nil
}

// tmplPasswordHash accepts only the characters of an Argon2id PHC string, so a
// forged hash field cannot add a colon and take over the following field.
func tmplPasswordHash(v string) (string, error) {
	if !strings.HasPrefix(v, "$argon2id$") {
		return "", invalid("password", "is not stored in the expected format")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '$' || c == '+' || c == '/' || c == '=' || c == ',' || c == '.' || c == '-' || c == '_':
		default:
			return "", invalid("password", "is not stored in the expected format")
		}
	}
	return v, nil
}

// mailDomainEntry is one virtual domain line.
type mailDomainEntry struct {
	Domain string
}

// mailboxEntry is one virtual mailbox, Dovecot user, and quota line.
type mailboxEntry struct {
	Address string
	Domain  string
	Local   string
	Hash    string
	Home    string
	QuotaMB int64
}

// aliasEntry is one virtual alias line.
type aliasEntry struct {
	Source      string
	Destination string
}

// dkimEntry is one OpenDKIM key- and signing-table line.
type dkimEntry struct {
	Domain   string
	Selector string
	KeyPath  string
}

// mailData is the render model shared by every generated mail map.
type mailData struct {
	Domains   []mailDomainEntry
	Mailboxes []mailboxEntry
	Aliases   []aliasEntry
	DKIM      []dkimEntry
	Hostname  string
}

// virtualDomainsTemplate lists the domains Postfix accepts mail for. The maps
// are read with the texthash: type, so no compiled database is ever built.
var virtualDomainsTemplate = template.Must(template.New("virtual_domains").Funcs(mailFuncs).Parse(
	`# Managed by cashp - reference as texthash:{file} in main.cf.
{{ range .Domains }}{{ mdomain .Domain }}	OK
{{ end }}`))

// virtualMailboxTemplate maps an address to its Maildir below the mail root.
var virtualMailboxTemplate = template.Must(template.New("virtual_mailboxes").Funcs(mailFuncs).Parse(
	`# Managed by cashp - reference as texthash:{file} in main.cf.
{{ range .Mailboxes }}{{ maddr .Address }}	{{ mdomain .Domain }}/{{ mtoken .Local }}/Maildir/
{{ end }}`))

// virtualAliasTemplate maps an alias address to its destination.
var virtualAliasTemplate = template.Must(template.New("virtual_aliases").Funcs(mailFuncs).Parse(
	`# Managed by cashp - reference as texthash:{file} in main.cf.
{{ range .Aliases }}{{ maddr .Source }}	{{ maddr .Destination }}
{{ end }}`))

// dovecotUsersTemplate renders a Dovecot passwd-file. The password field holds
// the Argon2id PHC string produced by src/security; no plaintext or reversible
// form of a mailbox password is ever written.
var dovecotUsersTemplate = template.Must(template.New("dovecot_users").Funcs(mailFuncs).Parse(
	`# Managed by cashp - reference as passdb/userdb passwd-file in dovecot.
{{ range .Mailboxes }}{{ maddr .Address }}:{ARGON2ID}{{ mhash .Hash }}::::{{ mpath .Home }}::userdb_quota_rule=*:storage={{ .QuotaMB }}M
{{ end }}`))

// dkimKeyTableTemplate maps a signing selector to its private key file.
var dkimKeyTableTemplate = template.Must(template.New("dkim_keytable").Funcs(mailFuncs).Parse(
	`# Managed by cashp - OpenDKIM KeyTable.
{{ range .DKIM }}{{ mtoken .Selector }}._domainkey.{{ mdomain .Domain }} {{ mdomain .Domain }}:{{ mtoken .Selector }}:{{ mpath .KeyPath }}
{{ end }}`))

// dkimSigningTableTemplate maps every sender of a domain to its selector.
var dkimSigningTableTemplate = template.Must(template.New("dkim_signingtable").Funcs(mailFuncs).Parse(
	`# Managed by cashp - OpenDKIM SigningTable.
{{ range .DKIM }}*@{{ mdomain .Domain }} {{ mtoken .Selector }}._domainkey.{{ mdomain .Domain }}
{{ end }}`))

// dkimTrustedHostsTemplate lists the hosts OpenDKIM signs mail from.
var dkimTrustedHostsTemplate = template.Must(template.New("dkim_trusted").Funcs(mailFuncs).Parse(
	`# Managed by cashp - OpenDKIM TrustedHosts.
127.0.0.1
::1
localhost
{{ if .Hostname }}{{ mdomain .Hostname }}
{{ end }}{{ range .Domains }}{{ mdomain .Domain }}
{{ end }}`))
