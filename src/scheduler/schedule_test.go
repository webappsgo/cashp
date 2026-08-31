package scheduler

import (
	"strings"
	"testing"
	"time"
)

// mustParse parses a schedule in UTC and fails the test when it is invalid.
func mustParse(t *testing.T, expr string) Schedule {
	t.Helper()
	s, err := ParseSchedule(expr, time.UTC)
	if err != nil {
		t.Fatalf("ParseSchedule(%q) error: %v", expr, err)
	}
	return s
}

func TestParseScheduleCronNext(t *testing.T) {
	base := time.Date(2026, time.March, 10, 1, 30, 0, 0, time.UTC)
	cases := []struct {
		expr string
		want time.Time
	}{
		{"0 3 * * *", time.Date(2026, time.March, 10, 3, 0, 0, 0, time.UTC)},
		{"0 0 * * *", time.Date(2026, time.March, 11, 0, 0, 0, 0, time.UTC)},
		{"*/15 * * * *", time.Date(2026, time.March, 10, 1, 45, 0, 0, time.UTC)},
		{"0 3 * * 0", time.Date(2026, time.March, 15, 3, 0, 0, 0, time.UTC)},
		{"0 2 1 * *", time.Date(2026, time.April, 1, 2, 0, 0, 0, time.UTC)},
		{"30 1 * * *", time.Date(2026, time.March, 11, 1, 30, 0, 0, time.UTC)},
		{"0 0 1 jan *", time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"0 4,6 * * *", time.Date(2026, time.March, 10, 4, 0, 0, 0, time.UTC)},
		{"0 0-5 * * *", time.Date(2026, time.March, 10, 2, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		got := mustParse(t, tc.expr).Next(base)
		if !got.Equal(tc.want) {
			t.Errorf("%q next = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

func TestParseScheduleDescriptors(t *testing.T) {
	base := time.Date(2026, time.March, 10, 1, 30, 0, 0, time.UTC)
	cases := []struct {
		expr string
		want time.Time
	}{
		{"@hourly", time.Date(2026, time.March, 10, 2, 0, 0, 0, time.UTC)},
		{"@daily", time.Date(2026, time.March, 11, 0, 0, 0, 0, time.UTC)},
		{"@midnight", time.Date(2026, time.March, 11, 0, 0, 0, 0, time.UTC)},
		{"@weekly", time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC)},
		{"@monthly", time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)},
		{"@yearly", time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"@every 15m", base.Add(15 * time.Minute)},
		{"@every 2h", base.Add(2 * time.Hour)},
		{"@every 30s", base.Add(30 * time.Second)},
	}
	for _, tc := range cases {
		got := mustParse(t, tc.expr).Next(base)
		if !got.Equal(tc.want) {
			t.Errorf("%q next = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

func TestParseScheduleStringRoundTrip(t *testing.T) {
	for _, expr := range []string{"@every 5m", "0 3 * * *", "@daily"} {
		if got := mustParse(t, expr).String(); got != expr {
			t.Errorf("String() = %q, want %q", got, expr)
		}
	}
}

func TestParseScheduleErrors(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"0 3 * *",
		"0 3 * * * *",
		"60 * * * *",
		"* 24 * * *",
		"0 3 * * 9",
		"@every",
		"@every banana",
		"@every 100ms",
		"@sometimes",
		"5-1 * * * *",
		"*/0 * * * *",
		"a * * * *",
		", * * * *",
	}
	for _, expr := range bad {
		if _, err := ParseSchedule(expr, time.UTC); err == nil {
			t.Errorf("ParseSchedule(%q) expected error", expr)
		}
	}
}

func TestParseScheduleNilLocationUsesLocal(t *testing.T) {
	s, err := ParseSchedule("0 0 * * *", nil)
	if err != nil {
		t.Fatalf("ParseSchedule error: %v", err)
	}
	next := s.Next(time.Now())
	if next.IsZero() {
		t.Fatal("expected a next run time")
	}
}

func TestCronDayOfMonthOrDayOfWeek(t *testing.T) {
	// With both day fields restricted, either match selects the day.
	s := mustParse(t, "0 0 13 * 5")
	base := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	got := s.Next(base)
	want := time.Date(2026, time.March, 6, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("next = %s, want %s (first Friday)", got, want)
	}
}

func TestCronSundayAsSeven(t *testing.T) {
	a := mustParse(t, "0 3 * * 7")
	b := mustParse(t, "0 3 * * 0")
	base := time.Date(2026, time.March, 10, 1, 0, 0, 0, time.UTC)
	if !a.Next(base).Equal(b.Next(base)) {
		t.Errorf("weekday 7 (%s) must equal weekday 0 (%s)", a.Next(base), b.Next(base))
	}
}

func TestCronImpossibleDateReturnsZero(t *testing.T) {
	s := mustParse(t, "0 0 30 2 *")
	if got := s.Next(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)); !got.IsZero() {
		t.Errorf("February 30th must never match, got %s", got)
	}
}

func TestCronHonoursLocation(t *testing.T) {
	loc := time.FixedZone("TEST", 5*3600+30*60)
	s, err := ParseSchedule("0 3 * * *", loc)
	if err != nil {
		t.Fatalf("ParseSchedule error: %v", err)
	}
	base := time.Date(2026, time.March, 10, 0, 0, 0, 0, time.UTC)
	got := s.Next(base).In(loc)
	if got.Hour() != 3 || got.Minute() != 0 {
		t.Errorf("next = %s, want 03:00 in TEST zone", got)
	}
}

func TestIntervalScheduleInterval(t *testing.T) {
	s := mustParse(t, "@every 45s")
	iv, ok := s.(*intervalSchedule)
	if !ok {
		t.Fatalf("expected interval schedule, got %T", s)
	}
	if iv.Interval() != 45*time.Second {
		t.Errorf("interval = %s, want 45s", iv.Interval())
	}
}

func TestParseScheduleErrorMentionsExpression(t *testing.T) {
	_, err := ParseSchedule("0 99 * * *", time.UTC)
	if err == nil || !strings.Contains(err.Error(), "hour field") {
		t.Fatalf("expected hour field error, got %v", err)
	}
}
