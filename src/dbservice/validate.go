package dbservice

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Identifier allowlists. Nothing that reaches an engine, a container name or
// a command line is accepted unless it matches one of these patterns
// completely. The patterns admit only ASCII letters, digits, underscore and
// (for instance names) a single-hyphen form, which excludes every shell
// metacharacter, quote, whitespace character, NUL and "..".
var (
	// identifierPattern governs database names and usernames inside an
	// instance. It must start with a letter or underscore so it is a legal
	// unquoted identifier on every engine cashp manages.
	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	// instanceNamePattern governs the tenant-visible instance name, which
	// also becomes part of a container and volume name.
	instanceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)
	// tenantIDPattern governs the tenant identifier used for scoping and for
	// per-tenant network and label names.
	tenantIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// versionPattern governs an engine version selector before it is checked
	// against the engine's offered version list.
	versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}$`)
)

// maxIdentifierLen is the shortest identifier ceiling across the managed
// engines, applied uniformly so a name accepted for one engine is never
// truncated by another.
const maxIdentifierLen = 63

// reservedIdentifiers are names a tenant may never create or drop, per
// engine. Handing these to a tenant would let them damage the instance's own
// bookkeeping or shadow a system catalogue.
var reservedIdentifiers = map[Engine]map[string]bool{
	EnginePostgres: {
		"postgres": true, "template0": true, "template1": true,
		"pg_catalog": true, "information_schema": true, "pg_toast": true,
	},
	EngineMariaDB: {
		"mysql": true, "information_schema": true, "performance_schema": true,
		"sys": true, "root": true,
	},
	EngineMongoDB: {
		"admin": true, "local": true, "config": true,
	},
	EngineValkey: {
		"default": true,
	},
}

// ValidateTenantID checks a tenant identifier before it is used for scoping,
// labelling or naming. An empty or malformed tenant id is rejected rather
// than defaulted, so no query can ever run without a real tenant scope.
func ValidateTenantID(id string) error {
	if id == "" {
		return ErrValidation("A tenant is required.")
	}
	if !tenantIDPattern.MatchString(id) {
		return ErrValidation("That tenant identifier is not valid.")
	}
	return nil
}

// ValidateInstanceName checks a tenant-chosen instance name.
func ValidateInstanceName(name string) error {
	if name == "" {
		return ErrValidation("An instance name is required.")
	}
	if strings.Contains(name, "--") {
		return ErrValidation("An instance name may not contain two hyphens in a row.")
	}
	if !instanceNamePattern.MatchString(name) {
		return ErrValidation("An instance name may use 3 to 32 lowercase letters, digits and hyphens, and must start and end with a letter or digit.")
	}
	return nil
}

// ValidateIdentifier checks a database name or username for the given engine.
// It enforces the allowlist pattern, the length ceiling and the engine's
// reserved-name list. It is the only gate an identifier passes through before
// it may appear in DDL, in an argv slice or in a connection string.
func ValidateIdentifier(engine Engine, kind, name string) error {
	if name == "" {
		return ErrValidation(fmt.Sprintf("A %s is required.", kind))
	}
	if len(name) > maxIdentifierLen {
		return ErrValidation(fmt.Sprintf("A %s may be at most %d characters.", kind, maxIdentifierLen))
	}
	if !identifierPattern.MatchString(name) {
		return ErrValidation(fmt.Sprintf("A %s may use only letters, digits and underscores, and must start with a letter or underscore.", kind))
	}
	if reservedIdentifiers[engine][strings.ToLower(name)] {
		return ErrValidation(fmt.Sprintf("%q is reserved by %s and cannot be used.", name, EngineDisplayName(engine)))
	}
	return nil
}

// ValidateVersion checks a version selector's shape. Membership in the
// engine's offered list is checked separately by the engine registry, so a
// malformed string is rejected before it is compared to anything.
func ValidateVersion(version string) error {
	if version == "" {
		return ErrValidation("An engine version is required.")
	}
	if len(version) > 16 || !versionPattern.MatchString(version) {
		return ErrValidation("That engine version is not valid.")
	}
	return nil
}

// QuoteIdentifier renders an already-validated identifier using the engine's
// own identifier-quoting rules. Validation is repeated here rather than
// assumed, because an unvalidated identifier must never reach DDL even if a
// future call site forgets the earlier check.
func QuoteIdentifier(engine Engine, name string) (string, error) {
	if err := ValidateIdentifier(engine, "name", name); err != nil {
		return "", err
	}
	switch engine {
	case EnginePostgres:
		// PostgreSQL quotes with double quotes and escapes an embedded quote
		// by doubling it. The allowlist admits none, so this only hardens the
		// rendering.
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
	case EngineMariaDB:
		// MariaDB quotes with backticks and doubles an embedded backtick.
		return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
	case EngineMongoDB:
		// MongoDB has no DDL: names are JSON string values inside a script,
		// so JSON encoding is the correct quoting rule.
		encoded, err := json.Marshal(name)
		if err != nil {
			return "", ErrInternal(err, "That name could not be processed.")
		}
		return string(encoded), nil
	case EngineValkey:
		// Valkey takes usernames as plain argv words to ACL SETUSER; there is
		// no quoting layer and no statement text to escape into.
		return name, nil
	default:
		return "", ErrUnknownEngine(string(engine))
	}
}

// jsonString encodes a validated value as a JSON string literal for
// embedding in a MongoDB script. It is used for values rather than
// identifiers, which QuoteIdentifier handles.
func jsonString(value string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", ErrInternal(err, "That value could not be processed.")
	}
	return string(encoded), nil
}
