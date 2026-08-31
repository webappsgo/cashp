package billing

import (
	"net/http"

	"github.com/webappsgo/cashp/src/api"
)

// tenantPageData assembles the shell every tenant billing page shares.
func (s *Service) tenantPageData(r *http.Request, id Identity) PageData {
	return PageData{
		CSRFToken: id.CSRFToken,
		BasePath:  tenantBasePath(id.TenantSlug),
		Identity:  id,
		Enabled:   s.Enabled(r.Context()),
	}
}

// handleTenantOverview shows a tenant their plan, their usage against it and
// what they owe. It is the one page a tenant needs, and it answers in JSON for
// the API and in HTML for the browser from the same code.
func (s *Service) handleTenantOverview(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, false)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	summary, err := s.TenantDashboard(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusOK, summary)
		return
	}
	methods, err := s.ListPaymentMethods(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	invoices, err := s.ListInvoices(r.Context(), id.TenantID, 10, 0)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	data := s.tenantPageData(r, id)
	data.Title = "Billing"
	data.Heading = "Billing"
	data.Description = "Your plan, your usage and your invoices."
	data.Summary = summary
	data.Account = summary.Account
	data.Quotas = summary.Quotas
	data.Methods = methods
	data.Invoices = invoices
	s.render(w, r, http.StatusOK, "tenant_overview", data)
}

// handleTenantPlans lists the plans a tenant may move to, with what the move
// costs today shown before they commit to it.
func (s *Service) handleTenantPlans(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, false)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	plans, err := s.ListPlans(r.Context(), true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WritePage(w, r, plans, len(plans))
		return
	}
	summary, err := s.TenantDashboard(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	data := s.tenantPageData(r, id)
	data.Title = "Plans"
	data.Heading = "Choose a plan"
	data.Description = "Every feature is available on every plan. A plan only sets how much you may use."
	data.Plans = plans
	data.Summary = summary
	if target := r.URL.Query().Get("preview"); target != "" {
		preview, pErr := s.PreviewPlanChange(r.Context(), id.TenantID, target)
		if pErr != nil {
			data.Error = pErr.Error()
		} else {
			data.Preview = preview
		}
	}
	s.render(w, r, http.StatusOK, "tenant_plans", data)
}

// handleTenantSubscribe starts a subscription or moves an existing one to
// another plan. Both cases arrive here because a tenant thinks of them as the
// same action: choosing a plan.
func (s *Service) handleTenantSubscribe(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	planID := formValue(r, "plan_id")
	if planID == "" {
		api.WriteError(w, r, ErrValidation("Choose a plan first."))
		return
	}
	actor, ip := id.actor(), clientIP(r)

	sub, err := s.ActiveSubscription(r.Context(), id.TenantID)
	switch {
	case err == nil:
		sub, err = s.ChangePlan(r.Context(), id.TenantID, planID, actor, ip)
	case isNotFound(err):
		sub, err = s.Subscribe(r.Context(), id.TenantID, planID, actor, ip)
	}
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusOK, sub)
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug), "Your plan has been updated.")
}

// handleTenantCancel cancels a subscription. Cancelling is one form post with
// no interception, no retention offer and no extra confirmation step, and it
// takes effect at the end of the period already paid for unless the tenant
// asks for it to be immediate.
func (s *Service) handleTenantCancel(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	sub, err := s.ActiveSubscription(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	sub, err = s.Cancel(r.Context(), id.TenantID, sub.ID,
		formBool(r, "immediate"), formValue(r, "reason"), id.actor(), clientIP(r))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusOK, sub)
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug), "Your subscription has been cancelled.")
}

// handleTenantResume undoes a cancellation that has not taken effect yet.
func (s *Service) handleTenantResume(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	sub, err := s.ActiveSubscription(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	sub, err = s.Resume(r.Context(), id.TenantID, sub.ID, id.actor(), clientIP(r))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusOK, sub)
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug), "Your subscription has been resumed.")
}

// handleTenantAccount saves the billing profile an invoice is addressed to.
func (s *Service) handleTenantAccount(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	in := AccountUpdate{
		Currency:     formValue(r, "currency"),
		BillingEmail: formValue(r, "billing_email"),
		LegalName:    formValue(r, "legal_name"),
		AddressLine1: formValue(r, "address_line1"),
		AddressLine2: formValue(r, "address_line2"),
		City:         formValue(r, "city"),
		Region:       formValue(r, "region"),
		PostalCode:   formValue(r, "postal_code"),
		Country:      formValue(r, "country"),
		TaxID:        formValue(r, "tax_id"),
		IsBusiness:   formBool(r, "is_business"),
	}
	account, err := s.UpdateAccount(r.Context(), id.TenantID, in, id.actor(), clientIP(r))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusOK, account)
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug), "Your billing details have been saved.")
}

// handleTenantInvoices lists a tenant's invoices.
func (s *Service) handleTenantInvoices(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, false)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	page, limit := api.Paginate(r)
	invoices, err := s.ListInvoices(r.Context(), id.TenantID, limit, (page-1)*limit)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WritePage(w, r, invoices, len(invoices))
		return
	}
	notes, err := s.ListCreditNotes(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	data := s.tenantPageData(r, id)
	data.Title = "Invoices"
	data.Heading = "Invoices"
	data.Description = "Every invoice raised on this organization, and every credit note against them."
	data.Invoices = invoices
	data.CreditNotes = notes
	s.render(w, r, http.StatusOK, "tenant_invoices", data)
}

// handleTenantInvoice shows one invoice with its lines.
func (s *Service) handleTenantInvoice(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, false)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	invoiceID := r.PathValue("id")
	invoice, err := s.Invoice(r.Context(), id.TenantID, invoiceID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	lines, err := s.InvoiceLines(r.Context(), id.TenantID, invoiceID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	invoice.Lines = lines
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusOK, invoice)
		return
	}
	data := s.tenantPageData(r, id)
	data.Title = "Invoice " + invoice.Number
	data.Heading = "Invoice " + invoice.Number
	data.Invoice = invoice
	data.Lines = lines
	s.render(w, r, http.StatusOK, "tenant_invoice", data)
}

// handleTenantPayInvoice charges an outstanding invoice on demand.
func (s *Service) handleTenantPayInvoice(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	invoiceID := r.PathValue("id")
	// The idempotency key is derived from the invoice rather than generated,
	// so a double-submitted form settles the same charge twice at most once.
	key := "manual:" + invoiceID
	attempt, err := s.ChargeInvoice(r.Context(), id.TenantID, invoiceID, key, id.actor(), clientIP(r))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusOK, attempt)
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug)+"/invoices/"+invoiceID, "The payment has been attempted.")
}

// handleTenantUsage reports usage against the plan's quotas.
func (s *Service) handleTenantUsage(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, false)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	quotas, err := s.QuotaStatuses(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WritePage(w, r, quotas, len(quotas))
		return
	}
	usage, err := s.ListUsage(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	data := s.tenantPageData(r, id)
	data.Title = "Usage"
	data.Heading = "Usage"
	data.Description = "What this organization is using against what its plan allows."
	data.Quotas = quotas
	data.Summary.TenantID = id.TenantID
	_ = usage
	s.render(w, r, http.StatusOK, "tenant_usage", data)
}

// handleTenantMethods lists stored payment methods.
func (s *Service) handleTenantMethods(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, false)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	methods, err := s.ListPaymentMethods(r.Context(), id.TenantID)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WritePage(w, r, methods, len(methods))
		return
	}
	providers, err := s.EnabledProviders(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	data := s.tenantPageData(r, id)
	data.Title = "Payment methods"
	data.Heading = "Payment methods"
	data.Description = "Card details are held by the payment provider. This server stores only the brand, the last four digits and the expiry."
	data.Methods = methods
	data.Categories = groupProviders(providersToViews(providers))
	s.render(w, r, http.StatusOK, "tenant_methods", data)
}

// providersToViews reduces registry rows to the small shape the payment
// method form needs. It never carries a credential.
func providersToViews(records []ProviderRecord) []ProviderView {
	out := make([]ProviderView, 0, len(records))
	for _, rec := range records {
		out = append(out, ProviderView{
			Name:        rec.Name,
			DisplayName: rec.DisplayName,
			Category:    rec.Category,
			Enabled:     rec.Enabled,
			TestMode:    rec.TestMode,
			Priority:    rec.Priority,
		})
	}
	return out
}

// handleTenantAddMethod stores a payment method. The token arrives from the
// provider's own browser-side form, so no card number, security code or full
// account number ever reaches this server.
func (s *Service) handleTenantAddMethod(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	method, err := s.AddPaymentMethod(r.Context(), id.TenantID,
		formValue(r, "provider"), formValue(r, "token"), formValue(r, "holder_name"),
		id.actor(), clientIP(r))
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteItem(w, r, http.StatusCreated, method)
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug)+"/payment-methods", "The payment method has been added.")
}

// handleTenantRemoveMethod removes a stored payment method.
func (s *Service) handleTenantRemoveMethod(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if err := s.RemovePaymentMethod(r.Context(), id.TenantID, r.PathValue("id"), id.actor(), clientIP(r)); err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteSuccess(w, r, http.StatusOK, map[string]any{"removed": true})
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug)+"/payment-methods", "The payment method has been removed.")
}

// handleTenantDefaultMethod chooses which stored method is charged first.
func (s *Service) handleTenantDefaultMethod(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, true)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	if err := s.SetDefaultMethod(r.Context(), id.TenantID, r.PathValue("id")); err != nil {
		api.WriteError(w, r, err)
		return
	}
	if wantsJSON(r) {
		api.WriteSuccess(w, r, http.StatusOK, map[string]any{"default_method_id": r.PathValue("id")})
		return
	}
	redirect(w, r, tenantBasePath(id.TenantSlug)+"/payment-methods", "The default payment method has been changed.")
}

// handleTenantExport hands a tenant their whole billing record. It is offered
// to every tenant, in an open format, so nothing here is a one-way door.
func (s *Service) handleTenantExport(w http.ResponseWriter, r *http.Request) {
	id, err := s.tenantCaller(r, false)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	format := formValue(r, "format")
	if format == "" {
		format = r.URL.Query().Get("format")
	}
	actor, ip := id.actor(), clientIP(r)

	var body []byte
	var contentType string
	if format == ExportCSV {
		body, err = s.ExportTenantCSV(r.Context(), id.TenantID, actor, ip)
		contentType = "text/csv; charset=utf-8"
	} else {
		format = ExportJSON
		body, err = s.ExportTenantJSON(r.Context(), id.TenantID, actor, ip)
		contentType = "application/json; charset=utf-8"
	}
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+ExportFilename(id.TenantID, format)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, wErr := w.Write(body); wErr != nil {
		return
	}
}
