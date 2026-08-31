package web

import (
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"
)

// buildFuncs returns the template helpers shared by every template set. They
// are deliberately presentation-only: no business logic lives in a template.
func buildFuncs() template.FuncMap {
	return template.FuncMap{
		"dict":        dict,
		"list":        list,
		"add":         func(a, b int) int { return a + b },
		"sub":         func(a, b int) int { return a - b },
		"fallback":    fallbackValue,
		"hasPrefix":   strings.HasPrefix,
		"hasSuffix":   strings.HasSuffix,
		"contains":    strings.Contains,
		"lower":       strings.ToLower,
		"upper":       strings.ToUpper,
		"humanize":    humanize,
		"bytes":       humanBytes,
		"percent":     percent,
		"pluralize":   pluralize,
		"statusClass": statusClass,
		"statusIcon":  statusIcon,
		"levelIcon":   levelIcon,
		"currentYear": currentYear,
		"formatTime":  formatTime,
		"relTime":     relTime,
		"isActive":    isActive,
	}
}

// dict builds a map from alternating key/value pairs so a partial can be
// invoked with several named arguments.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: expected an even number of arguments, got %d", len(pairs))
	}
	out := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d is not a string", i)
		}
		out[key] = pairs[i+1]
	}
	return out, nil
}

// list collects its arguments into a slice for range loops in templates.
func list(items ...any) []any {
	return items
}

// fallbackValue returns the fallback when the value is empty.
func fallbackValue(fallback any, value any) any {
	switch typed := value.(type) {
	case nil:
		return fallback
	case string:
		if typed == "" {
			return fallback
		}
	case int:
		if typed == 0 {
			return fallback
		}
	}
	return value
}

// humanize turns a snake_case or kebab-case identifier into a display label.
func humanize(value string) string {
	replaced := strings.NewReplacer("_", " ", "-", " ").Replace(value)
	fields := strings.Fields(replaced)
	for i, field := range fields {
		fields[i] = strings.ToUpper(field[:1]) + field[1:]
	}
	return strings.Join(fields, " ")
}

// humanBytes formats a byte count using binary units.
func humanBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	return fmt.Sprintf("%.1f %s", float64(size)/float64(div), units[exp])
}

// percent returns used/total as a whole percentage clamped to 0-100, which is
// what the usage meter renders as both a width and an accessible value.
func percent(used, total float64) int {
	if total <= 0 {
		return 0
	}
	value := int(math.Round(used / total * 100))
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

// pluralize returns singular or plural depending on the count.
func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

// statusClass maps a resource status to its badge modifier class.
func statusClass(status string) string {
	switch strings.ToLower(status) {
	case "running", "active", "healthy", "online", "succeeded", "ok":
		return "badge-success"
	case "stopped", "failed", "error", "offline", "unhealthy":
		return "badge-error"
	case "starting", "pending", "provisioning", "degraded", "restarting", "queued":
		return "badge-warning"
	case "suspended", "disabled", "archived":
		return "badge-muted"
	default:
		return "badge-info"
	}
}

// statusIcon pairs every status colour with a shape, so colour is never the
// only signal carrying meaning.
func statusIcon(status string) string {
	switch statusClass(status) {
	case "badge-success":
		return "●"
	case "badge-error":
		return "✕"
	case "badge-warning":
		return "▲"
	case "badge-muted":
		return "○"
	default:
		return "■"
	}
}

// levelIcon returns the glyph shown next to a flash or toast message.
func levelIcon(level string) string {
	switch level {
	case "success":
		return "✓"
	case "error":
		return "✕"
	case "warning":
		return "▲"
	default:
		return "ℹ"
	}
}

// currentYear returns the year used in the footer copyright line.
func currentYear() int {
	return time.Now().Year()
}

// formatTime renders a timestamp in an unambiguous, sortable form.
func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format("2006-01-02 15:04:05 MST")
}

// relTime renders a coarse "time ago" label for activity lists.
func relTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	delta := time.Since(value)
	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		minutes := int(delta.Minutes())
		return fmt.Sprintf("%d %s ago", minutes, pluralize(minutes, "minute", "minutes"))
	case delta < 24*time.Hour:
		hours := int(delta.Hours())
		return fmt.Sprintf("%d %s ago", hours, pluralize(hours, "hour", "hours"))
	default:
		days := int(delta.Hours() / 24)
		return fmt.Sprintf("%d %s ago", days, pluralize(days, "day", "days"))
	}
}

// isActive reports whether a navigation href matches the current path, used to
// set aria-current on the active link.
func isActive(current, href string) bool {
	if href == "/" {
		return current == "/"
	}
	return current == href || strings.HasPrefix(current, href+"/")
}
