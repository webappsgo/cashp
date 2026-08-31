package main

import (
	"regexp"

	"github.com/webappsgo/cashp/src/security"
)

// tokenPattern matches the documented token format, {prefix}_{32
// alphanumeric}, including the *_agt_ agent prefixes. It is used to scrub
// anything the agent is about to print: a token that reaches a terminal or
// a log file has to be treated as compromised, so it never gets there.
var tokenPattern = regexp.MustCompile(`\b((?:adm|usr|org)(?:_agt)?)_[A-Za-z0-9]{32}\b`)

// ScrubTokens replaces every token in text with its prefix and the shared
// mask, keeping the prefix so the scope stays diagnosable.
func ScrubTokens(text string) string {
	return tokenPattern.ReplaceAllString(text, "${1}_"+security.MaskedValue)
}
