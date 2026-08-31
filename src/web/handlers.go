package web

import (
	"net/http"
	"net/mail"
	"strings"
)

// ContactMessage is a validated submission from the public contact form.
type ContactMessage struct {
	Name    string
	Email   string
	Subject string
	Body    string
	// RemoteAddr is recorded so abuse reports can be traced by the operator.
	RemoteAddr string
}

// ContactSubmitter delivers a contact message. The mail transport lives outside
// this package; the frontend only collects, validates and reports.
type ContactSubmitter func(ContactMessage) error

// SetContactSubmitter installs the delivery function for the contact form.
// Until one is installed the form validates input and tells the visitor that
// delivery is not configured, rather than silently discarding the message.
func (r *Renderer) SetContactSubmitter(submit ContactSubmitter) {
	r.submitContact = submit
}

// Handlers returns the public page routes this package owns, keyed by the
// pattern the router should register them under. The admin panel is never
// included: no public route may reveal the admin path.
func (r *Renderer) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"/server":         http.HandlerFunc(r.handleServerRoot),
		"/server/about":   http.HandlerFunc(r.handleAbout),
		"/server/privacy": http.HandlerFunc(r.handlePrivacy),
		"/server/contact": http.HandlerFunc(r.handleContact),
		"/server/help":    http.HandlerFunc(r.handleHelp),
		"/server/terms":   http.HandlerFunc(r.handleTerms),
		"/server/theme":   http.HandlerFunc(r.handleTheme),
		"/server/consent": http.HandlerFunc(r.handleConsent),
		"/server/ccpa":    http.HandlerFunc(r.handleCCPA),
		"/offline.html":   http.HandlerFunc(r.handleOffline),
		"/manifest.json":  r.static,
		"/sw.js":          r.static,
		staticPrefix:      r.static,
	}
}

// handleServerRoot sends /server to the about page, which is the entry point
// for the public information section.
func (r *Renderer) handleServerRoot(w http.ResponseWriter, req *http.Request) {
	http.Redirect(w, req, "/server/about", http.StatusMovedPermanently)
}

// handleAbout renders the about page.
func (r *Renderer) handleAbout(w http.ResponseWriter, req *http.Request) {
	if !r.requireGet(w, req) {
		return
	}
	r.renderPage(w, req, "about", nil)
}

// helpData carries the optional overlay-network addresses shown on the help
// page. An address is only present when its overlay is enabled, running and
// publishing, so an empty value hides the whole section.
type helpData struct {
	TorAddress string
	I2PAddress string
}

// SetOverlayAddresses publishes the Tor onion and I2P addresses shown in the
// help page access sections. Pass an empty string to hide a section.
func (r *Renderer) SetOverlayAddresses(onion, i2p string) {
	r.onionAddress = onion
	r.i2pAddress = i2p
}

// handleHelp renders the help page.
func (r *Renderer) handleHelp(w http.ResponseWriter, req *http.Request) {
	if !r.requireGet(w, req) {
		return
	}
	r.renderPage(w, req, "help", helpData{
		TorAddress: r.onionAddress,
		I2PAddress: r.i2pAddress,
	})
}

// handleTerms renders the terms of service page.
func (r *Renderer) handleTerms(w http.ResponseWriter, req *http.Request) {
	if !r.requireGet(w, req) {
		return
	}
	r.renderPage(w, req, "terms", nil)
}

// privacyData is the page data for the privacy policy.
type privacyData struct {
	Consent      ConsentState
	CCPAOptedOut bool
}

// handlePrivacy renders the privacy policy with the visitor's current cookie
// choices reflected in the inline preferences form.
func (r *Renderer) handlePrivacy(w http.ResponseWriter, req *http.Request) {
	if !r.requireGet(w, req) {
		return
	}
	r.renderPage(w, req, "privacy", privacyData{
		Consent:      ConsentFromRequest(req),
		CCPAOptedOut: CCPAOptedOut(req),
	})
}

// handleOffline renders the page the service worker shows when a navigation
// fails while the device is offline.
func (r *Renderer) handleOffline(w http.ResponseWriter, req *http.Request) {
	if !r.requireGet(w, req) {
		return
	}
	r.renderPage(w, req, "offline", nil)
}

// contactData carries the state of the contact form across a failed submission
// so the visitor never loses what they typed.
type contactData struct {
	Values   map[string]string
	Errors   map[string]string
	Question string
	Token    string
}

// handleContact renders the contact form and processes submissions. The form
// is a plain POST: it works with JavaScript disabled.
func (r *Renderer) handleContact(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		question, token := newCaptcha()
		r.renderPage(w, req, "contact", contactData{
			Values:   map[string]string{},
			Errors:   map[string]string{},
			Question: question,
			Token:    token,
		})
	case http.MethodPost:
		r.submitContactForm(w, req)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		r.RenderError(w, req, http.StatusMethodNotAllowed, "", "Use GET to view the contact form or POST to submit it.")
	}
}

// submitContactForm validates a contact submission and hands it to the
// configured submitter.
func (r *Renderer) submitContactForm(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		r.RenderError(w, req, http.StatusBadRequest, "", "The submitted form could not be read.")
		return
	}
	if !ValidateCSRF(req) {
		r.RenderError(w, req, http.StatusForbidden, "csrf_invalid", "Your session expired before the message was sent. Reload the page and submit it again.")
		return
	}

	values := map[string]string{
		"name":    strings.TrimSpace(req.PostFormValue("name")),
		"email":   strings.TrimSpace(req.PostFormValue("email")),
		"subject": strings.TrimSpace(req.PostFormValue("subject")),
		"message": strings.TrimSpace(req.PostFormValue("message")),
	}
	errs := validateContact(values)

	// The honeypot field is invisible to people and irresistible to bots.
	if strings.TrimSpace(req.PostFormValue("website")) != "" {
		errs["captcha"] = "This submission was rejected as automated."
	} else if !verifyCaptcha(req.PostFormValue("captcha_token"), req.PostFormValue("captcha_answer")) {
		errs["captcha"] = "That answer was not correct. Try the new question below."
	}

	if len(errs) > 0 {
		question, token := newCaptcha()
		w.Header().Set("Cache-Control", "no-store")
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusUnprocessableEntity}
		if err := r.Render(rec, req, "contact", contactData{
			Values:   values,
			Errors:   errs,
			Question: question,
			Token:    token,
		}); err != nil {
			r.RenderError(w, req, http.StatusInternalServerError, "", "The contact form could not be displayed.")
		}
		return
	}

	if r.submitContact == nil {
		AddFlash(w, req, "error", "This instance has no outgoing mail configured, so the contact form cannot deliver your message. Contact the administrator directly.")
		http.Redirect(w, req, "/server/contact", http.StatusSeeOther)
		return
	}

	err := r.submitContact(ContactMessage{
		Name:       values["name"],
		Email:      values["email"],
		Subject:    values["subject"],
		Body:       values["message"],
		RemoteAddr: req.RemoteAddr,
	})
	if err != nil {
		AddFlash(w, req, "error", "Your message could not be sent. Try again in a few minutes.")
		http.Redirect(w, req, "/server/contact", http.StatusSeeOther)
		return
	}

	AddFlash(w, req, "success", "Thank you for your message. We will respond to the address you provided.")
	http.Redirect(w, req, "/server/contact", http.StatusSeeOther)
}

// validateContact returns a field-keyed map of validation messages.
func validateContact(values map[string]string) map[string]string {
	errs := map[string]string{}
	if values["name"] == "" {
		errs["name"] = "Enter the name we should address the reply to."
	}
	if values["email"] == "" {
		errs["email"] = "Enter an email address so we can reply."
	} else if _, err := mail.ParseAddress(values["email"]); err != nil {
		errs["email"] = "That does not look like a valid email address."
	}
	if values["subject"] == "" {
		errs["subject"] = "Enter a short subject."
	}
	if len(values["message"]) < 10 {
		errs["message"] = "Enter a message of at least 10 characters."
	}
	if len(values["message"]) > 5000 {
		errs["message"] = "Messages are limited to 5000 characters."
	}
	return errs
}

// handleTheme persists a theme choice submitted by the theme toggle form and
// returns the visitor to the page they came from.
func (r *Renderer) handleTheme(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		r.RenderError(w, req, http.StatusMethodNotAllowed, "", "The theme is changed by submitting the theme form.")
		return
	}
	if err := req.ParseForm(); err != nil {
		r.RenderError(w, req, http.StatusBadRequest, "", "The theme selection could not be read.")
		return
	}
	if !ValidateCSRF(req) {
		r.RenderError(w, req, http.StatusForbidden, "csrf_invalid", "Your session expired. Reload the page and try again.")
		return
	}
	SetThemeCookie(w, req.PostFormValue("theme"))
	http.Redirect(w, req, safeReferrer(req), http.StatusSeeOther)
}

// handleConsent records a cookie consent choice and redirects back. The banner
// is fully functional without JavaScript because of this handler.
func (r *Renderer) handleConsent(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		r.RenderError(w, req, http.StatusMethodNotAllowed, "", "Cookie choices are submitted with the consent form.")
		return
	}
	if err := req.ParseForm(); err != nil {
		r.RenderError(w, req, http.StatusBadRequest, "", "The consent choice could not be read.")
		return
	}

	state := ConsentState{Essential: true}
	switch req.PostFormValue("choice") {
	case "accept":
		state.Preferences = true
		state.Analytics = true
	case "save":
		state.Preferences = req.PostFormValue("preferences") == "on"
		state.Analytics = req.PostFormValue("analytics") == "on"
	case "decline":
		// Essential cookies only; nothing else to enable.
	default:
		r.RenderError(w, req, http.StatusBadRequest, "invalid_choice", "That cookie choice is not recognised.")
		return
	}

	SetConsentCookie(w, req, state)
	http.Redirect(w, req, safeReferrer(req), http.StatusSeeOther)
}

// handleCCPA records or clears a "do not sell my personal information" choice.
func (r *Renderer) handleCCPA(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		r.RenderError(w, req, http.StatusMethodNotAllowed, "", "The opt-out is submitted with the privacy form.")
		return
	}
	if err := req.ParseForm(); err != nil {
		r.RenderError(w, req, http.StatusBadRequest, "", "The opt-out choice could not be read.")
		return
	}

	switch req.PostFormValue("choice") {
	case "opt-out":
		SetCCPAOptOut(w, req, true)
		SetConsentCookie(w, req, ConsentState{Essential: true})
		AddFlash(w, req, "success", "You have opted out of the sale of your personal information.")
	case "opt-in":
		SetCCPAOptOut(w, req, false)
		AddFlash(w, req, "info", "You have opted back in. You can opt out again at any time.")
	default:
		r.RenderError(w, req, http.StatusBadRequest, "invalid_choice", "That opt-out choice is not recognised.")
		return
	}

	http.Redirect(w, req, "/server/privacy#ccpa-opt-out", http.StatusSeeOther)
}

// requireGet rejects non-read methods on a read-only page.
func (r *Renderer) requireGet(w http.ResponseWriter, req *http.Request) bool {
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	r.RenderError(w, req, http.StatusMethodNotAllowed, "", "This page can only be read.")
	return false
}

// renderPage renders a page and falls back to the error representation when
// the template fails, so a request never ends without a response.
func (r *Renderer) renderPage(w http.ResponseWriter, req *http.Request, name string, data any) {
	if err := r.Render(w, req, name, data); err != nil {
		r.RenderError(w, req, http.StatusInternalServerError, "", "This page could not be rendered.")
	}
}
