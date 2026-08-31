package cache

import (
	"bufio"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEncodeCommand(t *testing.T) {
	got := string(encodeCommand(nil, cmd("SET", "user:1", []byte("alice"))))
	want := "*3\r\n$3\r\nSET\r\n$6\r\nuser:1\r\n$5\r\nalice\r\n"
	if got != want {
		t.Fatalf("encoded = %q, want %q", got, want)
	}
}

func TestEncodeCommandBinarySafe(t *testing.T) {
	value := []byte{0x00, '\r', '\n', 0xff}
	got := string(encodeCommand(nil, cmd("SET", "k", value)))
	want := "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$4\r\n" + string(value) + "\r\n"
	if got != want {
		t.Fatalf("encoded = %q, want %q", got, want)
	}
}

func TestCmdConvertsArgumentTypes(t *testing.T) {
	args := cmd("SET", []byte("k"), 42)
	if len(args) != 3 {
		t.Fatalf("len = %d", len(args))
	}
	if string(args[2]) != "42" {
		t.Fatalf("args[2] = %q", args[2])
	}
}

func readReplyFrom(t *testing.T, wire string) (reply, error) {
	t.Helper()
	return readReply(bufio.NewReader(strings.NewReader(wire)), 0)
}

func TestReadReplyTypes(t *testing.T) {
	rp, err := readReplyFrom(t, "+OK\r\n")
	if err != nil || rp.typ != typeStatus || string(rp.str) != "OK" {
		t.Fatalf("status: %+v %v", rp, err)
	}

	rp, err = readReplyFrom(t, ":7\r\n")
	if err != nil || rp.num != 7 {
		t.Fatalf("int: %+v %v", rp, err)
	}

	rp, err = readReplyFrom(t, "$5\r\nalice\r\n")
	if err != nil || string(rp.str) != "alice" || rp.null {
		t.Fatalf("bulk: %+v %v", rp, err)
	}

	rp, err = readReplyFrom(t, "$-1\r\n")
	if err != nil || !rp.null {
		t.Fatalf("null bulk: %+v %v", rp, err)
	}

	rp, err = readReplyFrom(t, "$0\r\n\r\n")
	if err != nil || rp.null || len(rp.str) != 0 {
		t.Fatalf("empty bulk: %+v %v", rp, err)
	}

	rp, err = readReplyFrom(t, "*2\r\n$1\r\n0\r\n*1\r\n$4\r\nkey1\r\n")
	if err != nil {
		t.Fatalf("array: %v", err)
	}
	if len(rp.arr) != 2 || string(rp.arr[0].str) != "0" || string(rp.arr[1].arr[0].str) != "key1" {
		t.Fatalf("array: %+v", rp)
	}

	rp, err = readReplyFrom(t, "*-1\r\n")
	if err != nil || !rp.null {
		t.Fatalf("null array: %+v %v", rp, err)
	}
}

func TestReadReplyError(t *testing.T) {
	rp, err := readReplyFrom(t, "-ERR unknown command\r\n")
	if err != nil {
		t.Fatalf("error reply must decode, not fail: %v", err)
	}
	if rp.typ != typeError || string(rp.str) != "ERR unknown command" {
		t.Fatalf("rp = %+v", rp)
	}

	serverErr := &ServerError{Msg: "ERR nope"}
	if !strings.Contains(serverErr.Error(), "ERR nope") {
		t.Fatalf("ServerError.Error() = %q", serverErr.Error())
	}
}

func TestReadReplyRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"unknown type":    "!boom\r\n",
		"missing cr":      "+OK\n",
		"bad int":         ":abc\r\n",
		"bad bulk length": "$abc\r\n",
		"bad array count": "*abc\r\n",
		"oversized bulk":  "$99999999999\r\n",
		"oversized array": "*99999999999\r\n",
	}
	for name, wire := range cases {
		if _, err := readReplyFrom(t, wire); !errors.Is(err, errProtocol) {
			t.Errorf("%s: err = %v, want errProtocol", name, err)
		}
	}
}

func TestReadReplyRejectsBadBulkTerminator(t *testing.T) {
	if _, err := readReplyFrom(t, "$5\r\naliceXX"); !errors.Is(err, errProtocol) {
		t.Fatalf("err = %v, want errProtocol", err)
	}
}

func TestReadReplyRejectsDeepNesting(t *testing.T) {
	wire := strings.Repeat("*1\r\n", maxNestDepth+2) + ":1\r\n"
	if _, err := readReplyFrom(t, wire); !errors.Is(err, errProtocol) {
		t.Fatalf("err = %v, want errProtocol", err)
	}
}

func TestReadReplyTruncatedInput(t *testing.T) {
	if _, err := readReplyFrom(t, ""); err == nil {
		t.Fatal("empty input must fail")
	}
	if _, err := readReplyFrom(t, "$5\r\nali"); err == nil {
		t.Fatal("truncated bulk must fail")
	}
}

func TestGlobEscape(t *testing.T) {
	cases := map[string]string{
		"user:1:":     "user:1:",
		"user:a*b":    `user:a\*b`,
		"user:a?b":    `user:a\?b`,
		"user:[a]":    `user:\[a\]`,
		`user:a\b`:    `user:a\\b`,
		"user:^start": `user:\^start`,
	}
	for in, want := range cases {
		if got := globEscape(in); got != want {
			t.Errorf("globEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTTLMillis(t *testing.T) {
	if got := ttlMillis(0); got != 0 {
		t.Errorf("zero ttl = %d, want 0", got)
	}
	if got := ttlMillis(-time.Second); got != 0 {
		t.Errorf("negative ttl = %d, want 0", got)
	}
	if got := ttlMillis(time.Nanosecond); got != 1 {
		t.Errorf("sub-millisecond ttl = %d, want 1", got)
	}
	if got := ttlMillis(2 * time.Second); got != 2000 {
		t.Errorf("2s ttl = %d, want 2000", got)
	}
}
