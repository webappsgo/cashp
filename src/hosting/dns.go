package hosting

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// SOA defaults applied to a new zone. They are the conservative values a
// hosting panel is expected to publish and can be tuned per zone afterwards.
const (
	defaultZoneRefresh = 7200
	defaultZoneRetry   = 3600
	defaultZoneExpire  = 1209600
	defaultZoneMinimum = 3600
	defaultZoneTTL     = 3600
)

// dnssecPolicy is the BIND dnssec-policy applied to a signed zone. Signing and
// key rollover stay inside named, so cashp never touches private key material.
const dnssecPolicy = "default"

// bindService names the DNS subsystem in errors and audit entries.
const bindService = "dns server"

// namedIncludeFile is the generated file the host's named.conf includes.
const namedIncludeFile = "cashp-zones.conf"

// zoneFilePrefix is the conventional BIND master file prefix.
const zoneFilePrefix = "db."

// maxUint16 bounds MX preference, SRV priority, weight, and port.
const maxUint16 = 65535

// RecordRequest is the tenant-supplied description of a resource record.
type RecordRequest struct {
	// Name is the owner name relative to the zone; "@" is the apex.
	Name string
	// Type is one of the allowlisted record types.
	Type string
	// Value carries the type-specific data half.
	Value string
	// TTL overrides the zone default; zero uses the zone default.
	TTL int64
	// Priority is the MX preference, the SRV priority, or the CAA flags byte.
	Priority int64
	// Weight and Port apply to SRV only.
	Weight int64
	Port   int64
}

// CreateZone provisions an authoritative zone for a verified domain, seeds its
// NS records, writes the zone file, and adds it to the generated named include.
func (s *Service) CreateZone(ctx context.Context, tenantID, rawDomain string, dnssec bool) (Zone, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return Zone{}, err
	}
	domain, err := ValidateDomain(rawDomain)
	if err != nil {
		return Zone{}, err
	}
	if err = s.requireOwnedDomain(ctx, tenantID, domain); err != nil {
		return Zone{}, err
	}
	if len(s.nameservers) == 0 {
		return Zone{}, apperr.New(apperr.CodeUnavailable, 503, "no authoritative nameservers are configured")
	}
	if _, err = s.store.ZoneByName(ctx, domain); err == nil {
		return Zone{}, apperr.New(apperr.CodeConflict, 409, "a zone for that domain already exists")
	} else if !apperr.Is(err, apperr.CodeNotFound) {
		return Zone{}, err
	}
	existing, err := s.store.ListZones(ctx, tenantID)
	if err != nil {
		return Zone{}, err
	}
	if err = s.checkQuota(ctx, tenantID, ResourceZones, int64(len(existing))); err != nil {
		return Zone{}, err
	}

	now := s.now().UTC()
	zone := Zone{
		ID:         s.newID(),
		TenantID:   tenantID,
		Name:       domain,
		PrimaryNS:  s.nameservers[0],
		Hostmaster: s.hostmaster,
		Serial:     nextSerial(0, now),
		Refresh:    defaultZoneRefresh,
		Retry:      defaultZoneRetry,
		Expire:     defaultZoneExpire,
		Minimum:    defaultZoneMinimum,
		DefaultTTL: defaultZoneTTL,
		DNSSEC:     dnssec,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err = s.store.CreateZone(ctx, zone); err != nil {
		return Zone{}, err
	}

	for _, ns := range s.nameservers {
		record := Record{
			ID:        s.newID(),
			ZoneID:    zone.ID,
			TenantID:  tenantID,
			Name:      "@",
			Type:      RecordNS,
			Value:     ns,
			TTL:       zone.DefaultTTL,
			Managed:   true,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err = s.store.CreateRecord(ctx, record); err != nil {
			return Zone{}, err
		}
	}

	if err = s.publishZone(ctx, zone); err != nil {
		s.rollbackZone(ctx, zone)
		return Zone{}, err
	}
	if err = s.rebuildNamedInclude(ctx); err != nil {
		s.rollbackZone(ctx, zone)
		return Zone{}, err
	}
	s.audit(ctx, "hosting.zone.create", tenantID, zone.ID, "domain", domain, "dnssec", dnssec)
	return zone, nil
}

// GetZone returns one zone owned by the tenant.
func (s *Service) GetZone(ctx context.Context, tenantID, zoneID string) (Zone, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return Zone{}, err
	}
	if err := ValidateID("zone", zoneID); err != nil {
		return Zone{}, err
	}
	return s.store.GetZone(ctx, tenantID, zoneID)
}

// ListZones returns every zone owned by the tenant.
func (s *Service) ListZones(ctx context.Context, tenantID string) ([]Zone, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return nil, err
	}
	return s.store.ListZones(ctx, tenantID)
}

// SetZoneDNSSEC turns inline signing on or off for a zone.
func (s *Service) SetZoneDNSSEC(ctx context.Context, tenantID, zoneID string, enabled bool) (Zone, error) {
	zone, err := s.GetZone(ctx, tenantID, zoneID)
	if err != nil {
		return Zone{}, err
	}
	if zone.DNSSEC == enabled {
		return zone, nil
	}
	zone.DNSSEC = enabled
	zone.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateZone(ctx, zone); err != nil {
		return Zone{}, err
	}
	if err = s.rebuildNamedInclude(ctx); err != nil {
		return Zone{}, err
	}
	s.audit(ctx, "hosting.zone.dnssec", tenantID, zone.ID, "enabled", enabled)
	return zone, nil
}

// SetZoneEnabled adds or removes the zone from the served configuration
// without discarding its records.
func (s *Service) SetZoneEnabled(ctx context.Context, tenantID, zoneID string, enabled bool) (Zone, error) {
	zone, err := s.GetZone(ctx, tenantID, zoneID)
	if err != nil {
		return Zone{}, err
	}
	if zone.Enabled == enabled {
		return zone, nil
	}
	zone.Enabled = enabled
	zone.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateZone(ctx, zone); err != nil {
		return Zone{}, err
	}
	if err = s.rebuildNamedInclude(ctx); err != nil {
		return Zone{}, err
	}
	event := "hosting.zone.disable"
	if enabled {
		event = "hosting.zone.enable"
	}
	s.audit(ctx, event, tenantID, zone.ID)
	return zone, nil
}

// DeleteZone removes a zone, its records, and its zone file. It is destructive
// and therefore requires an explicit confirmation.
func (s *Service) DeleteZone(ctx context.Context, tenantID, zoneID string, confirm bool) error {
	if err := requireConfirm(confirm); err != nil {
		return err
	}
	zone, err := s.GetZone(ctx, tenantID, zoneID)
	if err != nil {
		return err
	}
	records, err := s.store.ListRecords(ctx, tenantID, zoneID)
	if err != nil {
		return err
	}
	for _, r := range records {
		if err = s.store.DeleteRecord(ctx, tenantID, r.ID); err != nil {
			return err
		}
	}
	if err = s.store.DeleteZone(ctx, tenantID, zoneID); err != nil {
		return err
	}
	if err = s.rebuildNamedInclude(ctx); err != nil {
		return err
	}
	zonePath, err := s.zoneFilePath(zone)
	if err != nil {
		return err
	}
	plan := applyPlan{
		Files:      []configFile{{Path: zonePath, Mode: configMode, Remove: true}},
		Reload:     s.cmds.NamedReconfig,
		ReloadArgs: nil,
		Service:    bindService,
	}
	if err = s.apply(ctx, plan); err != nil {
		return err
	}
	s.audit(ctx, "hosting.zone.delete", tenantID, zone.ID, "domain", zone.Name)
	return nil
}

// ListRecords returns every record of a zone owned by the tenant.
func (s *Service) ListRecords(ctx context.Context, tenantID, zoneID string) ([]Record, error) {
	if _, err := s.GetZone(ctx, tenantID, zoneID); err != nil {
		return nil, err
	}
	return s.store.ListRecords(ctx, tenantID, zoneID)
}

// CreateRecord validates a record for its type, stores it, bumps the zone
// serial, and reloads the zone.
func (s *Service) CreateRecord(ctx context.Context, tenantID, zoneID string, req RecordRequest) (Record, error) {
	zone, err := s.GetZone(ctx, tenantID, zoneID)
	if err != nil {
		return Record{}, err
	}
	record, err := s.normalizeRecord(zone, req)
	if err != nil {
		return Record{}, err
	}
	existing, err := s.store.ListRecords(ctx, tenantID, zoneID)
	if err != nil {
		return Record{}, err
	}
	if err = checkRecordConflicts(existing, record, ""); err != nil {
		return Record{}, err
	}

	now := s.now().UTC()
	record.ID = s.newID()
	record.CreatedAt = now
	record.UpdatedAt = now
	if err = s.store.CreateRecord(ctx, record); err != nil {
		return Record{}, err
	}
	if err = s.bumpAndPublish(ctx, zone); err != nil {
		if delErr := s.store.DeleteRecord(ctx, tenantID, record.ID); delErr != nil {
			return Record{}, delErr
		}
		return Record{}, err
	}
	s.audit(ctx, "hosting.record.create", tenantID, record.ID, "zone", zone.Name, "type", record.Type)
	return record, nil
}

// UpdateRecord replaces the data of an existing record. A cashp-managed record
// (the zone NS set, the mail records) is not editable by a tenant.
func (s *Service) UpdateRecord(ctx context.Context, tenantID, recordID string, req RecordRequest) (Record, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return Record{}, err
	}
	if err := ValidateID("record", recordID); err != nil {
		return Record{}, err
	}
	current, err := s.store.GetRecord(ctx, tenantID, recordID)
	if err != nil {
		return Record{}, err
	}
	if current.Managed {
		return Record{}, apperr.New(apperr.CodeForbidden, 403, "that record is managed by the platform")
	}
	zone, err := s.GetZone(ctx, tenantID, current.ZoneID)
	if err != nil {
		return Record{}, err
	}
	updated, err := s.normalizeRecord(zone, req)
	if err != nil {
		return Record{}, err
	}
	existing, err := s.store.ListRecords(ctx, tenantID, zone.ID)
	if err != nil {
		return Record{}, err
	}
	if err = checkRecordConflicts(existing, updated, recordID); err != nil {
		return Record{}, err
	}

	updated.ID = current.ID
	updated.CreatedAt = current.CreatedAt
	updated.UpdatedAt = s.now().UTC()
	if err = s.store.UpdateRecord(ctx, updated); err != nil {
		return Record{}, err
	}
	if err = s.bumpAndPublish(ctx, zone); err != nil {
		if restoreErr := s.store.UpdateRecord(ctx, current); restoreErr != nil {
			return Record{}, restoreErr
		}
		return Record{}, err
	}
	s.audit(ctx, "hosting.record.update", tenantID, updated.ID, "zone", zone.Name, "type", updated.Type)
	return updated, nil
}

// DeleteRecord removes a tenant-owned record and reloads the zone.
func (s *Service) DeleteRecord(ctx context.Context, tenantID, recordID string, confirm bool) error {
	if err := requireConfirm(confirm); err != nil {
		return err
	}
	if err := ValidateID("tenant", tenantID); err != nil {
		return err
	}
	if err := ValidateID("record", recordID); err != nil {
		return err
	}
	record, err := s.store.GetRecord(ctx, tenantID, recordID)
	if err != nil {
		return err
	}
	if record.Managed {
		return apperr.New(apperr.CodeForbidden, 403, "that record is managed by the platform")
	}
	zone, err := s.GetZone(ctx, tenantID, record.ZoneID)
	if err != nil {
		return err
	}
	if err = s.store.DeleteRecord(ctx, tenantID, recordID); err != nil {
		return err
	}
	if err = s.bumpAndPublish(ctx, zone); err != nil {
		return err
	}
	s.audit(ctx, "hosting.record.delete", tenantID, recordID, "zone", zone.Name, "type", record.Type)
	return nil
}

// RenderZoneFile returns the zone file a zone would be served from.
func (s *Service) RenderZoneFile(ctx context.Context, tenantID, zoneID string) ([]byte, error) {
	zone, err := s.GetZone(ctx, tenantID, zoneID)
	if err != nil {
		return nil, err
	}
	records, err := s.store.ListRecords(ctx, tenantID, zoneID)
	if err != nil {
		return nil, err
	}
	return renderZone(zone, records)
}

// upsertManagedRecord creates or replaces a platform-managed record. It is the
// path the mail stack uses to publish MX, SPF, DKIM, and DMARC records.
func (s *Service) upsertManagedRecord(ctx context.Context, zone Zone, req RecordRequest) error {
	record, err := s.normalizeRecord(zone, req)
	if err != nil {
		return err
	}
	record.Managed = true
	existing, err := s.store.ListRecords(ctx, zone.TenantID, zone.ID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, r := range existing {
		if r.Name == record.Name && r.Type == record.Type && r.Managed {
			record.ID = r.ID
			record.CreatedAt = r.CreatedAt
			record.UpdatedAt = now
			return s.store.UpdateRecord(ctx, record)
		}
	}
	record.ID = s.newID()
	record.CreatedAt = now
	record.UpdatedAt = now
	return s.store.CreateRecord(ctx, record)
}

// removeManagedRecords deletes every managed record of a type in a zone.
func (s *Service) removeManagedRecords(ctx context.Context, zone Zone, recordType, name string) error {
	records, err := s.store.ListRecords(ctx, zone.TenantID, zone.ID)
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Managed && r.Type == recordType && (name == "" || r.Name == name) {
			if err = s.store.DeleteRecord(ctx, zone.TenantID, r.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// bumpAndPublish advances the zone serial and rewrites the zone file.
func (s *Service) bumpAndPublish(ctx context.Context, zone Zone) error {
	zone.Serial = nextSerial(zone.Serial, s.now().UTC())
	zone.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateZone(ctx, zone); err != nil {
		return err
	}
	return s.publishZone(ctx, zone)
}

// publishZone renders the zone file, validates it with named-checkzone, and
// asks named to reload just that zone.
func (s *Service) publishZone(ctx context.Context, zone Zone) error {
	records, err := s.store.ListRecords(ctx, zone.TenantID, zone.ID)
	if err != nil {
		return err
	}
	content, err := renderZone(zone, records)
	if err != nil {
		return err
	}
	zonePath, err := s.zoneFilePath(zone)
	if err != nil {
		return err
	}
	plan := applyPlan{
		Files:      []configFile{{Path: zonePath, Content: content, Mode: configMode}},
		Check:      s.cmds.NamedCheckZone,
		CheckArgs:  []string{zone.Name, zonePath},
		Reload:     s.cmds.NamedReload,
		ReloadArgs: []string{zone.Name},
		Service:    bindService,
	}
	return s.apply(ctx, plan)
}

// rebuildNamedInclude regenerates the include file listing every enabled zone
// and asks named to adopt the new zone set.
func (s *Service) rebuildNamedInclude(ctx context.Context) error {
	zones, err := s.store.ListAllZones(ctx)
	if err != nil {
		return err
	}
	keyDir, err := s.systemPath(DirBindKeys)
	if err != nil {
		return err
	}
	if err = ensureDir(keyDir, dirMode); err != nil {
		return err
	}

	data := namedData{Zones: make([]namedZone, 0, len(zones))}
	for _, z := range zones {
		if !z.Enabled {
			continue
		}
		zonePath, pathErr := s.zoneFilePath(z)
		if pathErr != nil {
			return pathErr
		}
		data.Zones = append(data.Zones, namedZone{
			Name:   z.Name,
			File:   zonePath,
			DNSSEC: z.DNSSEC,
			KeyDir: keyDir,
			Policy: dnssecPolicy,
		})
	}

	var buf bytes.Buffer
	if err = namedTemplate.Execute(&buf, data); err != nil {
		return apperr.Wrap(err, apperr.CodeValidation, 422, "the dns configuration could not be generated")
	}
	includePath, err := s.systemPath(DirBind, namedIncludeFile)
	if err != nil {
		return err
	}
	plan := applyPlan{
		Files:     []configFile{{Path: includePath, Content: buf.Bytes(), Mode: configMode}},
		Check:     s.cmds.NamedCheckConf,
		CheckArgs: []string{includePath},
		Reload:    s.cmds.NamedReconfig,
		Service:   bindService,
	}
	return s.apply(ctx, plan)
}

// ResyncZones rewrites any zone file on this node whose content no longer
// matches the stored zone state, then refreshes the generated named include.
// It is the body of the DNS resync scheduler task and is what makes a node
// that joined the cluster later converge on the same zone set.
func (s *Service) ResyncZones(ctx context.Context) error {
	zones, err := s.store.ListAllZones(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, zone := range zones {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !zone.Enabled {
			continue
		}
		records, recErr := s.store.ListRecords(ctx, zone.TenantID, zone.ID)
		if recErr != nil {
			return recErr
		}
		content, renderErr := renderZone(zone, records)
		if renderErr != nil {
			return renderErr
		}
		zonePath, pathErr := s.zoneFilePath(zone)
		if pathErr != nil {
			return pathErr
		}
		current, readErr := readFileIfExists(zonePath)
		if readErr != nil {
			return readErr
		}
		if string(current) == string(content) {
			continue
		}
		if err = s.publishZone(ctx, zone); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return s.rebuildNamedInclude(ctx)
}

// rollbackZone removes a half-created zone after an activation failure.
func (s *Service) rollbackZone(ctx context.Context, zone Zone) {
	records, err := s.store.ListRecords(ctx, zone.TenantID, zone.ID)
	if err == nil {
		for _, r := range records {
			if delErr := s.store.DeleteRecord(ctx, zone.TenantID, r.ID); delErr != nil {
				s.audit(ctx, "hosting.zone.rollback_failed", zone.TenantID, zone.ID)
			}
		}
	}
	if delErr := s.store.DeleteZone(ctx, zone.TenantID, zone.ID); delErr != nil {
		s.audit(ctx, "hosting.zone.rollback_failed", zone.TenantID, zone.ID)
	}
}

// zoneFilePath resolves the generated master file of a zone.
func (s *Service) zoneFilePath(zone Zone) (string, error) {
	domain, err := ValidateDomain(zone.Name)
	if err != nil {
		return "", err
	}
	return s.systemPath(DirBindZones, zoneFilePrefix+domain)
}

// renderZone renders a zone file from a zone and its records.
func renderZone(zone Zone, records []Record) ([]byte, error) {
	mailbox, err := soaMailbox(zone.Hostmaster, zone.Name)
	if err != nil {
		return nil, err
	}
	data := zoneData{
		Origin:     zone.Name,
		DefaultTTL: zone.DefaultTTL,
		PrimaryNS:  zone.PrimaryNS,
		Mailbox:    mailbox,
		Serial:     zone.Serial,
		Refresh:    zone.Refresh,
		Retry:      zone.Retry,
		Expire:     zone.Expire,
		Minimum:    zone.Minimum,
		Records:    make([]zoneRecord, 0, len(records)),
	}
	for _, r := range records {
		rdata, rdataErr := rdataFor(r)
		if rdataErr != nil {
			return nil, rdataErr
		}
		ttl := r.TTL
		if ttl == 0 {
			ttl = zone.DefaultTTL
		}
		data.Records = append(data.Records, zoneRecord{Name: r.Name, TTL: ttl, Type: r.Type, RData: rdata})
	}

	var buf bytes.Buffer
	if err = zoneTemplate.Execute(&buf, data); err != nil {
		return nil, apperr.Wrap(err, apperr.CodeValidation, 422, "the zone file could not be generated")
	}
	return buf.Bytes(), nil
}

// normalizeRecord validates a record request against the rules of its type and
// returns the stored form. Nothing here trusts the caller: every half of every
// record is checked before it can reach a zone file.
func (s *Service) normalizeRecord(zone Zone, req RecordRequest) (Record, error) {
	name, err := ValidateRecordName(req.Name)
	if err != nil {
		return Record{}, err
	}
	recordType := strings.ToUpper(strings.TrimSpace(req.Type))
	if err = ValidateRecordType(recordType); err != nil {
		return Record{}, err
	}
	ttl := req.TTL
	if ttl == 0 {
		ttl = zone.DefaultTTL
	}
	if err = ValidateTTL(ttl); err != nil {
		return Record{}, err
	}

	record := Record{
		ZoneID:   zone.ID,
		TenantID: zone.TenantID,
		Name:     name,
		Type:     recordType,
		TTL:      ttl,
	}
	value := strings.TrimSpace(req.Value)

	switch recordType {
	case RecordA:
		if err = ValidateIPv4(value); err != nil {
			return Record{}, err
		}
		record.Value = value
	case RecordAAAA:
		if err = ValidateIPv6(value); err != nil {
			return Record{}, err
		}
		record.Value = value
	case RecordCNAME:
		if name == "@" {
			return Record{}, invalid("record name", "a CNAME must not be placed at the zone apex")
		}
		target, targetErr := ValidateDomain(value)
		if targetErr != nil {
			return Record{}, targetErr
		}
		record.Value = target
	case RecordNS, RecordPTR:
		target, targetErr := ValidateDomain(value)
		if targetErr != nil {
			return Record{}, targetErr
		}
		record.Value = target
	case RecordMX:
		if req.Priority < 0 || req.Priority > maxUint16 {
			return Record{}, invalid("priority", "must be between 0 and 65535")
		}
		target, targetErr := ValidateDomain(value)
		if targetErr != nil {
			return Record{}, targetErr
		}
		record.Value = target
		record.Priority = req.Priority
	case RecordTXT:
		if err = ValidateTXTValue(value); err != nil {
			return Record{}, err
		}
		record.Value = value
	case RecordSRV:
		if err = validateSRVName(name); err != nil {
			return Record{}, err
		}
		if req.Priority < 0 || req.Priority > maxUint16 {
			return Record{}, invalid("priority", "must be between 0 and 65535")
		}
		if req.Weight < 0 || req.Weight > maxUint16 {
			return Record{}, invalid("weight", "must be between 0 and 65535")
		}
		if req.Port < 0 || req.Port > maxUint16 {
			return Record{}, invalid("port", "must be between 0 and 65535")
		}
		if req.Port == 0 {
			return Record{}, invalid("port", "must be between 1 and 65535")
		}
		target, targetErr := ValidateDomain(value)
		if targetErr != nil {
			return Record{}, targetErr
		}
		record.Value = target
		record.Priority = req.Priority
		record.Weight = req.Weight
		record.Port = req.Port
	case RecordCAA:
		if req.Priority < 0 || req.Priority > 255 {
			return Record{}, invalid("priority", "must be between 0 and 255")
		}
		if err = ValidateCAAValue(value); err != nil {
			return Record{}, err
		}
		record.Value = value
		record.Priority = req.Priority
	default:
		return Record{}, invalid("record type", "is not a supported type")
	}
	return record, nil
}

// validateSRVName enforces the _service._proto owner name SRV requires.
func validateSRVName(name string) error {
	labels := strings.Split(name, ".")
	if len(labels) < 2 || !strings.HasPrefix(labels[0], "_") || !strings.HasPrefix(labels[1], "_") {
		return invalid("record name", "must start with _service._proto")
	}
	return nil
}

// checkRecordConflicts enforces the coexistence rules of the record types: a
// CNAME excludes every other record at the same owner name, and a duplicate of
// the same name, type, and value is refused.
func checkRecordConflicts(existing []Record, candidate Record, excludeID string) error {
	for _, r := range existing {
		if r.ID == excludeID {
			continue
		}
		if r.Name != candidate.Name {
			continue
		}
		if r.Type == RecordCNAME || candidate.Type == RecordCNAME {
			return apperr.New(apperr.CodeConflict, 409, "a CNAME cannot share a name with another record")
		}
		if r.Type == candidate.Type && r.Value == candidate.Value && r.Priority == candidate.Priority {
			return apperr.New(apperr.CodeConflict, 409, "an identical record already exists")
		}
	}
	return nil
}

// rdataFor builds the data half of a record line from validated fields. Values
// are composed, never formatted from raw input, and the TXT and CAA payloads
// are quoted and escaped for zone-file syntax.
func rdataFor(r Record) (string, error) {
	switch r.Type {
	case RecordA:
		if err := ValidateIPv4(r.Value); err != nil {
			return "", err
		}
		return r.Value, nil
	case RecordAAAA:
		if err := ValidateIPv6(r.Value); err != nil {
			return "", err
		}
		return r.Value, nil
	case RecordCNAME, RecordNS, RecordPTR:
		return zoneFQDN(r.Value)
	case RecordMX:
		target, err := zoneFQDN(r.Value)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(r.Priority, 10) + " " + target, nil
	case RecordTXT:
		if err := ValidateTXTValue(r.Value); err != nil {
			return "", err
		}
		return zoneTXT(r.Value), nil
	case RecordSRV:
		target, err := zoneFQDN(r.Value)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(r.Priority, 10) + " " +
			strconv.FormatInt(r.Weight, 10) + " " +
			strconv.FormatInt(r.Port, 10) + " " + target, nil
	case RecordCAA:
		if err := ValidateCAAValue(r.Value); err != nil {
			return "", err
		}
		tag, value, _ := strings.Cut(strings.TrimSpace(r.Value), " ")
		return strconv.FormatInt(r.Priority, 10) + " " + strings.ToLower(tag) + " " + quoteZoneString(value), nil
	default:
		return "", invalid("record type", "is not a supported type")
	}
}

// zoneFQDN renders a target hostname as an absolute name.
func zoneFQDN(v string) (string, error) {
	domain, err := ValidateDomain(v)
	if err != nil {
		return "", err
	}
	return domain + ".", nil
}

// zoneTXT splits a payload into the 255-byte character strings a TXT record is
// made of and quotes each one.
func zoneTXT(v string) string {
	const chunk = 255
	parts := make([]string, 0, len(v)/chunk+1)
	for len(v) > chunk {
		parts = append(parts, quoteZoneString(v[:chunk]))
		v = v[chunk:]
	}
	parts = append(parts, quoteZoneString(v))
	return strings.Join(parts, " ")
}

// quoteZoneString wraps a payload in quotes and escapes the two characters
// that would otherwise end the string or start an escape sequence.
func quoteZoneString(v string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(v) + `"`
}

// soaMailbox renders the SOA responsible party in zone-file form, escaping a
// dot inside the local part as BIND requires.
func soaMailbox(hostmaster, zoneName string) (string, error) {
	if err := ValidateHostmaster(hostmaster); err != nil {
		return "", err
	}
	local, domain, hasDomain := strings.Cut(hostmaster, "@")
	if !hasDomain {
		normalized, err := ValidateDomain(zoneName)
		if err != nil {
			return "", err
		}
		domain = normalized
	} else {
		normalized, err := ValidateDomain(domain)
		if err != nil {
			return "", err
		}
		domain = normalized
	}
	if err := ValidateLocalPart(local); err != nil {
		return "", err
	}
	return strings.ReplaceAll(local, ".", `\.`) + "." + domain + ".", nil
}

// nextSerial returns the next zone serial in the conventional YYYYMMDDnn form,
// always strictly greater than the current one so a secondary always sees the
// change even when a zone is edited many times in one day.
func nextSerial(current int64, now time.Time) int64 {
	base := int64(now.Year())*1000000 + int64(now.Month())*10000 + int64(now.Day())*100
	if current < base {
		return base + 1
	}
	return current + 1
}
