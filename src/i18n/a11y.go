package i18n

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"
)

// WCAG 2.1 AA thresholds from AI.md PART 31. Contrast ratios are minimums;
// MinTouchTargetPx is the minimum edge length of an interactive target.
const (
	ContrastNormalTextAA = 4.5
	ContrastLargeTextAA  = 3.0
	ContrastComponentAA  = 3.0
	MinTouchTargetPx     = 44
)

// Live region politeness values.
const (
	PolitenessPolite    = "polite"
	PolitenessAssertive = "assertive"
)

// SkipLink is one bypass-blocks link, rendered as the first focusable
// elements on every page so keyboard users can jump past repeated navigation.
type SkipLink struct {
	Href  string
	Label string
}

// skipLinkTargets pairs each skip link's fragment with the message key that
// labels it. Order is the tab order.
var skipLinkTargets = []struct {
	href string
	key  string
}{
	{href: "#main-content", key: "a11y.skip_to_content"},
	{href: "#navigation", key: "a11y.skip_to_navigation"},
}

// SkipLinks returns the translated skip links for a locale. The fragments
// match the landmark ids produced by MainAttrs and NavAttrs.
func (b *Bundle) SkipLinks(locale string) []SkipLink {
	out := make([]SkipLink, 0, len(skipLinkTargets))

	for _, target := range skipLinkTargets {
		out = append(out, SkipLink{
			Href:  target.href,
			Label: b.T(locale, target.key),
		})
	}

	return out
}

// Attrs renders alternating name/value pairs as an HTML attribute string.
//
// Attribute names are restricted to lowercase letters, digits and hyphens and
// invalid names are dropped, so a caller can never inject markup through a
// name. Values are HTML-escaped. Pairs with an empty value are omitted, which
// keeps optional attributes such as aria-describedby off the element entirely
// rather than emitting a meaningless empty reference. A trailing unpaired
// argument is ignored instead of panicking.
func Attrs(pairs ...string) template.HTMLAttr {
	var parts []string

	for i := 0; i+1 < len(pairs); i += 2 {
		name, value := pairs[i], pairs[i+1]
		if !validAttrName(name) || value == "" {
			continue
		}

		parts = append(parts, name+`="`+html.EscapeString(value)+`"`)
	}

	return template.HTMLAttr(strings.Join(parts, " "))
}

// AriaAttrs renders alternating name/value pairs as aria-* attributes. Names
// that already carry the "aria-" prefix are left alone, so both "label" and
// "aria-label" are accepted.
func AriaAttrs(pairs ...string) template.HTMLAttr {
	normalized := make([]string, 0, len(pairs))

	for i := 0; i+1 < len(pairs); i += 2 {
		name := pairs[i]
		if !strings.HasPrefix(name, "aria-") {
			name = "aria-" + name
		}

		normalized = append(normalized, name, pairs[i+1])
	}

	return Attrs(normalized...)
}

// validAttrName reports whether name is a safe HTML attribute name.
func validAttrName(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}

	return true
}

// AccessibleName joins the fragments of an accessible name into a single
// string, dropping empty fragments and collapsing runs of whitespace. It is
// the text that belongs in an aria-label when a control's visible text alone
// would be ambiguous, for example "Delete" plus the row's subject.
func AccessibleName(parts ...string) string {
	fields := make([]string, 0, len(parts))

	for _, part := range parts {
		if trimmed := strings.Join(strings.Fields(part), " "); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}

	return strings.Join(fields, " ")
}

// IDList joins element ids for aria-describedby and aria-labelledby, dropping
// empties so a missing hint never leaves a dangling reference.
func IDList(ids ...string) string {
	kept := make([]string, 0, len(ids))

	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}

	return strings.Join(kept, " ")
}

// LandmarkAttrs renders the role and accessible label of a landmark region.
// Landmarks of the same role must carry distinct labels, which is why label is
// explicit rather than defaulted.
func LandmarkAttrs(role, label string) template.HTMLAttr {
	return Attrs("role", role, "aria-label", label)
}

// BannerAttrs renders the site header landmark.
func BannerAttrs() template.HTMLAttr {
	return Attrs("role", "banner")
}

// NavAttrs renders a navigation landmark carrying the id that the skip links
// target.
func (b *Bundle) NavAttrs(locale string) template.HTMLAttr {
	return Attrs("id", "navigation", "role", "navigation", "aria-label", b.T(locale, "nav.main_navigation"))
}

// MainAttrs renders the main landmark carrying the id that the skip links
// target. tabindex="-1" lets the skip link move focus into the region.
func MainAttrs() template.HTMLAttr {
	return Attrs("id", "main-content", "role", "main", "tabindex", "-1")
}

// ComplementaryAttrs renders a supporting landmark such as a sidebar.
func ComplementaryAttrs(label string) template.HTMLAttr {
	return Attrs("role", "complementary", "aria-label", label)
}

// ContentInfoAttrs renders the site footer landmark.
func ContentInfoAttrs() template.HTMLAttr {
	return Attrs("role", "contentinfo")
}

// LiveRegionAttrs renders a live region.
//
// Polite regions announce after the screen reader finishes its current
// utterance and use role="status"; assertive regions interrupt and use
// role="alert", which is reserved for errors. An unrecognised politeness
// value degrades to polite rather than shouting at the user.
func LiveRegionAttrs(politeness string, atomic bool) template.HTMLAttr {
	role := "status"

	if politeness != PolitenessAssertive {
		politeness = PolitenessPolite
	} else {
		role = "alert"
	}

	return Attrs("role", role, "aria-live", politeness, "aria-atomic", strconv.FormatBool(atomic))
}

// DialogAttrs renders a modal dialog. labelledBy must reference the dialog's
// heading; describedBy is optional and is omitted when empty.
func DialogAttrs(labelledBy, describedBy string) template.HTMLAttr {
	return Attrs(
		"role", "dialog",
		"aria-modal", "true",
		"aria-labelledby", labelledBy,
		"aria-describedby", describedBy,
	)
}

// Field describes a form control for the purposes of its ARIA wiring.
type Field struct {
	// ID is the control's id, which its <label for> must match.
	ID string
	// Required marks the control as mandatory.
	Required bool
	// Invalid marks the control as currently failing validation.
	Invalid bool
	// HintID references static help text describing the control.
	HintID string
	// ErrorID references the live region holding this control's error text.
	ErrorID string
	// Label overrides the visible label when the control has none.
	Label string
}

// FieldAttrs renders the ARIA wiring for a form control: its id, its required
// and invalid state, and the hint and error text that describe it. The error
// id is listed last so a screen reader reads the hint before the failure.
func FieldAttrs(f Field) template.HTMLAttr {
	invalid := ""
	if f.Invalid {
		invalid = "true"
	}

	required := ""
	if f.Required {
		required = "true"
	}

	return Attrs(
		"id", f.ID,
		"aria-required", required,
		"aria-invalid", invalid,
		"aria-describedby", IDList(f.HintID, f.ErrorID),
		"aria-label", f.Label,
	)
}

// ErrorRegionAttrs renders the per-field error container: an assertive-free
// polite alert that is announced when its text changes.
func ErrorRegionAttrs(id string) template.HTMLAttr {
	return Attrs("id", id, "role", "alert", "aria-live", PolitenessPolite)
}

// ToggleAttrs renders a two-state button such as a show/hide password control.
func ToggleAttrs(pressed bool, label string) template.HTMLAttr {
	return Attrs("type", "button", "aria-pressed", strconv.FormatBool(pressed), "aria-label", label)
}

// ExpandableAttrs renders a disclosure trigger such as an accordion header.
func ExpandableAttrs(expanded bool, controls string) template.HTMLAttr {
	return Attrs("aria-expanded", strconv.FormatBool(expanded), "aria-controls", controls)
}

// BusyAttrs renders the busy state of a control or region, used on submit
// buttons and on containers that are loading.
func BusyAttrs(busy bool) template.HTMLAttr {
	return Attrs("aria-busy", strconv.FormatBool(busy))
}

// DecorativeAttrs hides purely decorative content, such as an icon that sits
// beside its own text label, from assistive technology.
func DecorativeAttrs() template.HTMLAttr {
	return Attrs("aria-hidden", "true")
}

// LangAttrs renders the document language and writing direction for the
// <html> element of a page rendered in the given locale.
func (b *Bundle) LangAttrs(locale string) template.HTMLAttr {
	return Attrs("lang", b.Resolve(locale), "dir", b.Dir(locale))
}

// RequiredMarker returns the screen-reader-only text that accompanies the
// visual asterisk on a required field, so the requirement is never conveyed
// by a symbol alone.
func (b *Bundle) RequiredMarker(locale string) string {
	return b.T(locale, "form.required_field")
}

// OptionalMarker returns the text marking an optional field.
func (b *Bundle) OptionalMarker(locale string) string {
	return b.T(locale, "form.optional_field")
}

// ContrastRatio returns the WCAG relative contrast ratio between two colors,
// each given as a #rgb or #rrggbb hex string. The result ranges from 1 (no
// contrast) to 21 (black against white) and is order-independent.
func ContrastRatio(fg, bg string) (float64, error) {
	lf, err := relativeLuminance(fg)
	if err != nil {
		return 0, err
	}

	lb, err := relativeLuminance(bg)
	if err != nil {
		return 0, err
	}

	lighter, darker := lf, lb
	if darker > lighter {
		lighter, darker = darker, lighter
	}

	return (lighter + 0.05) / (darker + 0.05), nil
}

// MeetsAA reports whether a contrast ratio satisfies WCAG 2.1 AA for text.
// largeText covers 18pt regular or 14pt bold and above.
func MeetsAA(ratio float64, largeText bool) bool {
	if largeText {
		return ratio >= ContrastLargeTextAA
	}

	return ratio >= ContrastNormalTextAA
}

// MeetsAAComponent reports whether a contrast ratio satisfies WCAG 2.1 AA for
// non-text content such as borders, icons and focus indicators.
func MeetsAAComponent(ratio float64) bool {
	return ratio >= ContrastComponentAA
}

// relativeLuminance computes the WCAG relative luminance of a hex color.
func relativeLuminance(color string) (float64, error) {
	r, g, b, err := parseHexColor(color)
	if err != nil {
		return 0, err
	}

	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b), nil
}

// linearize converts an 8-bit sRGB channel to its linear-light value.
func linearize(channel uint8) float64 {
	c := float64(channel) / 255

	if c <= 0.04045 {
		return c / 12.92
	}

	// WCAG defines the transfer function as ((c+0.055)/1.055)^2.4.
	return math.Pow((c+0.055)/1.055, 2.4)
}

// parseHexColor decodes a #rgb or #rrggbb color into its channels.
func parseHexColor(color string) (uint8, uint8, uint8, error) {
	s := strings.TrimSpace(color)
	s = strings.TrimPrefix(s, "#")

	switch len(s) {
	case 3:
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 6:
	default:
		return 0, 0, 0, fmt.Errorf("i18n: %q is not a #rgb or #rrggbb color", color)
	}

	value, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("i18n: %q is not a hex color: %w", color, err)
	}

	return uint8(value >> 16), uint8(value >> 8 & 0xff), uint8(value & 0xff), nil
}

// SortedIDs returns ids in a stable order, used when a template assembles an
// aria-describedby list from an unordered set.
func SortedIDs(ids map[string]bool) []string {
	out := make([]string, 0, len(ids))

	for id, include := range ids {
		if include && strings.TrimSpace(id) != "" {
			out = append(out, id)
		}
	}

	sort.Strings(out)

	return out
}
