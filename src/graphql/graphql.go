package graphql

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/webappsgo/cashp/src/api"
)

// maxDocumentBytes caps the size of an accepted GraphQL document. A query is
// a small text; anything larger is a resource-exhaustion attempt.
const maxDocumentBytes = 64 << 10

// operationKind is the kind of a parsed operation.
type operationKind string

const (
	// opQuery reads data.
	opQuery operationKind = "query"
	// opMutation changes data.
	opMutation operationKind = "mutation"
)

// operation is one parsed GraphQL operation.
type operation struct {
	kind       operationKind
	name       string
	selections []selection
}

// selection is one requested field with its arguments and sub-selection.
type selection struct {
	name  string
	alias string
	args  map[string]any
	subs  []selection
}

// key returns the response key of a selection, which is its alias when one
// was given.
func (s selection) key() string {
	if s.alias != "" {
		return s.alias
	}
	return s.name
}

// request is the standard GraphQL-over-HTTP request body.
type request struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
	OperationName string         `json:"operationName"`
}

// gqlError is one entry of the errors array of a GraphQL response.
type gqlError struct {
	Message string         `json:"message"`
	Path    []string       `json:"path,omitempty"`
	Extra   map[string]any `json:"extensions,omitempty"`
}

// Handler serves the GraphQL endpoint. It resolves every field by invoking
// the same handler the equivalent REST route uses, so the two surfaces cannot
// diverge (AI.md PART 14).
type Handler struct {
	provider   RouteProvider
	dispatcher http.Handler
	debug      bool
}

// NewHandler builds a GraphQL handler over a route provider.
//
// The dispatcher must be the server's complete middleware chain, because every
// field is executed by replaying a request through it. Resolving a route
// handler directly would skip authentication and rate limiting and turn the
// GraphQL endpoint into a way around them. When no dispatcher is supplied,
// fields whose route requires authentication refuse to execute.
func NewHandler(provider RouteProvider, dispatcher http.Handler, debug bool) *Handler {
	return &Handler{provider: provider, dispatcher: dispatcher, debug: debug}
}

// SDL returns the current schema document.
func (h *Handler) SDL() string {
	return SDL(h.provider())
}

// ServeHTTP executes a GraphQL request. GraphQL reports its own failures
// inside a 200 response body, so the transport status stays 200 for anything
// the parser or an executed field rejects; only a malformed transport request
// produces a non-200 status.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRequest(r)
	if err != nil {
		writeErrors(w, http.StatusBadRequest, gqlError{Message: err.Error()})
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeErrors(w, http.StatusBadRequest, gqlError{Message: "the request carries no query"})
		return
	}

	ops, err := parse(req.Query, req.Variables)
	if err != nil {
		writeErrors(w, http.StatusOK, gqlError{Message: err.Error()})
		return
	}
	op, err := selectOperation(ops, req.OperationName)
	if err != nil {
		writeErrors(w, http.StatusOK, gqlError{Message: err.Error()})
		return
	}
	if op.kind == opMutation && r.Method != http.MethodPost {
		writeErrors(w, http.StatusMethodNotAllowed, gqlError{Message: "a mutation must be sent with POST"})
		return
	}

	data, errs := h.execute(r, op)
	body := map[string]any{"data": data}
	if len(errs) > 0 {
		body["errors"] = errs
	}
	api.WriteJSON(w, http.StatusOK, body)
}

// selectOperation picks the operation to run, honouring operationName.
func selectOperation(ops []operation, name string) (operation, error) {
	if name == "" {
		if len(ops) > 1 {
			return operation{}, errors.New("the document declares several operations, so operationName is required")
		}
		return ops[0], nil
	}
	for _, op := range ops {
		if op.name == name {
			return op, nil
		}
	}
	return operation{}, fmt.Errorf("the document declares no operation named %q", name)
}

// decodeRequest reads a GraphQL request from any of the transports the
// specification allows: a JSON body, a raw application/graphql body, a form
// body, or query parameters on a GET.
func decodeRequest(r *http.Request) (request, error) {
	var req request
	if r.Method == http.MethodGet {
		req.Query = r.URL.Query().Get("query")
		req.OperationName = r.URL.Query().Get("operationName")
		if raw := r.URL.Query().Get("variables"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &req.Variables); err != nil {
				return req, errors.New("the variables parameter is not valid JSON")
			}
		}
		return req, nil
	}

	contentType := r.Header.Get("Content-Type")
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	contentType = strings.TrimSpace(strings.ToLower(contentType))

	if contentType == "application/x-www-form-urlencoded" || contentType == "multipart/form-data" {
		if err := r.ParseForm(); err != nil {
			return req, errors.New("the form body could not be read")
		}
		req.Query = r.FormValue("query")
		req.OperationName = r.FormValue("operationName")
		if raw := r.FormValue("variables"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &req.Variables); err != nil {
				return req, errors.New("the variables field is not valid JSON")
			}
		}
		return req, nil
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxDocumentBytes+1))
	if err != nil {
		return req, errors.New("the request body could not be read")
	}
	if len(body) > maxDocumentBytes {
		return req, errors.New("the request body is larger than the accepted limit")
	}
	if contentType == "application/graphql" {
		req.Query = string(body)
		return req, nil
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, errors.New("the request body is not a valid GraphQL request document")
	}
	return req, nil
}

// writeErrors emits a GraphQL error response with a null data field.
func writeErrors(w http.ResponseWriter, status int, errs ...gqlError) {
	api.WriteJSON(w, status, map[string]any{"data": nil, "errors": errs})
}

// token is one lexical unit of a GraphQL document.
type token struct {
	kind  tokenKind
	value string
}

// tokenKind enumerates the lexical classes this parser recognises.
type tokenKind int

const (
	tokenName tokenKind = iota
	tokenPunct
	tokenString
	tokenNumber
	tokenVariable
	tokenEOF
)

// parser walks a token stream.
type parser struct {
	tokens []token
	pos    int
	vars   map[string]any
}

// parse turns a document and its variables into the operations it declares.
func parse(document string, vars map[string]any) ([]operation, error) {
	tokens, err := lex(document)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens, vars: vars}
	var ops []operation
	for !p.atEOF() {
		op, err := p.parseOperation()
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return nil, errors.New("the document declares no operation")
	}
	return ops, nil
}

// parseOperation parses one operation, with or without the leading keyword.
func (p *parser) parseOperation() (operation, error) {
	op := operation{kind: opQuery}
	tok := p.peek()
	if tok.kind == tokenName {
		switch tok.value {
		case "query", "mutation":
			if tok.value == "mutation" {
				op.kind = opMutation
			}
			p.next()
			if p.peek().kind == tokenName {
				op.name = p.next().value
			}
			if p.peek().value == "(" {
				if err := p.skipBalanced("(", ")"); err != nil {
					return op, err
				}
			}
		case "subscription":
			return op, errors.New("subscriptions are not supported")
		default:
			return op, fmt.Errorf("unexpected token %q", tok.value)
		}
	}
	selections, err := p.parseSelectionSet()
	if err != nil {
		return op, err
	}
	op.selections = selections
	return op, nil
}

// parseSelectionSet parses a brace-delimited selection set.
func (p *parser) parseSelectionSet() ([]selection, error) {
	if p.peek().value != "{" {
		return nil, errors.New("expected a selection set")
	}
	p.next()
	var out []selection
	for {
		tok := p.peek()
		switch {
		case tok.kind == tokenEOF:
			return nil, errors.New("unterminated selection set")
		case tok.value == "}":
			p.next()
			return out, nil
		case tok.value == ",":
			p.next()
			continue
		case tok.value == "...":
			return nil, errors.New("fragments are not supported")
		}
		sel, err := p.parseSelection()
		if err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
}

// parseSelection parses one field selection.
func (p *parser) parseSelection() (selection, error) {
	var sel selection
	tok := p.next()
	if tok.kind != tokenName {
		return sel, fmt.Errorf("expected a field name, found %q", tok.value)
	}
	sel.name = tok.value
	if p.peek().value == ":" {
		p.next()
		nameTok := p.next()
		if nameTok.kind != tokenName {
			return sel, errors.New("expected a field name after an alias")
		}
		sel.alias = sel.name
		sel.name = nameTok.value
	}
	if p.peek().value == "(" {
		args, err := p.parseArguments()
		if err != nil {
			return sel, err
		}
		sel.args = args
	}
	if p.peek().value == "{" {
		subs, err := p.parseSelectionSet()
		if err != nil {
			return sel, err
		}
		sel.subs = subs
	}
	return sel, nil
}

// parseArguments parses a parenthesised argument list.
func (p *parser) parseArguments() (map[string]any, error) {
	p.next()
	args := map[string]any{}
	for {
		tok := p.next()
		switch {
		case tok.kind == tokenEOF:
			return nil, errors.New("unterminated argument list")
		case tok.value == ")":
			return args, nil
		case tok.value == ",":
			continue
		case tok.kind != tokenName:
			return nil, fmt.Errorf("expected an argument name, found %q", tok.value)
		}
		name := tok.value
		if p.next().value != ":" {
			return nil, fmt.Errorf("argument %q is missing its value", name)
		}
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		args[name] = value
	}
}

// parseValue parses a scalar argument value or a variable reference.
func (p *parser) parseValue() (any, error) {
	tok := p.next()
	switch tok.kind {
	case tokenString:
		return tok.value, nil
	case tokenNumber:
		if n, err := strconv.ParseInt(tok.value, 10, 64); err == nil {
			return n, nil
		}
		f, err := strconv.ParseFloat(tok.value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", tok.value)
		}
		return f, nil
	case tokenVariable:
		v, ok := p.vars[tok.value]
		if !ok {
			return nil, fmt.Errorf("variable $%s was not supplied", tok.value)
		}
		return v, nil
	case tokenName:
		switch tok.value {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return tok.value, nil
	default:
		return nil, fmt.Errorf("unsupported argument value %q", tok.value)
	}
}

// skipBalanced consumes a balanced delimiter pair. It is used to ignore the
// variable definitions of an operation header, whose types this executor does
// not need: values arrive already typed in the variables map.
func (p *parser) skipBalanced(open, closing string) error {
	depth := 0
	for {
		tok := p.next()
		if tok.kind == tokenEOF {
			return errors.New("unbalanced delimiters")
		}
		switch tok.value {
		case open:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return nil
			}
		}
	}
}

// peek returns the next token without consuming it.
func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{kind: tokenEOF}
	}
	return p.tokens[p.pos]
}

// next consumes and returns the next token.
func (p *parser) next() token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

// atEOF reports whether the whole document has been consumed.
func (p *parser) atEOF() bool {
	return p.peek().kind == tokenEOF
}

// lex converts a document into tokens, discarding the whitespace, commas, and
// comments GraphQL treats as insignificant.
func lex(document string) ([]token, error) {
	var out []token
	runes := []rune(document)
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '#':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		case c == '.':
			if !strings.HasPrefix(string(runes[i:]), "...") {
				return nil, errors.New("unexpected character \".\"")
			}
			out = append(out, token{kind: tokenPunct, value: "..."})
			i += 3
		case c == '$':
			start := i + 1
			i++
			for i < len(runes) && isNameRune(runes[i]) {
				i++
			}
			if start == i {
				return nil, errors.New("a variable reference is missing its name")
			}
			out = append(out, token{kind: tokenVariable, value: string(runes[start:i])})
		case strings.ContainsRune("{}():,=[]!@|&", c):
			out = append(out, token{kind: tokenPunct, value: string(c)})
			i++
		case c == '"':
			value, width, err := lexString(runes[i:])
			if err != nil {
				return nil, err
			}
			out = append(out, token{kind: tokenString, value: value})
			i += width
		case c == '-' || unicode.IsDigit(c):
			start := i
			i++
			for i < len(runes) && isNumberRune(runes[i]) {
				i++
			}
			out = append(out, token{kind: tokenNumber, value: string(runes[start:i])})
		case isNameRune(c):
			start := i
			for i < len(runes) && isNameRune(runes[i]) {
				i++
			}
			out = append(out, token{kind: tokenName, value: string(runes[start:i])})
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	return append(out, token{kind: tokenEOF}), nil
}

// lexString reads a quoted string, honouring the standard escapes and the
// triple-quoted block form. It returns the value and the number of runes it
// consumed.
func lexString(runes []rune) (string, int, error) {
	if strings.HasPrefix(string(runes), `"""`) {
		body := runes[3:]
		for i := 0; i+3 <= len(body); i++ {
			if body[i] == '"' && body[i+1] == '"' && body[i+2] == '"' {
				return strings.TrimSpace(string(body[:i])), i + 6, nil
			}
		}
		return "", 0, errors.New("unterminated block string")
	}
	var b strings.Builder
	i := 1
	for i < len(runes) {
		c := runes[i]
		switch c {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+1 >= len(runes) {
				return "", 0, errors.New("unterminated escape sequence")
			}
			i++
			switch runes[i] {
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			case 'r':
				b.WriteRune('\r')
			default:
				b.WriteRune(runes[i])
			}
			i++
		default:
			b.WriteRune(c)
			i++
		}
	}
	return "", 0, errors.New("unterminated string")
}

// isNameRune reports whether a rune may appear in a GraphQL name.
func isNameRune(c rune) bool {
	return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)
}

// isNumberRune reports whether a rune may appear in a numeric literal.
func isNumberRune(c rune) bool {
	return unicode.IsDigit(c) || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}
