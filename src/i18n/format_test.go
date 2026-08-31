package i18n

import (
	"testing"
	"time"
)

// sample is the reference instant used by every formatting test.
var sample = time.Date(2024, time.March, 9, 15, 4, 5, 0, time.UTC)

func TestFormatNumber(t *testing.T) {
	bundle := newTestBundle(t)

	cases := map[string]string{
		"en": "1,234.56",
		"es": "1.234,56",
		"de": "1.234,56",
		"fr": "1 234,56",
		"zh": "1,234.56",
		"ja": "1,234.56",
		"ar": "١٬٢٣٤٫٥٦",
	}

	for locale, want := range cases {
		if got := bundle.FormatNumber(locale, 1234.56); got != want {
			t.Errorf("FormatNumber(%s) = %q, want %q", locale, got, want)
		}
	}

	if got := bundle.FormatNumber("pt", 1234.56); got != cases[DefaultLocale] {
		t.Errorf("unsupported locale did not use the default format: %q", got)
	}

	if got := bundle.FormatNumber("en", -1234.5); got != "-1,234.50" {
		t.Errorf("negative FormatNumber = %q", got)
	}

	if got := bundle.FormatDecimal("en", 0.125, 3); got != "0.125" {
		t.Errorf("FormatDecimal = %q", got)
	}

	if got := bundle.FormatDecimal("en", 1234.6, -1); got != "1,235" {
		t.Errorf("negative digit count was not clamped: %q", got)
	}
}

func TestFormatInt(t *testing.T) {
	bundle := newTestBundle(t)

	cases := []struct {
		locale string
		value  int64
		want   string
	}{
		{"en", 0, "0"},
		{"en", 999, "999"},
		{"en", 1000, "1,000"},
		{"en", 1234567, "1,234,567"},
		{"en", -1234567, "-1,234,567"},
		{"de", 1234567, "1.234.567"},
		{"ar", 1234, "١٬٢٣٤"},
	}

	for _, tc := range cases {
		if got := bundle.FormatInt(tc.locale, tc.value); got != tc.want {
			t.Errorf("FormatInt(%s, %d) = %q, want %q", tc.locale, tc.value, got, tc.want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	bundle := newTestBundle(t)

	cases := []struct {
		locale string
		value  float64
		want   string
	}{
		{"en", 45.5, "45.5%"},
		{"en", 45, "45%"},
		{"de", 45, "45 %"},
		{"fr", 12.5, "12,5 %"},
		{"ar", 45, "٤٥٪"},
	}

	for _, tc := range cases {
		if got := bundle.FormatPercent(tc.locale, tc.value); got != tc.want {
			t.Errorf("FormatPercent(%s, %v) = %q, want %q", tc.locale, tc.value, got, tc.want)
		}
	}
}

func TestFormatCurrency(t *testing.T) {
	bundle := newTestBundle(t)

	cases := []struct {
		locale   string
		value    float64
		currency string
		want     string
	}{
		{"en", 1234.56, "USD", "$1,234.56"},
		{"de", 1234.56, "EUR", "1.234,56 €"},
		{"fr", 1234.56, "eur", "1 234,56 €"},
		{"ja", 1234.56, "JPY", "¥1,235"},
		{"zh", 1234.56, "CNY", "¥1,234.56"},
		{"de", 5, "", "5,00 €"},
		{"en", 5, "XYZ", "XYZ5.00"},
	}

	for _, tc := range cases {
		got := bundle.FormatCurrency(tc.locale, tc.value, tc.currency)
		if got != tc.want {
			t.Errorf("FormatCurrency(%s, %v, %q) = %q, want %q", tc.locale, tc.value, tc.currency, got, tc.want)
		}
	}
}

func TestFormatDateAndTime(t *testing.T) {
	bundle := newTestBundle(t)

	dates := map[string]string{
		"en": "03/09/2024",
		"es": "09/03/2024",
		"fr": "09/03/2024",
		"de": "09.03.2024",
		"zh": "2024/03/09",
		"ja": "2024/03/09",
		"ar": "٠٩/٠٣/٢٠٢٤",
	}

	for locale, want := range dates {
		if got := bundle.FormatDate(locale, sample); got != want {
			t.Errorf("FormatDate(%s) = %q, want %q", locale, got, want)
		}
	}

	times := map[string]string{
		"en": "3:04 PM",
		"de": "15:04",
		"ja": "15:04",
		"ar": "٣:٠٤ م",
	}

	for locale, want := range times {
		if got := bundle.FormatTime(locale, sample); got != want {
			t.Errorf("FormatTime(%s) = %q, want %q", locale, got, want)
		}
	}

	if got := bundle.FormatDateTime("en", sample); got != "03/09/2024, 3:04 PM" {
		t.Errorf("FormatDateTime(en) = %q", got)
	}

	if got := bundle.FormatDateTime("ja", sample); got != "2024/03/09 15:04" {
		t.Errorf("FormatDateTime(ja) = %q", got)
	}

	// The Arabic morning marker must replace AM, not survive as Latin text.
	morning := time.Date(2024, time.March, 9, 9, 5, 0, 0, time.UTC)
	if got := bundle.FormatTime("ar", morning); got != "٩:٠٥ ص" {
		t.Errorf("FormatTime(ar, morning) = %q", got)
	}
}

func TestGlobalFormatHelpers(t *testing.T) {
	if got := FormatNumber("de", 1234.56); got != "1.234,56" {
		t.Errorf("FormatNumber(de) = %q", got)
	}

	if got := FormatDate("de", sample); got != "09.03.2024" {
		t.Errorf("FormatDate(de) = %q", got)
	}
}

func TestEveryLocaleHasFormattingRules(t *testing.T) {
	bundle := newTestBundle(t)

	for _, code := range bundle.Locales() {
		f, ok := formats[code]
		if !ok {
			t.Errorf("%s: no formatting rules defined", code)

			continue
		}

		if f.decimal == "" || f.dateLayout == "" || f.timeLayout == "" || f.defaultCurrency == "" {
			t.Errorf("%s: incomplete formatting rules %+v", code, f)
		}
	}
}
