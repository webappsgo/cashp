package metrics

import (
	"bytes"
	"io"
	"math"
	"strconv"
	"strings"
)

// ContentTypePrometheus is the media type of the Prometheus text exposition
// format served by the prometheus service.
const ContentTypePrometheus = "text/plain; version=0.0.4; charset=utf-8"

// WriteText writes samples in the Prometheus text exposition format. Samples
// must be grouped by metric name, which is what Collect guarantees.
func WriteText(w io.Writer, samples []Sample) error {
	var buf bytes.Buffer

	previous := ""
	for _, s := range samples {
		if s.Name != previous {
			if previous != "" {
				buf.WriteByte('\n')
			}

			if s.Help != "" {
				buf.WriteString("# HELP ")
				buf.WriteString(s.Name)
				buf.WriteByte(' ')
				buf.WriteString(escapeHelp(s.Help))
				buf.WriteByte('\n')
			}

			buf.WriteString("# TYPE ")
			buf.WriteString(s.Name)
			buf.WriteByte(' ')
			buf.WriteString(s.Type)
			buf.WriteByte('\n')

			previous = s.Name
		}

		writeSample(&buf, s)
	}

	_, err := w.Write(buf.Bytes())

	return err
}

// writeSample writes every line one sample contributes: a single line for a
// counter or gauge, and the bucket, sum, and count lines for a histogram.
func writeSample(buf *bytes.Buffer, s Sample) {
	if s.Type != TypeHistogram {
		writeSeries(buf, s.Name, s.Labels, nil, formatFloat(s.Value))

		return
	}

	for _, b := range s.Buckets {
		le := Label{Name: "le", Value: formatFloat(b.UpperBound)}
		writeSeries(buf, s.Name+"_bucket", s.Labels, &le, strconv.FormatUint(b.Count, 10))
	}

	inf := Label{Name: "le", Value: "+Inf"}
	writeSeries(buf, s.Name+"_bucket", s.Labels, &inf, strconv.FormatUint(s.Count, 10))
	writeSeries(buf, s.Name+"_sum", s.Labels, nil, formatFloat(s.Sum))
	writeSeries(buf, s.Name+"_count", s.Labels, nil, strconv.FormatUint(s.Count, 10))
}

// writeSeries writes one "name{labels} value" line, appending extra as the
// final label when it is set.
func writeSeries(buf *bytes.Buffer, name string, labels []Label, extra *Label, value string) {
	buf.WriteString(name)

	if len(labels) > 0 || extra != nil {
		buf.WriteByte('{')

		for i, l := range labels {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeLabel(buf, l)
		}

		if extra != nil {
			if len(labels) > 0 {
				buf.WriteByte(',')
			}
			writeLabel(buf, *extra)
		}

		buf.WriteByte('}')
	}

	buf.WriteByte(' ')
	buf.WriteString(value)
	buf.WriteByte('\n')
}

// writeLabel writes a single name="value" pair with the value escaped.
func writeLabel(buf *bytes.Buffer, l Label) {
	buf.WriteString(l.Name)
	buf.WriteString(`="`)
	buf.WriteString(escapeLabelValue(l.Value))
	buf.WriteString(`"`)
}

// formatFloat renders a value the way the exposition format expects, with
// the infinities and NaN spelled out.
func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}

	return strconv.FormatFloat(v, 'g', -1, 64)
}

// escapeHelp escapes the characters that would break a HELP line.
func escapeHelp(v string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(v)
}

// escapeLabelValue escapes the characters that would break a label value.
func escapeLabelValue(v string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(v)
}
