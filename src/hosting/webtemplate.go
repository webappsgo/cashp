package hosting

import (
	"strings"
	"text/template"
)

// nginxUnsafe lists the characters that carry meaning in an nginx
// configuration. A value containing one of them is rejected at render time
// rather than escaped, because every value reaching a template has already
// passed an allowlist and must not contain them at all.
const nginxUnsafe = "\"'\\;{}$\n\r\t #"

// configFuncs are the render-time guards shared by every generated config.
// They re-validate what the service layer already validated, so a future
// caller cannot bypass validation by rendering a template directly.
var configFuncs = template.FuncMap{
	"host":  tmplHost,
	"qpath": tmplPath,
	"token": tmplToken,
	"join":  strings.Join,
}

// tmplHost re-validates a hostname before it becomes a server_name.
func tmplHost(v string) (string, error) {
	d, err := ValidateDomain(v)
	if err != nil {
		return "", err
	}
	return d, nil
}

// tmplPath quotes a filesystem path and refuses any nginx metacharacter.
func tmplPath(v string) (string, error) {
	if v == "" {
		return "", invalid("path", "must not be empty")
	}
	if strings.ContainsAny(v, nginxUnsafe) {
		return "", invalid("path", "contains an unsupported character")
	}
	return `"` + v + `"`, nil
}

// tmplToken passes through a bare identifier and refuses anything else.
func tmplToken(v string) (string, error) {
	if v == "" {
		return "", invalid("value", "must not be empty")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '-' || c == '_' || c == '/':
		default:
			return "", invalid("value", "contains an unsupported character")
		}
	}
	return v, nil
}

// vhostData is the render model for one nginx server block.
type vhostData struct {
	SiteID      string
	ServerNames []string
	DocRoot     string
	AccessLog   string
	ErrorLog    string
	PHP         bool
	PHPSocket   string
	TLS         bool
	CertPath    string
	KeyPath     string
	MaxBodyMB   int64
}

// vhostTemplate renders an nginx virtual host. Every interpolation runs
// through a guard function, so no tenant-supplied value can open a directive
// or a block even if it reached this point unvalidated.
var vhostTemplate = template.Must(template.New("vhost").Funcs(configFuncs).Parse(
	`# Managed by cashp for site {{ token .SiteID }} - manual edits are overwritten.
server {
	listen 80;
	listen [::]:80;
	server_name{{ range .ServerNames }} {{ host . }}{{ end }};
	root {{ qpath .DocRoot }};
	index index.html index.htm{{ if .PHP }} index.php{{ end }};
	access_log {{ qpath .AccessLog }};
	error_log {{ qpath .ErrorLog }};
	client_max_body_size {{ .MaxBodyMB }}m;
	server_tokens off;
	add_header X-Content-Type-Options "nosniff" always;
	add_header X-Frame-Options "SAMEORIGIN" always;
	add_header Referrer-Policy "strict-origin-when-cross-origin" always;

	location ^~ /.well-known/acme-challenge/ {
		root {{ qpath .DocRoot }};
		default_type "text/plain";
	}

	location ~ /\. {
		deny all;
	}
{{ if .TLS }}
	location / {
		return 301 https://$host$request_uri;
	}
}

server {
	listen 443 ssl;
	listen [::]:443 ssl;
	http2 on;
	server_name{{ range .ServerNames }} {{ host . }}{{ end }};
	root {{ qpath .DocRoot }};
	index index.html index.htm{{ if .PHP }} index.php{{ end }};
	access_log {{ qpath .AccessLog }};
	error_log {{ qpath .ErrorLog }};
	client_max_body_size {{ .MaxBodyMB }}m;
	server_tokens off;
	ssl_certificate {{ qpath .CertPath }};
	ssl_certificate_key {{ qpath .KeyPath }};
	ssl_protocols TLSv1.2 TLSv1.3;
	ssl_prefer_server_ciphers off;
	ssl_session_timeout 1d;
	ssl_session_tickets off;
	add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
	add_header X-Content-Type-Options "nosniff" always;
	add_header X-Frame-Options "SAMEORIGIN" always;
	add_header Referrer-Policy "strict-origin-when-cross-origin" always;

	location ~ /\. {
		deny all;
	}
{{ end }}
	location / {
		try_files $uri $uri/ =404;
	}
{{ if .PHP }}
	location ~ \.php$ {
		include fastcgi_params;
		fastcgi_pass unix:{{ token .PHPSocket }};
		fastcgi_index index.php;
		fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
		fastcgi_read_timeout 120;
	}
{{ end }}}
`))
