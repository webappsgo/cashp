package i18n

import (
	"strconv"
	"strings"
	"time"
)

// nbsp is the no-break space used between a number and a trailing symbol so
// the pair never wraps across a line.
const nbsp = " "

// arabicIndicDigits maps ASCII digit values onto Arabic-Indic digits.
var arabicIndicDigits = [10]rune{'٠', '١', '٢', '٣', '٤', '٥', '٦', '٧', '٨', '٩'}

// localeFormat holds the locale-specific separators, digit shapes and layouts
// used by the formatting helpers. The tables follow the CLDR conventions
// tabulated in AI.md PART 31 for each supported language.
type localeFormat struct {
	group           string
	decimal         string
	percentGap      string
	percentSymbol   string
	digits          *[10]rune
	dateLayout      string
	timeLayout      string
	dateTimeSep     string
	currencyPrefix  bool
	currencyGap     string
	defaultCurrency string
	amMarker        string
	pmMarker        string
}

// formats maps every supported locale onto its formatting conventions.
var formats = map[string]localeFormat{
	"en": {
		group:           ",",
		decimal:         ".",
		percentSymbol:   "%",
		dateLayout:      "01/02/2006",
		timeLayout:      "3:04 PM",
		dateTimeSep:     ", ",
		currencyPrefix:  true,
		defaultCurrency: "USD",
	},
	"es": {
		group:           ".",
		decimal:         ",",
		percentGap:      nbsp,
		percentSymbol:   "%",
		dateLayout:      "02/01/2006",
		timeLayout:      "15:04",
		dateTimeSep:     ", ",
		currencyGap:     nbsp,
		defaultCurrency: "EUR",
	},
	"de": {
		group:           ".",
		decimal:         ",",
		percentGap:      nbsp,
		percentSymbol:   "%",
		dateLayout:      "02.01.2006",
		timeLayout:      "15:04",
		dateTimeSep:     ", ",
		currencyGap:     nbsp,
		defaultCurrency: "EUR",
	},
	"fr": {
		group:           " ",
		decimal:         ",",
		percentGap:      nbsp,
		percentSymbol:   "%",
		dateLayout:      "02/01/2006",
		timeLayout:      "15:04",
		dateTimeSep:     ", ",
		currencyGap:     nbsp,
		defaultCurrency: "EUR",
	},
	"ar": {
		group:           "٬",
		decimal:         "٫",
		percentSymbol:   "٪",
		digits:          &arabicIndicDigits,
		dateLayout:      "02/01/2006",
		timeLayout:      "3:04 PM",
		dateTimeSep:     "، ",
		currencyGap:     nbsp,
		defaultCurrency: "SAR",
		amMarker:        "ص",
		pmMarker:        "م",
	},
	"zh": {
		group:           ",",
		decimal:         ".",
		percentSymbol:   "%",
		dateLayout:      "2006/01/02",
		timeLayout:      "15:04",
		dateTimeSep:     " ",
		currencyPrefix:  true,
		defaultCurrency: "CNY",
	},
	"ja": {
		group:           ",",
		decimal:         ".",
		percentSymbol:   "%",
		dateLayout:      "2006/01/02",
		timeLayout:      "15:04",
		dateTimeSep:     " ",
		currencyPrefix:  true,
		defaultCurrency: "JPY",
	},
}

// currencySymbols maps ISO 4217 codes onto their display symbols. Codes with
// no entry render as the bare code, which is unambiguous and never wrong.
var currencySymbols = map[string]string{
	"USD": "$",
	"CAD": "$",
	"AUD": "$",
	"EUR": "€",
	"GBP": "£",
	"JPY": "¥",
	"CNY": "¥",
	"SAR": "ر.س",
	"AED": "د.إ",
	"INR": "₹",
	"BRL": "R$",
	"CHF": "CHF",
	"SEK": "kr",
	"NOK": "kr",
	"DKK": "kr",
	"PLN": "zł",
	"MXN": "$",
	"ZAR": "R",
}

// zeroDecimalCurrencies lists ISO 4217 codes whose minor unit is zero digits.
var zeroDecimalCurrencies = map[string]bool{
	"JPY": true,
	"KRW": true,
	"VND": true,
	"CLP": true,
	"ISK": true,
	"XAF": true,
	"XOF": true,
	"XPF": true,
}

// formatFor returns the formatting conventions for the resolved locale.
func (b *Bundle) formatFor(locale string) localeFormat {
	code := b.Resolve(locale)

	if f, ok := formats[code]; ok {
		return f
	}

	return formats[DefaultLocale]
}

// FormatNumber renders a decimal number with the resolved locale's grouping
// separator, decimal separator and digit shapes, to two fraction digits.
func (b *Bundle) FormatNumber(locale string, v float64) string {
	return b.formatFor(locale).number(v, 2)
}

// FormatDecimal renders a number with an explicit number of fraction digits.
func (b *Bundle) FormatDecimal(locale string, v float64, digits int) string {
	return b.formatFor(locale).number(v, digits)
}

// FormatInt renders an integer with the resolved locale's grouping separator
// and digit shapes, without any fraction part.
func (b *Bundle) FormatInt(locale string, v int64) string {
	f := b.formatFor(locale)

	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}

	return sign + f.shape(f.group3(strconv.FormatInt(v, 10)))
}

// FormatPercent renders a percentage value, where 45.5 renders as "45.5%".
func (b *Bundle) FormatPercent(locale string, v float64) string {
	f := b.formatFor(locale)

	return f.number(v, percentDigits(v)) + f.percentGap + f.percentSymbol
}

// percentDigits keeps whole percentages free of a pointless ".0" while
// preserving one fraction digit for values that need it.
func percentDigits(v float64) int {
	if v == float64(int64(v)) {
		return 0
	}

	return 1
}

// FormatCurrency renders a monetary amount for the resolved locale. An empty
// currency code uses the locale's default currency, and the minor-unit digit
// count follows ISO 4217 so JPY renders without a fraction part.
func (b *Bundle) FormatCurrency(locale string, v float64, currency string) string {
	f := b.formatFor(locale)

	code := strings.ToUpper(strings.TrimSpace(currency))
	if code == "" {
		code = f.defaultCurrency
	}

	digits := 2
	if zeroDecimalCurrencies[code] {
		digits = 0
	}

	symbol, ok := currencySymbols[code]
	if !ok {
		symbol = code
	}

	amount := f.number(v, digits)

	if f.currencyPrefix {
		return symbol + f.currencyGap + amount
	}

	return amount + f.currencyGap + symbol
}

// FormatDate renders a date in the resolved locale's short date order, with
// locale digit shapes applied.
func (b *Bundle) FormatDate(locale string, t time.Time) string {
	f := b.formatFor(locale)

	return f.shape(t.Format(f.dateLayout))
}

// FormatTime renders a time of day in the resolved locale's convention,
// including localized day-period markers where the locale uses a 12-hour clock.
func (b *Bundle) FormatTime(locale string, t time.Time) string {
	f := b.formatFor(locale)

	out := t.Format(f.timeLayout)

	if f.amMarker != "" {
		out = strings.ReplaceAll(out, "AM", f.amMarker)
	}

	if f.pmMarker != "" {
		out = strings.ReplaceAll(out, "PM", f.pmMarker)
	}

	return f.shape(out)
}

// FormatDateTime renders a date and time of day joined by the locale's
// separator.
func (b *Bundle) FormatDateTime(locale string, t time.Time) string {
	f := b.formatFor(locale)

	return b.FormatDate(locale, t) + f.dateTimeSep + b.FormatTime(locale, t)
}

// number renders v with the given fraction digit count using this locale's
// separators and digit shapes.
func (f localeFormat) number(v float64, digits int) string {
	if digits < 0 {
		digits = 0
	}

	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}

	raw := strconv.FormatFloat(v, 'f', digits, 64)

	integer, fraction, hasFraction := strings.Cut(raw, ".")

	out := f.group3(integer)
	if hasFraction {
		out += f.decimal + fraction
	}

	return sign + f.shape(out)
}

// group3 inserts the locale's grouping separator every three digits, counting
// from the right.
func (f localeFormat) group3(digits string) string {
	if len(digits) <= 3 || f.group == "" {
		return digits
	}

	var out strings.Builder

	lead := len(digits) % 3
	if lead > 0 {
		out.WriteString(digits[:lead])
	}

	for i := lead; i < len(digits); i += 3 {
		if out.Len() > 0 {
			out.WriteString(f.group)
		}

		out.WriteString(digits[i : i+3])
	}

	return out.String()
}

// shape rewrites ASCII digits into the locale's own digit forms. Locales
// without a digit table are returned unchanged.
func (f localeFormat) shape(s string) string {
	if f.digits == nil {
		return s
	}

	var out strings.Builder
	out.Grow(len(s))

	for _, r := range s {
		if r >= '0' && r <= '9' {
			out.WriteRune(f.digits[r-'0'])

			continue
		}

		out.WriteRune(r)
	}

	return out.String()
}
