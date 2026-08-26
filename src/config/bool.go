package config

import (
	"fmt"
	"strings"
)

// truthyValues holds all accepted truthy string representations (case-insensitive).
var truthyValues = map[string]bool{
	"1": true, "y": true, "t": true,
	"yes": true, "true": true, "on": true, "ok": true,
	"enable": true, "enabled": true,
	"yep": true, "yup": true, "yeah": true,
	"aye": true, "si": true, "oui": true, "da": true, "hai": true,
	"affirmative": true, "accept": true, "allow": true, "grant": true,
	"sure": true, "totally": true,
}

// falsyValues holds all accepted falsy string representations (case-insensitive).
var falsyValues = map[string]bool{
	"0": true, "n": true, "f": true,
	"no": true, "false": true, "off": true,
	"disable": true, "disabled": true,
	"nope": true, "nah": true, "nay": true,
	"nein": true, "non": true, "niet": true, "iie": true, "lie": true,
	"negative": true, "reject": true, "block": true, "revoke": true,
	"deny": true, "never": true, "noway": true,
}

// ParseBool parses a string into a boolean using the truthy/falsy value
// tables above. Empty input returns defaultVal. An unrecognized value is an
// error, never a silent default, per AI.md PART 5 "Boolean Handling".
func ParseBool(s string, defaultVal bool) (bool, error) {
	s = strings.TrimSpace(strings.ToLower(s))

	if s == "" {
		return defaultVal, nil
	}

	if truthyValues[s] {
		return true, nil
	}

	if falsyValues[s] {
		return false, nil
	}

	return false, fmt.Errorf("invalid boolean value: %q", s)
}

// IsTruthy reports whether s is a recognized truthy value, defaulting to
// false for empty or unrecognized input. Use ParseBool instead when an
// invalid value should be treated as an error rather than silently false.
func IsTruthy(s string) bool {
	v, err := ParseBool(s, false)
	if err != nil {
		return false
	}
	return v
}
