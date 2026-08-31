package hosting

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"strings"
	"text/template"

	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// Generated mail map file names. They are referenced from the host's Postfix,
// Dovecot, and OpenDKIM configuration; cashp owns their content only.
const (
	fileVirtualDomains   = "virtual_domains"
	fileVirtualMailboxes = "virtual_mailboxes"
	fileVirtualAliases   = "virtual_aliases"
	fileDovecotUsers     = "dovecot-users"
	fileDKIMKeyTable     = "dkim-keytable"
	fileDKIMSigningTable = "dkim-signingtable"
	fileDKIMTrustedHosts = "dkim-trustedhosts"
)

// dkimSelector is the DKIM selector cashp publishes under.
const dkimSelector = "cashp"

// dkimKeyBits is the RSA size of a generated DKIM signing key.
const dkimKeyBits = 2048

// minMailPasswordLen is the shortest accepted mailbox password.
const minMailPasswordLen = 12

// defaultMailboxQuotaMB applies when a mailbox is created without a quota.
const defaultMailboxQuotaMB = 2048

// Service names used in rollback errors and audit entries.
const (
	postfixService = "mail transport"
	dovecotService = "mail delivery"
	dkimService    = "mail signing"
)

// spfPolicy is the SPF record published for a mail domain.
const spfPolicy = "v=spf1 mx -all"

// CreateMailDomain enables mail hosting for a verified domain: it generates an
// OpenDKIM signing key, rebuilds the mail maps, and publishes the MX, SPF,
// DKIM, and DMARC records when the tenant also hosts the zone here.
func (s *Service) CreateMailDomain(ctx context.Context, tenantID, rawDomain string) (MailDomain, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return MailDomain{}, err
	}
	domain, err := ValidateDomain(rawDomain)
	if err != nil {
		return MailDomain{}, err
	}
	if err = s.requireOwnedDomain(ctx, tenantID, domain); err != nil {
		return MailDomain{}, err
	}
	if s.mailHostname == "" {
		return MailDomain{}, apperr.New(apperr.CodeUnavailable, 503, "the mail hostname is not configured")
	}
	if _, err = s.store.MailDomainByName(ctx, domain); err == nil {
		return MailDomain{}, apperr.New(apperr.CodeConflict, 409, "mail is already hosted for that domain")
	} else if !apperr.Is(err, apperr.CodeNotFound) {
		return MailDomain{}, err
	}

	privatePEM, publicKey, err := generateDKIMKey()
	if err != nil {
		return MailDomain{}, err
	}
	sealed, err := security.Encrypt(s.key, privatePEM)
	if err != nil {
		return MailDomain{}, internalErr(err, "the signing key could not be protected")
	}

	now := s.now().UTC()
	record := MailDomain{
		ID:           s.newID(),
		TenantID:     tenantID,
		Domain:       domain,
		DKIMSelector: dkimSelector,
		DKIMPrivate:  sealed,
		DKIMPublic:   publicKey,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err = s.store.CreateMailDomain(ctx, record); err != nil {
		return MailDomain{}, err
	}
	if err = s.writeDKIMKey(record, privatePEM); err != nil {
		return MailDomain{}, err
	}
	if err = s.publishMail(ctx); err != nil {
		return MailDomain{}, err
	}
	if err = s.publishMailDNS(ctx, record); err != nil {
		return MailDomain{}, err
	}
	s.audit(ctx, "hosting.maildomain.create", tenantID, record.ID, "domain", domain)
	return sanitizeMailDomain(record), nil
}

// GetMailDomain returns one mail domain owned by the tenant, without its
// private key material.
func (s *Service) GetMailDomain(ctx context.Context, tenantID, domainID string) (MailDomain, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return MailDomain{}, err
	}
	if err := ValidateID("mail domain", domainID); err != nil {
		return MailDomain{}, err
	}
	record, err := s.store.GetMailDomain(ctx, tenantID, domainID)
	if err != nil {
		return MailDomain{}, err
	}
	return sanitizeMailDomain(record), nil
}

// ListMailDomains returns every mail domain owned by the tenant.
func (s *Service) ListMailDomains(ctx context.Context, tenantID string) ([]MailDomain, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return nil, err
	}
	records, err := s.store.ListMailDomains(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for i := range records {
		records[i] = sanitizeMailDomain(records[i])
	}
	return records, nil
}

// DeleteMailDomain removes a mail domain, its mailboxes, its aliases, its
// signing key, and its managed DNS records.
func (s *Service) DeleteMailDomain(ctx context.Context, tenantID, domainID string, confirm bool) error {
	if err := requireConfirm(confirm); err != nil {
		return err
	}
	record, err := s.store.GetMailDomain(ctx, tenantID, domainID)
	if err != nil {
		return err
	}

	mailboxes, err := s.store.ListMailboxes(ctx, tenantID, domainID)
	if err != nil {
		return err
	}
	for _, m := range mailboxes {
		if err = s.store.DeleteMailbox(ctx, tenantID, m.ID); err != nil {
			return err
		}
	}
	aliases, err := s.store.ListAliases(ctx, tenantID, domainID)
	if err != nil {
		return err
	}
	for _, a := range aliases {
		if err = s.store.DeleteAlias(ctx, tenantID, a.ID); err != nil {
			return err
		}
	}
	if err = s.store.DeleteMailDomain(ctx, tenantID, domainID); err != nil {
		return err
	}
	if err = s.retractMailDNS(ctx, record); err != nil {
		return err
	}
	if err = s.publishMail(ctx); err != nil {
		return err
	}

	keyPath, err := s.dkimKeyPath(record)
	if err != nil {
		return err
	}
	if err = os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
		return internalErr(err, "the signing key could not be removed")
	}
	mailDir, err := s.tenantDir(DirMail, tenantID, record.Domain)
	if err != nil {
		return err
	}
	if err = os.RemoveAll(mailDir); err != nil {
		return internalErr(err, "the mail storage could not be removed")
	}
	s.audit(ctx, "hosting.maildomain.delete", tenantID, record.ID, "domain", record.Domain)
	return nil
}

// MailDNSRecords returns the records a domain needs for deliverable mail. The
// panel shows them so a tenant hosting DNS elsewhere can publish them by hand.
func (s *Service) MailDNSRecords(ctx context.Context, tenantID, domainID string) ([]RecordRequest, error) {
	record, err := s.store.GetMailDomain(ctx, tenantID, domainID)
	if err != nil {
		return nil, err
	}
	return s.mailRecords(record)
}

// CreateMailbox provisions a virtual mailbox with an Argon2id password hash.
func (s *Service) CreateMailbox(ctx context.Context, tenantID, domainID, localPart, password string, quotaMB int64) (Mailbox, error) {
	domain, err := s.store.GetMailDomain(ctx, tenantID, domainID)
	if err != nil {
		return Mailbox{}, err
	}
	if err = ValidateLocalPart(localPart); err != nil {
		return Mailbox{}, err
	}
	if err = validateMailPassword(password); err != nil {
		return Mailbox{}, err
	}
	if quotaMB == 0 {
		quotaMB = defaultMailboxQuotaMB
	}
	if err = ValidateQuotaMB("quota_mb", quotaMB); err != nil {
		return Mailbox{}, err
	}

	local := strings.ToLower(strings.TrimSpace(localPart))
	existing, err := s.store.ListMailboxes(ctx, tenantID, domainID)
	if err != nil {
		return Mailbox{}, err
	}
	for _, m := range existing {
		if m.LocalPart == local {
			return Mailbox{}, apperr.New(apperr.CodeConflict, 409, "that mailbox already exists")
		}
	}
	total, err := s.countMailboxes(ctx, tenantID)
	if err != nil {
		return Mailbox{}, err
	}
	if err = s.checkQuota(ctx, tenantID, ResourceMailboxes, total); err != nil {
		return Mailbox{}, err
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return Mailbox{}, invalid("password", "was rejected by the password policy")
	}
	now := s.now().UTC()
	mailbox := Mailbox{
		ID:           s.newID(),
		TenantID:     tenantID,
		DomainID:     domainID,
		Domain:       domain.Domain,
		LocalPart:    local,
		PasswordHash: hash,
		QuotaMB:      quotaMB,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	home, err := s.mailboxHome(mailbox)
	if err != nil {
		return Mailbox{}, err
	}
	if err = ensureDir(home, dirMode); err != nil {
		return Mailbox{}, err
	}
	if err = s.store.CreateMailbox(ctx, mailbox); err != nil {
		return Mailbox{}, err
	}
	if err = s.publishMail(ctx); err != nil {
		return Mailbox{}, err
	}
	s.audit(ctx, "hosting.mailbox.create", tenantID, mailbox.ID, "domain", domain.Domain)
	return sanitizeMailbox(mailbox), nil
}

// ListMailboxes returns the mailboxes of one domain owned by the tenant.
func (s *Service) ListMailboxes(ctx context.Context, tenantID, domainID string) ([]Mailbox, error) {
	if _, err := s.store.GetMailDomain(ctx, tenantID, domainID); err != nil {
		return nil, err
	}
	boxes, err := s.store.ListMailboxes(ctx, tenantID, domainID)
	if err != nil {
		return nil, err
	}
	for i := range boxes {
		boxes[i] = sanitizeMailbox(boxes[i])
	}
	return boxes, nil
}

// SetMailboxPassword replaces a mailbox password with a fresh Argon2id hash.
func (s *Service) SetMailboxPassword(ctx context.Context, tenantID, mailboxID, password string) error {
	if err := ValidateID("tenant", tenantID); err != nil {
		return err
	}
	if err := ValidateID("mailbox", mailboxID); err != nil {
		return err
	}
	if err := validateMailPassword(password); err != nil {
		return err
	}
	mailbox, err := s.store.GetMailbox(ctx, tenantID, mailboxID)
	if err != nil {
		return err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return invalid("password", "was rejected by the password policy")
	}
	mailbox.PasswordHash = hash
	mailbox.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateMailbox(ctx, mailbox); err != nil {
		return err
	}
	if err = s.publishMail(ctx); err != nil {
		return err
	}
	s.audit(ctx, "hosting.mailbox.password", tenantID, mailbox.ID)
	return nil
}

// SetMailboxQuota changes the storage ceiling of a mailbox.
func (s *Service) SetMailboxQuota(ctx context.Context, tenantID, mailboxID string, quotaMB int64) (Mailbox, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return Mailbox{}, err
	}
	if err := ValidateID("mailbox", mailboxID); err != nil {
		return Mailbox{}, err
	}
	if err := ValidateQuotaMB("quota_mb", quotaMB); err != nil {
		return Mailbox{}, err
	}
	mailbox, err := s.store.GetMailbox(ctx, tenantID, mailboxID)
	if err != nil {
		return Mailbox{}, err
	}
	mailbox.QuotaMB = quotaMB
	mailbox.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateMailbox(ctx, mailbox); err != nil {
		return Mailbox{}, err
	}
	if err = s.publishMail(ctx); err != nil {
		return Mailbox{}, err
	}
	s.audit(ctx, "hosting.mailbox.quota", tenantID, mailbox.ID, "quota_mb", quotaMB)
	return sanitizeMailbox(mailbox), nil
}

// SetMailboxEnabled suspends or restores delivery and login for a mailbox.
func (s *Service) SetMailboxEnabled(ctx context.Context, tenantID, mailboxID string, enabled bool) (Mailbox, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return Mailbox{}, err
	}
	if err := ValidateID("mailbox", mailboxID); err != nil {
		return Mailbox{}, err
	}
	mailbox, err := s.store.GetMailbox(ctx, tenantID, mailboxID)
	if err != nil {
		return Mailbox{}, err
	}
	if mailbox.Enabled == enabled {
		return sanitizeMailbox(mailbox), nil
	}
	mailbox.Enabled = enabled
	mailbox.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateMailbox(ctx, mailbox); err != nil {
		return Mailbox{}, err
	}
	if err = s.publishMail(ctx); err != nil {
		return Mailbox{}, err
	}
	event := "hosting.mailbox.disable"
	if enabled {
		event = "hosting.mailbox.enable"
	}
	s.audit(ctx, event, tenantID, mailbox.ID)
	return sanitizeMailbox(mailbox), nil
}

// DeleteMailbox removes a mailbox and its stored mail.
func (s *Service) DeleteMailbox(ctx context.Context, tenantID, mailboxID string, confirm bool) error {
	if err := requireConfirm(confirm); err != nil {
		return err
	}
	if err := ValidateID("tenant", tenantID); err != nil {
		return err
	}
	if err := ValidateID("mailbox", mailboxID); err != nil {
		return err
	}
	mailbox, err := s.store.GetMailbox(ctx, tenantID, mailboxID)
	if err != nil {
		return err
	}
	if err = s.store.DeleteMailbox(ctx, tenantID, mailboxID); err != nil {
		return err
	}
	if err = s.publishMail(ctx); err != nil {
		return err
	}
	home, err := s.mailboxHome(mailbox)
	if err != nil {
		return err
	}
	if err = os.RemoveAll(home); err != nil {
		return internalErr(err, "the mailbox storage could not be removed")
	}
	s.audit(ctx, "hosting.mailbox.delete", tenantID, mailboxID)
	return nil
}

// CreateAlias forwards one address of a hosted domain to any destination.
func (s *Service) CreateAlias(ctx context.Context, tenantID, domainID, source, destination string) (Alias, error) {
	domain, err := s.store.GetMailDomain(ctx, tenantID, domainID)
	if err != nil {
		return Alias{}, err
	}
	if err = ValidateLocalPart(source); err != nil {
		return Alias{}, err
	}
	dest, err := normalizeAddress(destination)
	if err != nil {
		return Alias{}, err
	}

	local := strings.ToLower(strings.TrimSpace(source))
	existing, err := s.store.ListAliases(ctx, tenantID, domainID)
	if err != nil {
		return Alias{}, err
	}
	for _, a := range existing {
		if a.Source == local && a.Destination == dest {
			return Alias{}, apperr.New(apperr.CodeConflict, 409, "that alias already exists")
		}
	}

	now := s.now().UTC()
	alias := Alias{
		ID:          s.newID(),
		TenantID:    tenantID,
		DomainID:    domainID,
		Domain:      domain.Domain,
		Source:      local,
		Destination: dest,
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err = s.store.CreateAlias(ctx, alias); err != nil {
		return Alias{}, err
	}
	if err = s.publishMail(ctx); err != nil {
		return Alias{}, err
	}
	s.audit(ctx, "hosting.alias.create", tenantID, alias.ID, "domain", domain.Domain)
	return alias, nil
}

// ListAliases returns the aliases of one domain owned by the tenant.
func (s *Service) ListAliases(ctx context.Context, tenantID, domainID string) ([]Alias, error) {
	if _, err := s.store.GetMailDomain(ctx, tenantID, domainID); err != nil {
		return nil, err
	}
	return s.store.ListAliases(ctx, tenantID, domainID)
}

// DeleteAlias removes one alias.
func (s *Service) DeleteAlias(ctx context.Context, tenantID, aliasID string, confirm bool) error {
	if err := requireConfirm(confirm); err != nil {
		return err
	}
	if err := ValidateID("tenant", tenantID); err != nil {
		return err
	}
	if err := ValidateID("alias", aliasID); err != nil {
		return err
	}
	if _, err := s.store.GetAlias(ctx, tenantID, aliasID); err != nil {
		return err
	}
	if err := s.store.DeleteAlias(ctx, tenantID, aliasID); err != nil {
		return err
	}
	if err := s.publishMail(ctx); err != nil {
		return err
	}
	s.audit(ctx, "hosting.alias.delete", tenantID, aliasID)
	return nil
}

// publishMail regenerates every mail map from stored state and reloads the
// three services in dependency order: transport, delivery, then signing.
func (s *Service) publishMail(ctx context.Context) error {
	data, err := s.mailData(ctx)
	if err != nil {
		return err
	}

	domains, err := s.renderMail(virtualDomainsTemplate, data)
	if err != nil {
		return err
	}
	mailboxes, err := s.renderMail(virtualMailboxTemplate, data)
	if err != nil {
		return err
	}
	aliases, err := s.renderMail(virtualAliasTemplate, data)
	if err != nil {
		return err
	}
	users, err := s.renderMail(dovecotUsersTemplate, data)
	if err != nil {
		return err
	}
	keyTable, err := s.renderMail(dkimKeyTableTemplate, data)
	if err != nil {
		return err
	}
	signingTable, err := s.renderMail(dkimSigningTableTemplate, data)
	if err != nil {
		return err
	}
	trusted, err := s.renderMail(dkimTrustedHostsTemplate, data)
	if err != nil {
		return err
	}

	postfixFiles, err := s.mailFiles(map[string][]byte{
		fileVirtualDomains:   domains,
		fileVirtualMailboxes: mailboxes,
		fileVirtualAliases:   aliases,
	})
	if err != nil {
		return err
	}
	if err = s.apply(ctx, applyPlan{
		Files:   postfixFiles,
		Check:   s.cmds.PostfixCheck,
		Reload:  s.cmds.PostfixReload,
		Service: postfixService,
	}); err != nil {
		return err
	}

	dovecotFiles, err := s.mailFiles(map[string][]byte{fileDovecotUsers: users})
	if err != nil {
		return err
	}
	for i := range dovecotFiles {
		dovecotFiles[i].Mode = secretMode
	}
	if err = s.apply(ctx, applyPlan{
		Files:   dovecotFiles,
		Check:   s.cmds.DovecotCheck,
		Reload:  s.cmds.DovecotReload,
		Service: dovecotService,
	}); err != nil {
		return err
	}

	dkimFiles, err := s.mailFiles(map[string][]byte{
		fileDKIMKeyTable:     keyTable,
		fileDKIMSigningTable: signingTable,
		fileDKIMTrustedHosts: trusted,
	})
	if err != nil {
		return err
	}
	return s.apply(ctx, applyPlan{
		Files:   dkimFiles,
		Reload:  s.cmds.OpenDKIMReload,
		Service: dkimService,
	})
}

// mailFiles resolves each generated map name to a configFile under the mail
// configuration directory.
func (s *Service) mailFiles(contents map[string][]byte) ([]configFile, error) {
	names := []string{
		fileVirtualDomains, fileVirtualMailboxes, fileVirtualAliases,
		fileDovecotUsers, fileDKIMKeyTable, fileDKIMSigningTable, fileDKIMTrustedHosts,
	}
	files := make([]configFile, 0, len(contents))
	for _, name := range names {
		content, ok := contents[name]
		if !ok {
			continue
		}
		full, err := s.systemPath(DirMailConf, name)
		if err != nil {
			return nil, err
		}
		files = append(files, configFile{Path: full, Content: content, Mode: configMode})
	}
	return files, nil
}

// mailData collects the installation-wide mail state the maps render from.
func (s *Service) mailData(ctx context.Context) (mailData, error) {
	domains, err := s.store.ListAllMailDomains(ctx)
	if err != nil {
		return mailData{}, err
	}
	mailboxes, err := s.store.ListAllMailboxes(ctx)
	if err != nil {
		return mailData{}, err
	}
	aliases, err := s.store.ListAllAliases(ctx)
	if err != nil {
		return mailData{}, err
	}

	data := mailData{Hostname: s.mailHostname}
	enabled := make(map[string]bool, len(domains))
	for _, d := range domains {
		if !d.Enabled {
			continue
		}
		enabled[d.ID] = true
		keyPath, keyErr := s.dkimKeyPath(d)
		if keyErr != nil {
			return mailData{}, keyErr
		}
		data.Domains = append(data.Domains, mailDomainEntry{Domain: d.Domain})
		data.DKIM = append(data.DKIM, dkimEntry{Domain: d.Domain, Selector: d.DKIMSelector, KeyPath: keyPath})
	}
	for _, m := range mailboxes {
		if !m.Enabled || !enabled[m.DomainID] {
			continue
		}
		home, homeErr := s.mailboxHome(m)
		if homeErr != nil {
			return mailData{}, homeErr
		}
		data.Mailboxes = append(data.Mailboxes, mailboxEntry{
			Address: m.LocalPart + "@" + m.Domain,
			Domain:  m.Domain,
			Local:   m.LocalPart,
			Hash:    m.PasswordHash,
			Home:    home,
			QuotaMB: m.QuotaMB,
		})
	}
	for _, a := range aliases {
		if !a.Enabled || !enabled[a.DomainID] {
			continue
		}
		data.Aliases = append(data.Aliases, aliasEntry{
			Source:      a.Source + "@" + a.Domain,
			Destination: a.Destination,
		})
	}
	return data, nil
}

// renderMail executes one mail template.
func (s *Service) renderMail(tmpl *template.Template, data mailData) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, apperr.Wrap(err, apperr.CodeValidation, 422, "the mail configuration could not be generated")
	}
	return buf.Bytes(), nil
}

// mailRecords builds the MX, SPF, DKIM, and DMARC records of a mail domain.
func (s *Service) mailRecords(d MailDomain) ([]RecordRequest, error) {
	if s.mailHostname == "" {
		return nil, apperr.New(apperr.CodeUnavailable, 503, "the mail hostname is not configured")
	}
	selector := d.DKIMSelector
	if selector == "" {
		selector = dkimSelector
	}
	if _, err := tmplToken(selector); err != nil {
		return nil, err
	}
	return []RecordRequest{
		{Name: "@", Type: RecordMX, Value: s.mailHostname, Priority: 10, TTL: defaultZoneTTL},
		{Name: "@", Type: RecordTXT, Value: spfPolicy, TTL: defaultZoneTTL},
		{
			Name:  selector + "._domainkey",
			Type:  RecordTXT,
			Value: "v=DKIM1; k=rsa; p=" + d.DKIMPublic,
			TTL:   defaultZoneTTL,
		},
		{
			Name:  "_dmarc",
			Type:  RecordTXT,
			Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + d.Domain,
			TTL:   defaultZoneTTL,
		},
	}, nil
}

// publishMailDNS writes the mail records into the tenant's own zone when the
// domain is hosted here. A domain whose DNS lives elsewhere is left alone; the
// records are still available through MailDNSRecords.
func (s *Service) publishMailDNS(ctx context.Context, d MailDomain) error {
	zone, err := s.store.ZoneByName(ctx, d.Domain)
	if err != nil {
		if apperr.Is(err, apperr.CodeNotFound) {
			return nil
		}
		return err
	}
	if zone.TenantID != d.TenantID {
		return nil
	}
	records, err := s.mailRecords(d)
	if err != nil {
		return err
	}
	for _, r := range records {
		if err = s.upsertManagedRecord(ctx, zone, r); err != nil {
			return err
		}
	}
	return s.bumpAndPublish(ctx, zone)
}

// retractMailDNS removes the managed mail records from a hosted zone.
func (s *Service) retractMailDNS(ctx context.Context, d MailDomain) error {
	zone, err := s.store.ZoneByName(ctx, d.Domain)
	if err != nil {
		if apperr.Is(err, apperr.CodeNotFound) {
			return nil
		}
		return err
	}
	if zone.TenantID != d.TenantID {
		return nil
	}
	selector := d.DKIMSelector
	if selector == "" {
		selector = dkimSelector
	}
	if err = s.removeManagedRecords(ctx, zone, RecordMX, "@"); err != nil {
		return err
	}
	for _, name := range []string{"@", selector + "._domainkey", "_dmarc"} {
		if err = s.removeManagedRecords(ctx, zone, RecordTXT, name); err != nil {
			return err
		}
	}
	return s.bumpAndPublish(ctx, zone)
}

// writeDKIMKey stores the signing key on disk for OpenDKIM with owner-only
// permissions. The database copy stays encrypted.
func (s *Service) writeDKIMKey(d MailDomain, privatePEM []byte) error {
	keyPath, err := s.dkimKeyPath(d)
	if err != nil {
		return err
	}
	return writeFileAtomic(keyPath, privatePEM, secretMode)
}

// dkimKeyPath resolves the on-disk signing key of a mail domain.
func (s *Service) dkimKeyPath(d MailDomain) (string, error) {
	domain, err := ValidateDomain(d.Domain)
	if err != nil {
		return "", err
	}
	selector := d.DKIMSelector
	if selector == "" {
		selector = dkimSelector
	}
	if _, err = tmplToken(selector); err != nil {
		return "", err
	}
	return s.systemPath(DirDKIM, domain+"."+selector+".private")
}

// mailboxHome resolves the tenant-scoped home directory of a mailbox.
func (s *Service) mailboxHome(m Mailbox) (string, error) {
	domain, err := ValidateDomain(m.Domain)
	if err != nil {
		return "", err
	}
	if err = ValidateLocalPart(m.LocalPart); err != nil {
		return "", err
	}
	return s.tenantDir(DirMail, m.TenantID, path(domain, m.LocalPart))
}

// countMailboxes totals the mailboxes a tenant owns across its domains.
func (s *Service) countMailboxes(ctx context.Context, tenantID string) (int64, error) {
	domains, err := s.store.ListMailDomains(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, d := range domains {
		boxes, boxErr := s.store.ListMailboxes(ctx, tenantID, d.ID)
		if boxErr != nil {
			return 0, boxErr
		}
		total += int64(len(boxes))
	}
	return total, nil
}

// generateDKIMKey creates an RSA signing key and returns the PEM private key
// with the base64 public half that goes into the DKIM TXT record.
func generateDKIMKey() ([]byte, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, dkimKeyBits)
	if err != nil {
		return nil, "", internalErr(err, "the signing key could not be generated")
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, "", internalErr(err, "the signing key could not be generated")
	}
	return privatePEM, base64.StdEncoding.EncodeToString(pubDER), nil
}

// normalizeAddress validates a full mail address and returns its lower form.
func normalizeAddress(raw string) (string, error) {
	local, domain, ok := strings.Cut(strings.TrimSpace(raw), "@")
	if !ok {
		return "", invalid("address", "must be local@domain")
	}
	if err := ValidateLocalPart(local); err != nil {
		return "", err
	}
	d, err := ValidateDomain(domain)
	if err != nil {
		return "", err
	}
	return strings.ToLower(local) + "@" + d, nil
}

// validateMailPassword enforces the minimum mailbox password length before the
// value ever reaches the hasher.
func validateMailPassword(password string) error {
	if len(password) < minMailPasswordLen {
		return invalid("password", "must be at least 12 characters")
	}
	if strings.TrimSpace(password) != password {
		return invalid("password", "must not start or end with whitespace")
	}
	return nil
}

// sanitizeMailDomain strips key material from a record before it leaves the
// service, so an encrypted private key never reaches an API response.
func sanitizeMailDomain(d MailDomain) MailDomain {
	d.DKIMPrivate = nil
	return d
}

// sanitizeMailbox strips the password hash from a mailbox before it leaves the
// service.
func sanitizeMailbox(m Mailbox) Mailbox {
	m.PasswordHash = ""
	return m
}
