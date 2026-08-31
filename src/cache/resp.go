package cache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// RESP protocol type bytes (RESP2), shared by Valkey and Redis.
const (
	typeStatus = '+'
	typeError  = '-'
	typeInt    = ':'
	typeBulk   = '$'
	typeArray  = '*'
)

// Protocol safety limits. They bound what a compromised or misbehaving
// server can make this client allocate.
const (
	maxBulkSize  = 64 << 20
	maxArrayLen  = 1 << 20
	maxNestDepth = 8
)

// errProtocol reports a reply this client cannot parse. The connection is
// unusable afterwards and is always discarded.
var errProtocol = errors.New("cache: malformed RESP reply")

// errEmptyCommand reports a command with no arguments, which is a bug in
// this package rather than a protocol condition.
var errEmptyCommand = errors.New("cache: empty command")

// errAuthFailed reports a rejected AUTH. The server's message is
// deliberately dropped so no credential detail can reach a log.
var errAuthFailed = errors.New("cache: authentication rejected by server")

// ServerError is an error reply sent by the server. The connection stays
// usable, so the command failed but the transport did not.
type ServerError struct {
	Msg string
}

// Error renders the server's error reply.
func (e *ServerError) Error() string {
	return "cache: server error: " + e.Msg
}

// reply is a decoded RESP value.
type reply struct {
	typ  byte
	str  []byte
	num  int64
	arr  []reply
	null bool
}

// respConn is a single client connection to a Valkey/Redis server. It is
// not safe for concurrent use; the pool hands each command its own conn.
type respConn struct {
	conn net.Conn
	r    *bufio.Reader
	buf  []byte
}

// dialRESP opens an authenticated connection and selects the configured
// database.
func dialRESP(ctx context.Context, opts Options) (*respConn, error) {
	dialer := net.Dialer{Timeout: opts.DialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", opts.Addr)
	if err != nil {
		return nil, err
	}

	rc := &respConn{conn: conn, r: bufio.NewReader(conn)}

	if opts.Password != "" {
		args := [][]byte{[]byte("AUTH")}
		if opts.Username != "" {
			args = append(args, []byte(opts.Username))
		}
		args = append(args, []byte(opts.Password))
		if _, err := rc.do(ctx, opts.Timeout, args...); err != nil {
			rc.close()
			var serverErr *ServerError
			if errors.As(err, &serverErr) {
				return nil, errAuthFailed
			}
			return nil, err
		}
	}

	if opts.DB > 0 {
		if _, err := rc.do(ctx, opts.Timeout, cmd("SELECT", strconv.Itoa(opts.DB))...); err != nil {
			rc.close()
			return nil, err
		}
	}

	return rc, nil
}

// do writes one command and reads its reply, bounded by the smaller of
// timeout and the context deadline.
func (rc *respConn) do(ctx context.Context, timeout time.Duration, args ...[]byte) (reply, error) {
	if len(args) == 0 {
		return reply{}, errEmptyCommand
	}
	if err := ctx.Err(); err != nil {
		return reply{}, err
	}

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := rc.conn.SetDeadline(deadline); err != nil {
		return reply{}, err
	}

	rc.buf = encodeCommand(rc.buf[:0], args)
	if _, err := rc.conn.Write(rc.buf); err != nil {
		return reply{}, err
	}

	rp, err := readReply(rc.r, 0)
	if err != nil {
		return reply{}, err
	}
	if rp.typ == typeError {
		return reply{}, &ServerError{Msg: string(rp.str)}
	}
	return rp, nil
}

// close shuts the connection down, ignoring the close error because the
// connection is being discarded either way.
func (rc *respConn) close() {
	if rc == nil || rc.conn == nil {
		return
	}
	_ = rc.conn.Close()
}

// cmd builds a command's argument list from strings and byte slices.
func cmd(parts ...any) [][]byte {
	args := make([][]byte, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			args = append(args, []byte(v))
		case []byte:
			args = append(args, v)
		default:
			args = append(args, []byte(fmt.Sprintf("%v", v)))
		}
	}
	return args
}

// encodeCommand appends a RESP array of bulk strings to buf.
func encodeCommand(buf []byte, args [][]byte) []byte {
	buf = append(buf, typeArray)
	buf = strconv.AppendInt(buf, int64(len(args)), 10)
	buf = append(buf, '\r', '\n')
	for _, a := range args {
		buf = append(buf, typeBulk)
		buf = strconv.AppendInt(buf, int64(len(a)), 10)
		buf = append(buf, '\r', '\n')
		buf = append(buf, a...)
		buf = append(buf, '\r', '\n')
	}
	return buf
}

// readReply decodes one RESP value, refusing replies nested deeper than
// maxNestDepth so a hostile server cannot drive unbounded recursion.
func readReply(r *bufio.Reader, depth int) (reply, error) {
	if depth > maxNestDepth {
		return reply{}, errProtocol
	}

	typ, err := r.ReadByte()
	if err != nil {
		return reply{}, err
	}
	line, err := readLine(r)
	if err != nil {
		return reply{}, err
	}

	switch typ {
	case typeStatus, typeError:
		return reply{typ: typ, str: line}, nil

	case typeInt:
		n, err := strconv.ParseInt(string(line), 10, 64)
		if err != nil {
			return reply{}, errProtocol
		}
		return reply{typ: typ, num: n}, nil

	case typeBulk:
		n, err := strconv.ParseInt(string(line), 10, 64)
		if err != nil {
			return reply{}, errProtocol
		}
		if n < 0 {
			return reply{typ: typ, null: true}, nil
		}
		if n > maxBulkSize {
			return reply{}, errProtocol
		}
		body := make([]byte, n+2)
		if _, err := io.ReadFull(r, body); err != nil {
			return reply{}, err
		}
		if body[n] != '\r' || body[n+1] != '\n' {
			return reply{}, errProtocol
		}
		return reply{typ: typ, str: body[:n]}, nil

	case typeArray:
		n, err := strconv.ParseInt(string(line), 10, 64)
		if err != nil {
			return reply{}, errProtocol
		}
		if n < 0 {
			return reply{typ: typ, null: true}, nil
		}
		if n > maxArrayLen {
			return reply{}, errProtocol
		}
		items := make([]reply, 0, n)
		for i := int64(0); i < n; i++ {
			item, err := readReply(r, depth+1)
			if err != nil {
				return reply{}, err
			}
			items = append(items, item)
		}
		return reply{typ: typ, arr: items}, nil

	default:
		return reply{}, errProtocol
	}
}

// readLine reads one CRLF-terminated protocol line without its
// terminator.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, errProtocol
		}
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, errProtocol
	}
	out := make([]byte, len(line)-2)
	copy(out, line[:len(line)-2])
	return out, nil
}
