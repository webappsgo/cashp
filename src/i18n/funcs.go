package i18n

import (
	"html/template"
	"time"
)

// Funcs returns the template functions for a page already bound to one
// locale, so templates call {{t "nav.home"}} without threading the language
// through every partial.
//
// Merge exactly one of Funcs and FuncMap into a template: they define the
// same names with different signatures.
func (b *Bundle) Funcs(locale string) template.FuncMap {
	return template.FuncMap{
		// Messages.
		"t":  func(key string, args ...any) string { return b.T(locale, key, args...) },
		"tf": func(key string, args ...any) string { return b.T(locale, key, args...) },
		"tp": func(key string, count int, args ...any) string { return b.N(locale, key, count, args...) },

		// Locale identity.
		"lang":      func() string { return b.Resolve(locale) },
		"dir":       func() string { return b.Dir(locale) },
		"isRTL":     func() bool { return b.IsRTL(locale) },
		"langs":     b.Available,
		"langAttrs": func() template.HTMLAttr { return b.LangAttrs(locale) },

		// Formatting.
		"num":      func(v float64) string { return b.FormatNumber(locale, v) },
		"decimal":  func(v float64, digits int) string { return b.FormatDecimal(locale, v, digits) },
		"count":    func(v int64) string { return b.FormatInt(locale, v) },
		"pct":      func(v float64) string { return b.FormatPercent(locale, v) },
		"money":    func(v float64, currency string) string { return b.FormatCurrency(locale, v, currency) },
		"date":     func(t time.Time) string { return b.FormatDate(locale, t) },
		"clock":    func(t time.Time) string { return b.FormatTime(locale, t) },
		"datetime": func(t time.Time) string { return b.FormatDateTime(locale, t) },

		// Accessibility.
		"skipLinks":      func() []SkipLink { return b.SkipLinks(locale) },
		"navAttrs":       func() template.HTMLAttr { return b.NavAttrs(locale) },
		"mainAttrs":      MainAttrs,
		"bannerAttrs":    BannerAttrs,
		"footerAttrs":    ContentInfoAttrs,
		"asideAttrs":     ComplementaryAttrs,
		"landmarkAttrs":  LandmarkAttrs,
		"attrs":          Attrs,
		"ariaAttrs":      AriaAttrs,
		"liveRegion":     LiveRegionAttrs,
		"dialogAttrs":    DialogAttrs,
		"fieldAttrs":     FieldAttrs,
		"errorRegion":    ErrorRegionAttrs,
		"toggleAttrs":    ToggleAttrs,
		"expandAttrs":    ExpandableAttrs,
		"busyAttrs":      BusyAttrs,
		"decorative":     DecorativeAttrs,
		"accessibleName": AccessibleName,
		"idList":         IDList,
		"requiredMarker": func() string { return b.RequiredMarker(locale) },
		"optionalMarker": func() string { return b.OptionalMarker(locale) },
	}
}

// FuncMap returns the template functions that take the language as their
// first argument, matching the {{t .Lang "key"}} form used throughout AI.md
// PART 31. Use it when one template renders content for more than one locale,
// or when the page model already carries .Lang.
//
// Merge exactly one of Funcs and FuncMap into a template.
func (b *Bundle) FuncMap() template.FuncMap {
	return template.FuncMap{
		"t":  b.T,
		"tf": b.T,
		"tp": b.N,

		"lang":      b.Resolve,
		"dir":       b.Dir,
		"isRTL":     b.IsRTL,
		"langs":     b.Available,
		"langAttrs": b.LangAttrs,

		"num":      b.FormatNumber,
		"decimal":  b.FormatDecimal,
		"count":    b.FormatInt,
		"pct":      b.FormatPercent,
		"money":    b.FormatCurrency,
		"date":     b.FormatDate,
		"clock":    b.FormatTime,
		"datetime": b.FormatDateTime,

		"skipLinks":      b.SkipLinks,
		"navAttrs":       b.NavAttrs,
		"mainAttrs":      MainAttrs,
		"bannerAttrs":    BannerAttrs,
		"footerAttrs":    ContentInfoAttrs,
		"asideAttrs":     ComplementaryAttrs,
		"landmarkAttrs":  LandmarkAttrs,
		"attrs":          Attrs,
		"ariaAttrs":      AriaAttrs,
		"liveRegion":     LiveRegionAttrs,
		"dialogAttrs":    DialogAttrs,
		"fieldAttrs":     FieldAttrs,
		"errorRegion":    ErrorRegionAttrs,
		"toggleAttrs":    ToggleAttrs,
		"expandAttrs":    ExpandableAttrs,
		"busyAttrs":      BusyAttrs,
		"decorative":     DecorativeAttrs,
		"accessibleName": AccessibleName,
		"idList":         IDList,
		"requiredMarker": b.RequiredMarker,
		"optionalMarker": b.OptionalMarker,
	}
}
