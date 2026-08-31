package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/agent/transport"
	"github.com/webappsgo/cashp/src/security"
)

func TestScrubTokensMasksEveryPrefix(t *testing.T) {
	body := strings.Repeat("a", 32)
	for _, prefix := range []string{"adm", "usr", "org", "adm_agt", "usr_agt", "org_agt"} {
		token := prefix + "_" + body
		scrubbed := ScrubTokens("the panel rejected " + token + " on this node")

		if strings.Contains(scrubbed, body) {
			t.Errorf("token body survived scrubbing: %q", scrubbed)
		}
		if !strings.Contains(scrubbed, prefix+"_"+security.MaskedValue) {
			t.Errorf("scrubbed = %q, want the %s prefix kept", scrubbed, prefix)
		}
	}
}

func TestScrubTokensLeavesOrdinaryTextAlone(t *testing.T) {
	text := "could not reach https://panel.example.com: connection refused"
	if got := ScrubTokens(text); got != text {
		t.Fatalf("ScrubTokens rewrote ordinary text: %q", got)
	}
}

func TestFailMasksTokensAndMapsExitCodes(t *testing.T) {
	stderr := &bytes.Buffer{}
	token := "adm_agt_" + strings.Repeat("9", 32)

	if code := Fail(stderr, errors.New("token "+token+" refused")); code != ExitError {
		t.Fatalf("exit code = %d, want %d", code, ExitError)
	}
	if strings.Contains(stderr.String(), token) {
		t.Fatalf("Fail leaked a token: %q", stderr.String())
	}

	stderr.Reset()
	if code := Fail(stderr, transport.ErrUnauthorized); code != ExitAuth {
		t.Fatalf("exit code = %d, want %d", code, ExitAuth)
	}
}
