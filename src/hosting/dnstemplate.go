package hosting

import (
	"strings"
	"text/template"
)

// zoneFuncs guard every value written into a BIND zone file or into the
// generated named include. They repeat the service-layer validation so a
// record can never open a comment, close a parenthesis group, or start a new
// line of its own.
var zoneFuncs = template.FuncMap{
	"zname": tmplZoneName,
	"zhost": tmplZoneHost,
	"ztype": tmplRecordType,
	"zdata": tmplRData,
	"qpath": tmplPath,
}

// tmplZoneName renders a record owner name.
func tmplZoneName(v string) (string, error) {
	name, err := ValidateRecordName(v)
	if err != nil {
		return "", err
	}
	return name, nil
}

// tmplZoneHost renders a hostname as a fully qualified name with the trailing
// dot BIND requires, so it can never be completed with the zone origin.
func tmplZoneHost(v string) (string, error) {
	if strings.HasSuffix(v, ".") && strings.Count(v, ".") > 1 {
		trimmed := strings.TrimSuffix(v, ".")
		if _, err := ValidateDomain(trimmed); err != nil {
			return "", err
		}
		return trimmed + ".", nil
	}
	d, err := ValidateDomain(v)
	if err != nil {
		return "", err
	}
	return d + ".", nil
}

// tmplRecordType renders a record type from the allowlist.
func tmplRecordType(v string) (string, error) {
	upper := strings.ToUpper(v)
	if err := ValidateRecordType(upper); err != nil {
		return "", err
	}
	return upper, nil
}

// tmplRData is the last gate before record data reaches a zone file. The data
// halves are built by rdataFor from already-validated fields; this refuses
// anything carrying a line break or a control character regardless.
func tmplRData(v string) (string, error) {
	if v == "" {
		return "", invalid("record value", "must not be empty")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\n' || c == '\r' || c < 0x20 || c == 0x7f {
			return "", invalid("record value", "contains an unsupported character")
		}
	}
	return v, nil
}

// zoneRecord is one rendered resource record.
type zoneRecord struct {
	Name  string
	TTL   int64
	Type  string
	RData string
}

// zoneData is the render model of a full zone file.
type zoneData struct {
	Origin     string
	DefaultTTL int64
	PrimaryNS  string
	Mailbox    string
	Serial     int64
	Refresh    int64
	Retry      int64
	Expire     int64
	Minimum    int64
	Records    []zoneRecord
}

// zoneTemplate renders a BIND master zone file.
var zoneTemplate = template.Must(template.New("zone").Funcs(zoneFuncs).Parse(
	`; Managed by cashp - manual edits are overwritten.
$ORIGIN {{ zhost .Origin }}
$TTL {{ .DefaultTTL }}
@	IN	SOA	{{ zhost .PrimaryNS }} {{ zdata .Mailbox }} (
	{{ .Serial }}	; serial
	{{ .Refresh }}	; refresh
	{{ .Retry }}	; retry
	{{ .Expire }}	; expire
	{{ .Minimum }}	; minimum
	)
{{ range .Records }}{{ zname .Name }}	{{ .TTL }}	IN	{{ ztype .Type }}	{{ zdata .RData }}
{{ end }}`))

// namedZone is one zone entry of the generated named include.
type namedZone struct {
	Name   string
	File   string
	DNSSEC bool
	KeyDir string
	Policy string
}

// namedData is the render model of the generated named include.
type namedData struct {
	Zones []namedZone
}

// namedTemplate renders the include file the host's named.conf pulls in. DNSSEC
// is delegated to BIND's own dnssec-policy so cashp never handles key material.
var namedTemplate = template.Must(template.New("named").Funcs(zoneFuncs).Parse(
	`# Managed by cashp - manual edits are overwritten.
{{ range .Zones }}zone "{{ zhost .Name }}" {
	type master;
	file {{ qpath .File }};
	allow-transfer { none; };
	allow-update { none; };
{{ if .DNSSEC }}	dnssec-policy {{ .Policy }};
	key-directory {{ qpath .KeyDir }};
	inline-signing yes;
{{ end }}};
{{ end }}`))
