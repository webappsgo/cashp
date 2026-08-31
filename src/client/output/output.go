// Package output renders CLI results in the formats AI.md PART 33 defines:
// table, json, yaml, plain and csv. Colour follows the same precedence as
// the server binary in src/main.go.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/webappsgo/cashp/src/config"
)

// Supported output formats.
const (
	FormatTable = "table"
	FormatJSON  = "json"
	FormatYAML  = "yaml"
	FormatPlain = "plain"
	FormatCSV   = "csv"
)

// ANSI colour codes used for status emphasis only; layout never depends on
// colour being available.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

// Formats lists every valid --output value.
var Formats = []string{FormatTable, FormatJSON, FormatYAML, FormatPlain, FormatCSV}

// IsValidFormat reports whether name is a supported output format.
func IsValidFormat(name string) bool {
	for _, format := range Formats {
		if format == strings.ToLower(strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// Writer renders values to a stream in the configured format.
type Writer struct {
	out     io.Writer
	errOut  io.Writer
	format  string
	color   bool
	unicode bool
	quiet   bool
}

// Options configures a Writer.
type Options struct {
	Out     io.Writer
	ErrOut  io.Writer
	Format  string
	Color   bool
	Unicode bool
	Quiet   bool
}

// New builds a Writer, defaulting to table output on stdout.
func New(opts Options) *Writer {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	errOut := opts.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if !IsValidFormat(format) {
		format = FormatTable
	}
	return &Writer{
		out:     out,
		errOut:  errOut,
		format:  format,
		color:   opts.Color,
		unicode: opts.Unicode,
		quiet:   opts.Quiet,
	}
}

// Format returns the active output format.
func (w *Writer) Format() string {
	return w.format
}

// Color reports whether colour output is enabled.
func (w *Writer) Color() bool {
	return w.color
}

// Table is a rendered result set: headers plus string rows.
type Table struct {
	Headers []string
	Rows    [][]string
}

// Emit renders value in the configured format. structured is the raw
// decoded payload used for json/yaml; table is used for table/plain/csv.
func (w *Writer) Emit(structured any, table Table) error {
	switch w.format {
	case FormatJSON:
		return w.JSON(structured)
	case FormatYAML:
		return w.YAML(structured)
	case FormatCSV:
		return w.CSV(table)
	case FormatPlain:
		return w.Plain(table)
	default:
		return w.Table(table)
	}
}

// JSON writes value as indented JSON with a trailing newline.
func (w *Writer) JSON(value any) error {
	encoder := json.NewEncoder(w.out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// YAML writes value as YAML.
func (w *Writer) YAML(value any) error {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode yaml: %w", err)
	}
	_, err = w.out.Write(encoded)
	return err
}

// CSV writes the table as RFC 4180 CSV including the header row.
func (w *Writer) CSV(table Table) error {
	writer := csv.NewWriter(w.out)
	if len(table.Headers) > 0 {
		if err := writer.Write(table.Headers); err != nil {
			return fmt.Errorf("write csv header: %w", err)
		}
	}
	for _, row := range table.Rows {
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write csv row: %w", err)
		}
	}
	writer.Flush()
	return writer.Error()
}

// Plain writes tab-separated rows with no header and no decoration, which
// is the format shell pipelines consume.
func (w *Writer) Plain(table Table) error {
	for _, row := range table.Rows {
		if _, err := fmt.Fprintln(w.out, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// Table writes an aligned table. Box-drawing characters are used when
// Unicode is enabled, and ASCII is used otherwise so the output stays
// readable in a plain terminal or a pipe.
func (w *Writer) Table(table Table) error {
	if len(table.Headers) == 0 && len(table.Rows) == 0 {
		return nil
	}

	widths := columnWidths(table)
	glyphs := asciiGlyphs
	if w.unicode {
		glyphs = unicodeGlyphs
	}

	if err := w.writeRule(glyphs.topLeft, glyphs.topJoin, glyphs.topRight, glyphs.horizontal, widths); err != nil {
		return err
	}
	if len(table.Headers) > 0 {
		if err := w.writeRow(table.Headers, widths, glyphs, true); err != nil {
			return err
		}
		if err := w.writeRule(glyphs.midLeft, glyphs.midJoin, glyphs.midRight, glyphs.horizontal, widths); err != nil {
			return err
		}
	}
	for _, row := range table.Rows {
		if err := w.writeRow(row, widths, glyphs, false); err != nil {
			return err
		}
	}
	return w.writeRule(glyphs.bottomLeft, glyphs.bottomJoin, glyphs.bottomRight, glyphs.horizontal, widths)
}

// Message writes an informational line to stdout unless --quiet is active.
func (w *Writer) Message(format string, args ...any) {
	if w.quiet {
		return
	}
	fmt.Fprintf(w.out, format+"\n", args...)
}

// Success writes a confirmation line, green when colour is enabled.
func (w *Writer) Success(format string, args ...any) {
	if w.quiet {
		return
	}
	fmt.Fprintln(w.out, w.colorize(ansiGreen, fmt.Sprintf(format, args...)))
}

// Warn writes a warning line to stderr.
func (w *Writer) Warn(format string, args ...any) {
	fmt.Fprintln(w.errOut, w.colorize(ansiYellow, fmt.Sprintf(format, args...)))
}

// Error writes an error line to stderr. Callers must pass an already
// user-safe message: no tokens, DSNs or filesystem paths.
func (w *Writer) Error(format string, args ...any) {
	fmt.Fprintln(w.errOut, w.colorize(ansiRed, fmt.Sprintf(format, args...)))
}

// Detail writes a dimmed secondary line to stderr, used for --debug traces.
func (w *Writer) Detail(format string, args ...any) {
	fmt.Fprintln(w.errOut, w.colorize(ansiDim, fmt.Sprintf(format, args...)))
}

// colorize wraps text in an ANSI code when colour is enabled.
func (w *Writer) colorize(code, text string) string {
	if !w.color {
		return text
	}
	return code + text + ansiReset
}

// writeRow renders one table row, padding each cell to its column width.
func (w *Writer) writeRow(cells []string, widths []int, glyphs tableGlyphs, header bool) error {
	var builder strings.Builder
	builder.WriteString(glyphs.vertical)
	for index, width := range widths {
		cell := ""
		if index < len(cells) {
			cell = cells[index]
		}
		text := " " + cell + strings.Repeat(" ", width-runeLen(cell)) + " "
		if header && w.color {
			text = ansiBold + text + ansiReset
		}
		builder.WriteString(text)
		builder.WriteString(glyphs.vertical)
	}
	_, err := fmt.Fprintln(w.out, builder.String())
	return err
}

// writeRule renders a horizontal table rule.
func (w *Writer) writeRule(left, join, right, horizontal string, widths []int) error {
	var builder strings.Builder
	builder.WriteString(left)
	for index, width := range widths {
		builder.WriteString(strings.Repeat(horizontal, width+2))
		if index == len(widths)-1 {
			builder.WriteString(right)
			continue
		}
		builder.WriteString(join)
	}
	_, err := fmt.Fprintln(w.out, builder.String())
	return err
}

// tableGlyphs is one set of table-drawing characters.
type tableGlyphs struct {
	horizontal  string
	vertical    string
	topLeft     string
	topJoin     string
	topRight    string
	midLeft     string
	midJoin     string
	midRight    string
	bottomLeft  string
	bottomJoin  string
	bottomRight string
}

// unicodeGlyphs is the default box-drawing set.
var unicodeGlyphs = tableGlyphs{
	horizontal:  "─",
	vertical:    "│",
	topLeft:     "┌",
	topJoin:     "┬",
	topRight:    "┐",
	midLeft:     "├",
	midJoin:     "┼",
	midRight:    "┤",
	bottomLeft:  "└",
	bottomJoin:  "┴",
	bottomRight: "┘",
}

// asciiGlyphs is the fallback set for terminals without Unicode support.
var asciiGlyphs = tableGlyphs{
	horizontal:  "-",
	vertical:    "|",
	topLeft:     "+",
	topJoin:     "+",
	topRight:    "+",
	midLeft:     "+",
	midJoin:     "+",
	midRight:    "+",
	bottomLeft:  "+",
	bottomJoin:  "+",
	bottomRight: "+",
}

// columnWidths computes the display width of each column.
func columnWidths(table Table) []int {
	count := len(table.Headers)
	for _, row := range table.Rows {
		if len(row) > count {
			count = len(row)
		}
	}

	widths := make([]int, count)
	for index, header := range table.Headers {
		widths[index] = runeLen(header)
	}
	for _, row := range table.Rows {
		for index, cell := range row {
			if runeLen(cell) > widths[index] {
				widths[index] = runeLen(cell)
			}
		}
	}
	return widths
}

// runeLen counts runes rather than bytes so multi-byte values align.
func runeLen(s string) int {
	return len([]rune(s))
}

// TableFromMaps builds a Table from a list of JSON objects, using headers
// when supplied and the sorted union of keys otherwise.
func TableFromMaps(items []map[string]any, headers []string) Table {
	if len(headers) == 0 {
		keys := map[string]bool{}
		for _, item := range items {
			for key := range item {
				keys[key] = true
			}
		}
		for key := range keys {
			headers = append(headers, key)
		}
		sort.Strings(headers)
	}

	rows := make([][]string, 0, len(items))
	for _, item := range items {
		row := make([]string, 0, len(headers))
		for _, header := range headers {
			row = append(row, Stringify(item[header]))
		}
		rows = append(rows, row)
	}
	return Table{Headers: upperAll(headers), Rows: rows}
}

// TableFromMap builds a two-column key/value table from a single object.
func TableFromMap(item map[string]any) Table {
	keys := make([]string, 0, len(item))
	for key := range item {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{key, Stringify(item[key])})
	}
	return Table{Headers: []string{"FIELD", "VALUE"}, Rows: rows}
}

// Stringify renders a decoded JSON value as a single-line cell.
func Stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%g", typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, element := range typed {
			parts = append(parts, Stringify(element))
		}
		return strings.Join(parts, ",")
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

// upperAll upper-cases header labels for table display.
func upperAll(values []string) []string {
	headers := make([]string, 0, len(values))
	for _, value := range values {
		headers = append(headers, strings.ToUpper(value))
	}
	return headers
}

// ResolveColor applies the colour precedence used by the server binary:
// an explicit --color flag, then NO_COLOR, then COLOR, then TTY detection.
func ResolveColor(flagValue string, isTTY bool) bool {
	if flagValue != "" {
		switch strings.ToLower(strings.TrimSpace(flagValue)) {
		case "always", "force":
			return true
		case "never", "none":
			return false
		case "auto":
			return isTTY
		}
		if enabled, err := config.ParseBool(flagValue, isTTY); err == nil {
			return enabled
		}
	}
	if _, present := os.LookupEnv("NO_COLOR"); present {
		return false
	}
	if value, present := os.LookupEnv("COLOR"); present {
		if enabled, err := config.ParseBool(value, isTTY); err == nil {
			return enabled
		}
	}
	return isTTY
}

// ResolveUnicode reports whether box-drawing characters are safe to use.
// A non-TTY stream, NO_COLOR or an explicit tui.unicode:false all force the
// ASCII fallback.
func ResolveUnicode(configured bool, isTTY bool) bool {
	if !configured || !isTTY {
		return false
	}
	if _, present := os.LookupEnv("NO_COLOR"); present {
		return false
	}
	return true
}
