package hosting

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// createTestZone provisions a verified zone for a tenant.
func createTestZone(t *testing.T, env *testEnv, tenantID, domain string) Zone {
	t.Helper()
	env.verify(t, tenantID, domain)
	zone, err := env.svc.CreateZone(context.Background(), tenantID, domain, false)
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	return zone
}

func TestCreateZoneWritesGoldenZoneFile(t *testing.T) {
	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")

	want := strings.Join([]string{
		"; Managed by cashp - manual edits are overwritten.",
		"$ORIGIN example.com.",
		"$TTL 3600",
		"@\tIN\tSOA\tns1.example.net. hostmaster.example.com. (",
		"\t2026030401\t; serial",
		"\t7200\t; refresh",
		"\t3600\t; retry",
		"\t1209600\t; expire",
		"\t3600\t; minimum",
		"\t)",
		"@\t3600\tIN\tNS\tns1.example.net.",
		"@\t3600\tIN\tNS\tns2.example.net.",
		"",
	}, "\n")

	got, err := env.svc.RenderZoneFile(context.Background(), "tenant1", zone.ID)
	if err != nil {
		t.Fatalf("RenderZoneFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("zone file mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	onDisk, err := os.ReadFile(filepath.Join(env.root, DirBindZones, "db.example.com"))
	if err != nil {
		t.Fatalf("read zone file: %v", err)
	}
	if string(onDisk) != want {
		t.Fatal("the published zone file differs from the rendered one")
	}
	if !env.runner.ran("named-checkzone example.com") || !env.runner.ran("rndc reload example.com") {
		t.Fatal("the zone was not validated and reloaded")
	}
	include, err := os.ReadFile(filepath.Join(env.root, DirBind, namedIncludeFile))
	if err != nil {
		t.Fatalf("read named include: %v", err)
	}
	if !strings.Contains(string(include), `zone "example.com." {`) {
		t.Fatalf("named include missing the zone:\n%s", include)
	}
	if strings.Contains(string(include), "dnssec-policy") {
		t.Fatal("dnssec was configured for an unsigned zone")
	}
}

func TestCreateZoneDNSSECIncludesPolicy(t *testing.T) {
	env := newTestEnv(t)
	env.verify(t, "tenant1", "signed.example")
	if _, err := env.svc.CreateZone(context.Background(), "tenant1", "signed.example", true); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	include, err := os.ReadFile(filepath.Join(env.root, DirBind, namedIncludeFile))
	if err != nil {
		t.Fatalf("read named include: %v", err)
	}
	for _, want := range []string{"dnssec-policy default;", "inline-signing yes;", "key-directory"} {
		if !strings.Contains(string(include), want) {
			t.Fatalf("named include missing %q:\n%s", want, include)
		}
	}
}

func TestCreateZoneRequiresVerifiedOwnership(t *testing.T) {
	env := newTestEnv(t)
	_, err := env.svc.CreateZone(context.Background(), "tenant1", "notmine.example", false)
	if !apperr.Is(err, apperr.CodeForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
}

func TestRecordValidationPerType(t *testing.T) {
	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")
	ctx := context.Background()

	valid := []RecordRequest{
		{Name: "www", Type: RecordA, Value: "192.0.2.10"},
		{Name: "www", Type: RecordAAAA, Value: "2001:db8::1"},
		{Name: "blog", Type: RecordCNAME, Value: "www.example.com"},
		{Name: "@", Type: RecordMX, Value: "mail.example.com", Priority: 10},
		{Name: "@", Type: RecordTXT, Value: "v=spf1 mx -all"},
		{Name: "_sip._tcp", Type: RecordSRV, Value: "sip.example.com", Priority: 10, Weight: 5, Port: 5060},
		{Name: "sub", Type: RecordNS, Value: "ns1.example.net"},
		{Name: "@", Type: RecordCAA, Value: "issue letsencrypt.org", Priority: 0},
	}
	for _, req := range valid {
		if _, err := env.svc.CreateRecord(ctx, "tenant1", zone.ID, req); err != nil {
			t.Fatalf("%s record rejected: %v", req.Type, err)
		}
	}

	invalidCases := []struct {
		name string
		req  RecordRequest
	}{
		{"A with a hostname", RecordRequest{Name: "a", Type: RecordA, Value: "example.com"}},
		{"A with an IPv6 address", RecordRequest{Name: "a", Type: RecordA, Value: "2001:db8::1"}},
		{"AAAA with an IPv4 address", RecordRequest{Name: "b", Type: RecordAAAA, Value: "192.0.2.10"}},
		{"CNAME at the apex", RecordRequest{Name: "@", Type: RecordCNAME, Value: "www.example.com"}},
		{"MX preference out of range", RecordRequest{Name: "@", Type: RecordMX, Value: "mail.example.com", Priority: 70000}},
		{"SRV without a service name", RecordRequest{Name: "sip", Type: RecordSRV, Value: "sip.example.com", Port: 5060}},
		{"SRV without a port", RecordRequest{Name: "_sip._udp", Type: RecordSRV, Value: "sip.example.com"}},
		{"CAA flags out of range", RecordRequest{Name: "@", Type: RecordCAA, Value: "issue letsencrypt.org", Priority: 999}},
		{"unsupported type", RecordRequest{Name: "x", Type: "ANY", Value: "192.0.2.1"}},
		{"TXT with a newline", RecordRequest{Name: "x", Type: RecordTXT, Value: "one\ntwo"}},
		{"name with a traversal", RecordRequest{Name: "../etc", Type: RecordA, Value: "192.0.2.1"}},
		{"target that ends the record", RecordRequest{Name: "x", Type: RecordA, Value: "192.0.2.1\n@ IN NS evil.example."}},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := env.svc.CreateRecord(ctx, "tenant1", zone.ID, tc.req); err == nil {
				t.Fatal("an invalid record was accepted")
			}
		})
	}
}

func TestRecordConflictRules(t *testing.T) {
	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")
	ctx := context.Background()

	if _, err := env.svc.CreateRecord(ctx, "tenant1", zone.ID, RecordRequest{Name: "www", Type: RecordA, Value: "192.0.2.10"}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	_, err := env.svc.CreateRecord(ctx, "tenant1", zone.ID, RecordRequest{Name: "www", Type: RecordCNAME, Value: "other.example.com"})
	if !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a CNAME shared a name with an A record: %v", err)
	}
	_, err = env.svc.CreateRecord(ctx, "tenant1", zone.ID, RecordRequest{Name: "www", Type: RecordA, Value: "192.0.2.10"})
	if !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("a duplicate record was accepted: %v", err)
	}
}

func TestManagedRecordsAreNotTenantEditable(t *testing.T) {
	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")
	ctx := context.Background()

	records, err := env.svc.ListRecords(ctx, "tenant1", zone.ID)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("want the seeded NS set, got %d records", len(records))
	}
	req := RecordRequest{Name: "@", Type: RecordNS, Value: "ns.evil.example"}
	if _, err = env.svc.UpdateRecord(ctx, "tenant1", records[0].ID, req); !apperr.Is(err, apperr.CodeForbidden) {
		t.Fatalf("a managed record was edited: %v", err)
	}
	if err = env.svc.DeleteRecord(ctx, "tenant1", records[0].ID, true); !apperr.Is(err, apperr.CodeForbidden) {
		t.Fatalf("a managed record was deleted: %v", err)
	}
}

func TestSerialAlwaysIncreases(t *testing.T) {
	if got := nextSerial(0, time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)); got != 2026030401 {
		t.Fatalf("first serial = %d", got)
	}
	if got := nextSerial(2026030401, time.Date(2026, 3, 4, 23, 0, 0, 0, time.UTC)); got != 2026030402 {
		t.Fatalf("same-day serial = %d", got)
	}
	if got := nextSerial(2026030499, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)); got != 2026030501 {
		t.Fatalf("next-day serial = %d", got)
	}
	if got := nextSerial(2027010101, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)); got != 2027010102 {
		t.Fatalf("a clock that went backwards lowered the serial: %d", got)
	}

	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")
	if zone.Serial != 2026030401 {
		t.Fatalf("zone serial = %d", zone.Serial)
	}
	if _, err := env.svc.CreateRecord(context.Background(), "tenant1", zone.ID, RecordRequest{Name: "www", Type: RecordA, Value: "192.0.2.10"}); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	after, err := env.svc.GetZone(context.Background(), "tenant1", zone.ID)
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	if after.Serial <= zone.Serial {
		t.Fatalf("serial did not advance: %d -> %d", zone.Serial, after.Serial)
	}
}

func TestZoneRollsBackWhenNamedRejectsIt(t *testing.T) {
	env := newTestEnv(t)
	env.verify(t, "tenant1", "example.com")
	env.runner.failOn = "named-checkzone"

	_, err := env.svc.CreateZone(context.Background(), "tenant1", "example.com", false)
	if !apperr.Is(err, apperr.CodeValidation) {
		t.Fatalf("want validation error, got %v", err)
	}
	if strings.Contains(err.Error(), env.root) {
		t.Fatalf("error leaked a host path: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(env.root, DirBindZones, "db.example.com")); !os.IsNotExist(statErr) {
		t.Fatal("the rejected zone file was left in place")
	}
	if len(env.store.zones) != 0 || len(env.store.records) != 0 {
		t.Fatal("a rejected zone was left in the store")
	}
}

func TestZoneTenantIsolation(t *testing.T) {
	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")
	ctx := context.Background()
	record, err := env.svc.CreateRecord(ctx, "tenant1", zone.ID, RecordRequest{Name: "www", Type: RecordA, Value: "192.0.2.10"})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	if _, err = env.svc.GetZone(ctx, "tenant2", zone.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 read tenant1's zone: %v", err)
	}
	if _, err = env.svc.ListRecords(ctx, "tenant2", zone.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 listed tenant1's records: %v", err)
	}
	req := RecordRequest{Name: "www", Type: RecordA, Value: "203.0.113.9"}
	if _, err = env.svc.CreateRecord(ctx, "tenant2", zone.ID, req); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 added a record to tenant1's zone: %v", err)
	}
	if _, err = env.svc.UpdateRecord(ctx, "tenant2", record.ID, req); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 edited tenant1's record: %v", err)
	}
	if err = env.svc.DeleteRecord(ctx, "tenant2", record.ID, true); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 deleted tenant1's record: %v", err)
	}
	if err = env.svc.DeleteZone(ctx, "tenant2", zone.ID, true); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 deleted tenant1's zone: %v", err)
	}
}

func TestDeleteZoneRequiresConfirmation(t *testing.T) {
	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")

	if err := env.svc.DeleteZone(context.Background(), "tenant1", zone.ID, false); !apperr.Is(err, apperr.CodeBadRequest) {
		t.Fatalf("delete without confirmation: %v", err)
	}
	if err := env.svc.DeleteZone(context.Background(), "tenant1", zone.ID, true); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.root, DirBindZones, "db.example.com")); !os.IsNotExist(err) {
		t.Fatal("the zone file survived the delete")
	}
}

func TestResyncZonesRewritesDriftedFiles(t *testing.T) {
	env := newTestEnv(t)
	zone := createTestZone(t, env, "tenant1", "example.com")
	zonePath := filepath.Join(env.root, DirBindZones, "db.example.com")
	if err := os.WriteFile(zonePath, []byte("; hand edited\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := env.svc.ResyncZones(context.Background()); err != nil {
		t.Fatalf("ResyncZones: %v", err)
	}
	restored, err := os.ReadFile(zonePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(restored), "ns1.example.net.") {
		t.Fatalf("the drifted zone file was not rewritten:\n%s", restored)
	}

	before := len(env.runner.calls)
	if err = env.svc.ResyncZones(context.Background()); err != nil {
		t.Fatalf("second ResyncZones: %v", err)
	}
	if len(env.runner.calls) != before {
		t.Fatal("a converged resync still reloaded the dns server")
	}
	_ = zone
}
