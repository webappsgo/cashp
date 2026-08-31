package billing

import (
	"fmt"
	"strings"
)

// Money in cashp is always an integer count of the smallest unit of an
// ISO-4217 currency (cents for USD, yen for JPY, fils for BHD). Floating
// point is never used for a monetary value anywhere in this package: every
// amount is an int64 named *Minor and every record that carries an amount
// also carries the currency it is denominated in.

// DefaultCurrency is used when an operator has not chosen one.
const DefaultCurrency = "USD"

// currencyExponents maps an ISO-4217 alphabetic code to the number of
// decimal digits the currency subdivides into. Currencies absent from this
// table are rejected rather than guessed, because guessing the exponent
// silently multiplies or divides an amount by one hundred.
var currencyExponents = map[string]int{
	"AED": 2, "ARS": 2, "AUD": 2, "BGN": 2, "BHD": 3, "BRL": 2, "CAD": 2,
	"CHF": 2, "CLP": 0, "CNY": 2, "COP": 2, "CZK": 2, "DKK": 2, "EGP": 2,
	"EUR": 2, "GBP": 2, "HKD": 2, "HRK": 2, "HUF": 2, "IDR": 2, "ILS": 2,
	"INR": 2, "ISK": 0, "JOD": 3, "JPY": 0, "KES": 2, "KRW": 0, "KWD": 3,
	"MAD": 2, "MXN": 2, "MYR": 2, "NGN": 2, "NOK": 2, "NZD": 2, "OMR": 3,
	"PEN": 2, "PHP": 2, "PKR": 2, "PLN": 2, "QAR": 2, "RON": 2, "RSD": 2,
	"RUB": 2, "SAR": 2, "SEK": 2, "SGD": 2, "THB": 2, "TND": 3, "TRY": 2,
	"TWD": 2, "TZS": 2, "UAH": 2, "USD": 2, "UYU": 2, "VND": 0, "ZAR": 2,
}

// SupportedCurrency reports whether the code is a currency this package can
// denominate an amount in.
func SupportedCurrency(code string) bool {
	_, ok := currencyExponents[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}

// NormalizeCurrency upper-cases and validates an ISO-4217 alphabetic code.
func NormalizeCurrency(code string) (string, error) {
	c := strings.ToUpper(strings.TrimSpace(code))
	if c == "" {
		return "", fmt.Errorf("billing: currency is required")
	}
	if _, ok := currencyExponents[c]; !ok {
		return "", fmt.Errorf("billing: unsupported currency %q", c)
	}
	return c, nil
}

// CurrencyExponent returns how many decimal digits a currency subdivides
// into. Unknown currencies report two, but callers that accept operator
// input must validate with NormalizeCurrency first.
func CurrencyExponent(code string) int {
	if exp, ok := currencyExponents[strings.ToUpper(strings.TrimSpace(code))]; ok {
		return exp
	}
	return 2
}

// pow10 returns 10^n for the small exponents currencies use.
func pow10(n int) int64 {
	out := int64(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}

// FormatMinor renders an integer minor-unit amount as a decimal string in
// the currency's own precision, for example (1234, "USD") -> "12.34" and
// (1234, "JPY") -> "1234". The currency code is not appended so callers can
// place it wherever their locale wants it.
func FormatMinor(amount int64, currency string) string {
	exp := CurrencyExponent(currency)
	if exp == 0 {
		return fmt.Sprintf("%d", amount)
	}
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}
	div := pow10(exp)
	return fmt.Sprintf("%s%d.%0*d", sign, amount/div, exp, amount%div)
}

// ParseMinor converts a human decimal string such as "12.34" into integer
// minor units for the given currency. It refuses more fractional digits than
// the currency has, rather than rounding an operator's input away.
func ParseMinor(s, currency string) (int64, error) {
	exp := CurrencyExponent(currency)
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, fmt.Errorf("billing: empty amount")
	}
	neg := false
	switch raw[0] {
	case '-':
		neg = true
		raw = raw[1:]
	case '+':
		raw = raw[1:]
	}
	whole, frac, hasFrac := strings.Cut(raw, ".")
	if whole == "" {
		whole = "0"
	}
	if hasFrac && len(frac) > exp {
		return 0, fmt.Errorf("billing: %q has more than %d fractional digits for %s", s, exp, currency)
	}
	for len(frac) < exp {
		frac += "0"
	}
	var out int64
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("billing: %q is not a valid amount", s)
		}
		out = out*10 + int64(r-'0')
	}
	if neg {
		out = -out
	}
	return out, nil
}

// PercentBasisPoints applies a rate expressed in basis points (1 bp =
// 0.01%) to an integer amount, rounding half away from zero. Tax rates,
// plan discounts and overage multipliers all use basis points so no rate is
// ever stored as a float either.
func PercentBasisPoints(amount, bps int64) int64 {
	if bps == 0 || amount == 0 {
		return 0
	}
	return divRoundHalfAway(amount*bps, 10000)
}

// Prorate scales an amount by a numerator/denominator ratio, rounding half
// away from zero. It is the single rounding rule used for every proration
// credit and charge so an upgrade and its mirror-image downgrade agree to
// the cent.
func Prorate(amount, numerator, denominator int64) int64 {
	if denominator == 0 || amount == 0 || numerator == 0 {
		return 0
	}
	if numerator >= denominator {
		return amount
	}
	if numerator < 0 {
		return 0
	}
	return divRoundHalfAway(amount*numerator, denominator)
}

// divRoundHalfAway divides two integers rounding halves away from zero.
func divRoundHalfAway(num, den int64) int64 {
	if den < 0 {
		num, den = -num, -den
	}
	if num < 0 {
		return -((-num*2 + den) / (den * 2))
	}
	return (num*2 + den) / (den * 2)
}

// SumMinor adds a list of minor-unit amounts.
func SumMinor(amounts ...int64) int64 {
	var total int64
	for _, a := range amounts {
		total += a
	}
	return total
}

// ClampNonNegative floors an amount at zero, used where a credit may not
// turn an invoice total negative.
func ClampNonNegative(amount int64) int64 {
	if amount < 0 {
		return 0
	}
	return amount
}
