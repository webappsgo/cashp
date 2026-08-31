// Package scheduler implements cashp's built-in, always-running task
// scheduler per AI.md PART 19. External schedulers (cron, systemd timers,
// Task Scheduler, launchd, Kubernetes CronJobs, cloud schedulers) are never
// used for any scheduled task. State is persisted so it survives restarts,
// missed runs are replayed inside a catch-up window, cluster-wide tasks run
// on exactly one node, and all activity is logged to file only — never to
// the console.
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxCronSearchDays bounds the forward search for the next matching cron
// time so an impossible expression (for example February 30th) terminates
// instead of looping forever.
const maxCronSearchDays = 1500

// Schedule computes the next execution time for a task.
type Schedule interface {
	// Next returns the first execution time strictly after the given time,
	// or the zero time when the expression can never match again.
	Next(after time.Time) time.Time
	// String returns the canonical expression the schedule was parsed from.
	String() string
}

// ParseSchedule parses a schedule expression per AI.md PART 19 § Schedule
// Format. Supported forms are five-field cron expressions
// ("minute hour day month weekday"), the @hourly, @daily, @midnight,
// @weekly, @monthly, @yearly and @annually shorthands, and @every
// expressions with a Go duration ("@every 15m", "@every 2h", "@every 30s").
// A nil location is treated as time.Local.
func ParseSchedule(expr string, loc *time.Location) (Schedule, error) {
	if loc == nil {
		loc = time.Local
	}
	raw := strings.TrimSpace(expr)
	if raw == "" {
		return nil, fmt.Errorf("scheduler: empty schedule expression")
	}
	if strings.HasPrefix(raw, "@") {
		return parseDescriptor(raw, loc)
	}
	return parseCron(raw, raw, loc)
}

// parseDescriptor handles the @-prefixed shorthand expressions.
func parseDescriptor(raw string, loc *time.Location) (Schedule, error) {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "@every") {
		arg := strings.TrimSpace(strings.TrimPrefix(lower, "@every"))
		if arg == "" {
			return nil, fmt.Errorf("scheduler: @every requires a duration: %q", raw)
		}
		d, err := time.ParseDuration(arg)
		if err != nil {
			return nil, fmt.Errorf("scheduler: invalid @every duration %q: %w", arg, err)
		}
		if d < time.Second {
			return nil, fmt.Errorf("scheduler: @every duration must be at least 1s: %q", raw)
		}
		return &intervalSchedule{every: d, expr: raw}, nil
	}
	switch lower {
	case "@hourly":
		return parseCron("0 * * * *", raw, loc)
	case "@daily", "@midnight":
		return parseCron("0 0 * * *", raw, loc)
	case "@weekly":
		return parseCron("0 0 * * 0", raw, loc)
	case "@monthly":
		return parseCron("0 0 1 * *", raw, loc)
	case "@yearly", "@annually":
		return parseCron("0 0 1 1 *", raw, loc)
	}
	return nil, fmt.Errorf("scheduler: unknown schedule descriptor %q", raw)
}

// intervalSchedule fires at a fixed duration after the previous reference
// time. It is timezone independent by construction.
type intervalSchedule struct {
	every time.Duration
	expr  string
}

// Next returns the reference time advanced by the configured interval.
func (s *intervalSchedule) Next(after time.Time) time.Time {
	return after.Add(s.every)
}

// String returns the original expression.
func (s *intervalSchedule) String() string { return s.expr }

// Interval exposes the configured interval so callers can reason about
// tick frequency.
func (s *intervalSchedule) Interval() time.Duration { return s.every }

// cronField is a bitmask of the permitted values of one cron field.
type cronField struct {
	bits uint64
	star bool
}

// has reports whether the field permits the given value.
func (f cronField) has(v int) bool {
	if v < 0 || v > 63 {
		return false
	}
	return f.bits&(uint64(1)<<uint(v)) != 0
}

// cronSchedule is a parsed five-field cron expression evaluated in a fixed
// location.
type cronSchedule struct {
	minute cronField
	hour   cronField
	dom    cronField
	month  cronField
	dow    cronField
	loc    *time.Location
	expr   string
}

// monthNames maps the three-letter month abbreviations to their numbers.
var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// dowNames maps the three-letter weekday abbreviations to their numbers,
// where Sunday is 0.
var dowNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// parseCron parses a five-field cron expression. display is the expression
// reported by String, which differs from spec when a descriptor was
// expanded.
func parseCron(spec, display string, loc *time.Location) (Schedule, error) {
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return nil, fmt.Errorf("scheduler: cron expression %q must have 5 fields, got %d", display, len(parts))
	}
	minute, err := parseCronField(parts[0], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: %q minute field: %w", display, err)
	}
	hour, err := parseCronField(parts[1], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: %q hour field: %w", display, err)
	}
	dom, err := parseCronField(parts[2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: %q day-of-month field: %w", display, err)
	}
	month, err := parseCronField(parts[3], 1, 12, monthNames)
	if err != nil {
		return nil, fmt.Errorf("scheduler: %q month field: %w", display, err)
	}
	dow, err := parseCronField(normalizeDOW(parts[4]), 0, 6, dowNames)
	if err != nil {
		return nil, fmt.Errorf("scheduler: %q day-of-week field: %w", display, err)
	}
	return &cronSchedule{minute: minute, hour: hour, dom: dom, month: month, dow: dow, loc: loc, expr: display}, nil
}

// normalizeDOW rewrites the traditional day-of-week value 7 to 0 so both
// spellings of Sunday are accepted.
func normalizeDOW(field string) string {
	parts := strings.Split(field, ",")
	for i, p := range parts {
		if strings.TrimSpace(p) == "7" {
			parts[i] = "0"
		}
	}
	return strings.Join(parts, ",")
}

// parseCronField parses one comma-separated cron field into a bitmask.
func parseCronField(field string, min, max int, names map[string]int) (cronField, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return cronField{}, fmt.Errorf("empty field")
	}
	out := cronField{}
	for _, item := range strings.Split(field, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return cronField{}, fmt.Errorf("empty list element in %q", field)
		}
		step := 1
		if idx := strings.Index(item, "/"); idx >= 0 {
			stepStr := strings.TrimSpace(item[idx+1:])
			item = strings.TrimSpace(item[:idx])
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 {
				return cronField{}, fmt.Errorf("invalid step %q", stepStr)
			}
			step = n
		}
		low, high := min, max
		switch {
		case item == "*":
			if step == 1 {
				out.star = true
			}
		case strings.Contains(item, "-"):
			bounds := strings.SplitN(item, "-", 2)
			var err error
			if low, err = cronValue(bounds[0], min, max, names); err != nil {
				return cronField{}, err
			}
			if high, err = cronValue(bounds[1], min, max, names); err != nil {
				return cronField{}, err
			}
			if low > high {
				return cronField{}, fmt.Errorf("range %q is inverted", item)
			}
		default:
			v, err := cronValue(item, min, max, names)
			if err != nil {
				return cronField{}, err
			}
			low, high = v, v
			if step > 1 {
				high = max
			}
		}
		for v := low; v <= high; v += step {
			out.bits |= uint64(1) << uint(v)
		}
	}
	if out.bits == 0 {
		return cronField{}, fmt.Errorf("field %q matches no values", field)
	}
	return out, nil
}

// cronValue converts a single numeric or named cron token to its integer
// value, enforcing the field bounds.
func cronValue(token string, min, max int, names map[string]int) (int, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return 0, fmt.Errorf("empty value")
	}
	if names != nil {
		if v, ok := names[token]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", token)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("value %d out of range %d-%d", v, min, max)
	}
	return v, nil
}

// Next returns the first minute strictly after the given time that matches
// the expression, evaluated in the schedule's location.
func (s *cronSchedule) Next(after time.Time) time.Time {
	a := after.In(s.loc)
	t := time.Date(a.Year(), a.Month(), a.Day(), a.Hour(), a.Minute(), 0, 0, s.loc).Add(time.Minute)
	limit := t.AddDate(0, 0, maxCronSearchDays)
	for t.Before(limit) {
		if !s.matchDay(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.loc).AddDate(0, 0, 1)
			continue
		}
		if s.hour.has(t.Hour()) && s.minute.has(t.Minute()) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// String returns the expression the schedule was parsed from.
func (s *cronSchedule) String() string { return s.expr }

// matchDay applies the standard cron day rule: when both day-of-month and
// day-of-week are restricted the day matches if either matches, otherwise
// both restrictions must hold.
func (s *cronSchedule) matchDay(t time.Time) bool {
	if !s.month.has(int(t.Month())) {
		return false
	}
	domMatch := s.dom.has(t.Day())
	dowMatch := s.dow.has(int(t.Weekday()))
	if !s.dom.star && !s.dow.star {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}
