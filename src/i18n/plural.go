package i18n

// CLDR cardinal plural categories. AI.md PART 31 mandates these names for the
// nested plural groups inside every locale catalog.
const (
	CategoryZero  = "zero"
	CategoryOne   = "one"
	CategoryTwo   = "two"
	CategoryFew   = "few"
	CategoryMany  = "many"
	CategoryOther = "other"
)

// pluralCategories lists every valid CLDR category name in CLDR order.
var pluralCategories = []string{
	CategoryZero,
	CategoryOne,
	CategoryTwo,
	CategoryFew,
	CategoryMany,
	CategoryOther,
}

// isPluralCategory reports whether name is a CLDR cardinal category name.
func isPluralCategory(name string) bool {
	for _, category := range pluralCategories {
		if category == name {
			return true
		}
	}

	return false
}

// PluralCategories returns every CLDR cardinal category name in CLDR order.
func PluralCategories() []string {
	out := make([]string, len(pluralCategories))
	copy(out, pluralCategories)

	return out
}

// requiredCategories records, per supported language, the CLDR cardinal
// categories a catalog must define for every plural group. "other" is
// mandatory everywhere; "zero" is an optional explicit zero-count message and
// is therefore never required.
var requiredCategories = map[string][]string{
	"en": {CategoryOne, CategoryOther},
	"es": {CategoryOne, CategoryOther},
	"de": {CategoryOne, CategoryOther},
	"fr": {CategoryOne, CategoryOther},
	"ar": {CategoryZero, CategoryOne, CategoryTwo, CategoryFew, CategoryMany, CategoryOther},
	"zh": {CategoryOther},
	"ja": {CategoryOther},
}

// RequiredCategories returns the CLDR cardinal categories that every plural
// group in the given language must define. Unknown languages fall back to the
// default locale's requirement.
func RequiredCategories(locale string) []string {
	categories, ok := requiredCategories[locale]
	if !ok {
		categories = requiredCategories[DefaultLocale]
	}

	out := make([]string, len(categories))
	copy(out, categories)

	return out
}

// Category returns the CLDR cardinal plural category for count in the given
// language. Counts are matched on their absolute value, and languages without
// a rule table use "other", which is the CLDR behaviour for Chinese and
// Japanese and a safe default for anything unrecognised.
func Category(locale string, count int) string {
	n := count
	if n < 0 {
		n = -n
	}

	switch locale {
	case "en", "de":
		// i = 1 and v = 0; integer counts always have v = 0.
		if n == 1 {
			return CategoryOne
		}
	case "es":
		if n == 1 {
			return CategoryOne
		}
	case "fr":
		// French treats 0 and 1 alike.
		if n == 0 || n == 1 {
			return CategoryOne
		}
	case "ar":
		return arabicCategory(n)
	}

	return CategoryOther
}

// arabicCategory implements the CLDR cardinal rules for Arabic.
func arabicCategory(n int) string {
	switch {
	case n == 0:
		return CategoryZero
	case n == 1:
		return CategoryOne
	case n == 2:
		return CategoryTwo
	}

	switch mod := n % 100; {
	case mod >= 3 && mod <= 10:
		return CategoryFew
	case mod >= 11 && mod <= 99:
		return CategoryMany
	}

	return CategoryOther
}

// selectForm picks the message text for count out of a plural group.
//
// An explicit "zero" form always wins at count zero, because catalogs use it
// for phrasing like "No results" that no CLDR rule would ever select in
// English. Otherwise the locale's CLDR category is used, and "other" is the
// guaranteed last resort — pluralForms refuses to build a group without it.
func selectForm(forms map[string]string, locale string, count int) string {
	if count == 0 {
		if msg, ok := forms[CategoryZero]; ok && msg != "" {
			return msg
		}
	}

	if msg, ok := forms[Category(locale, count)]; ok && msg != "" {
		return msg
	}

	return forms[CategoryOther]
}
