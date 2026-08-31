package hosting

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore is an in-memory Store used by every test in this package, so no
// test ever needs a database driver or a real service.
type memStore struct {
	mu         sync.Mutex
	sites      []Site
	zones      []Zone
	records    []Record
	domains    []MailDomain
	mailboxes  []Mailbox
	aliases    []Alias
	apps       []App
	env        []EnvVar
	releases   []Release
	ownership  []DomainOwnership
	failOnCall string
}

// newMemStore builds an empty store.
func newMemStore() *memStore { return &memStore{} }

// fail reports whether the named method should return an error, which the
// tests use to prove a failed persist does not leave config behind.
func (m *memStore) fail(name string) error {
	if m.failOnCall == name {
		return notFound("store")
	}
	return nil
}

func (m *memStore) CreateSite(_ context.Context, s Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateSite"); err != nil {
		return err
	}
	m.sites = append(m.sites, s)
	return nil
}

func (m *memStore) UpdateSite(_ context.Context, s Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.sites {
		if m.sites[i].ID == s.ID && m.sites[i].TenantID == s.TenantID {
			m.sites[i] = s
			return nil
		}
	}
	return notFound("site")
}

func (m *memStore) GetSite(_ context.Context, tenantID, id string) (Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sites {
		if s.ID == id && s.TenantID == tenantID {
			return s, nil
		}
	}
	return Site{}, notFound("site")
}

func (m *memStore) ListSites(_ context.Context, tenantID string) ([]Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Site{}
	for _, s := range m.sites {
		if s.TenantID == tenantID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memStore) ListAllSites(_ context.Context) ([]Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Site(nil), m.sites...), nil
}

func (m *memStore) DeleteSite(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.sites {
		if s.ID == id && s.TenantID == tenantID {
			m.sites = append(m.sites[:i], m.sites[i+1:]...)
			return nil
		}
	}
	return notFound("site")
}

func (m *memStore) SiteByDomain(_ context.Context, domain string) (Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sites {
		if s.PrimaryDomain == domain {
			return s, nil
		}
	}
	return Site{}, notFound("site")
}

func (m *memStore) CreateZone(_ context.Context, z Zone) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("CreateZone"); err != nil {
		return err
	}
	m.zones = append(m.zones, z)
	return nil
}

func (m *memStore) UpdateZone(_ context.Context, z Zone) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.zones {
		if m.zones[i].ID == z.ID && m.zones[i].TenantID == z.TenantID {
			m.zones[i] = z
			return nil
		}
	}
	return notFound("zone")
}

func (m *memStore) GetZone(_ context.Context, tenantID, id string) (Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, z := range m.zones {
		if z.ID == id && z.TenantID == tenantID {
			return z, nil
		}
	}
	return Zone{}, notFound("zone")
}

func (m *memStore) ZoneByName(_ context.Context, name string) (Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, z := range m.zones {
		if z.Name == name {
			return z, nil
		}
	}
	return Zone{}, notFound("zone")
}

func (m *memStore) ListZones(_ context.Context, tenantID string) ([]Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Zone{}
	for _, z := range m.zones {
		if z.TenantID == tenantID {
			out = append(out, z)
		}
	}
	return out, nil
}

func (m *memStore) ListAllZones(_ context.Context) ([]Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Zone(nil), m.zones...), nil
}

func (m *memStore) DeleteZone(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, z := range m.zones {
		if z.ID == id && z.TenantID == tenantID {
			m.zones = append(m.zones[:i], m.zones[i+1:]...)
			return nil
		}
	}
	return notFound("zone")
}

func (m *memStore) CreateRecord(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, r)
	return nil
}

func (m *memStore) UpdateRecord(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.records {
		if m.records[i].ID == r.ID && m.records[i].TenantID == r.TenantID {
			m.records[i] = r
			return nil
		}
	}
	return notFound("record")
}

func (m *memStore) GetRecord(_ context.Context, tenantID, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.records {
		if r.ID == id && r.TenantID == tenantID {
			return r, nil
		}
	}
	return Record{}, notFound("record")
}

func (m *memStore) ListRecords(_ context.Context, tenantID, zoneID string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Record{}
	for _, r := range m.records {
		if r.TenantID == tenantID && r.ZoneID == zoneID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStore) DeleteRecord(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.records {
		if r.ID == id && r.TenantID == tenantID {
			m.records = append(m.records[:i], m.records[i+1:]...)
			return nil
		}
	}
	return notFound("record")
}

func (m *memStore) CreateMailDomain(_ context.Context, d MailDomain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.domains = append(m.domains, d)
	return nil
}

func (m *memStore) UpdateMailDomain(_ context.Context, d MailDomain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.domains {
		if m.domains[i].ID == d.ID && m.domains[i].TenantID == d.TenantID {
			m.domains[i] = d
			return nil
		}
	}
	return notFound("mail domain")
}

func (m *memStore) GetMailDomain(_ context.Context, tenantID, id string) (MailDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.domains {
		if d.ID == id && d.TenantID == tenantID {
			return d, nil
		}
	}
	return MailDomain{}, notFound("mail domain")
}

func (m *memStore) MailDomainByName(_ context.Context, domain string) (MailDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.domains {
		if d.Domain == domain {
			return d, nil
		}
	}
	return MailDomain{}, notFound("mail domain")
}

func (m *memStore) ListMailDomains(_ context.Context, tenantID string) ([]MailDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MailDomain{}
	for _, d := range m.domains {
		if d.TenantID == tenantID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memStore) ListAllMailDomains(_ context.Context) ([]MailDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MailDomain(nil), m.domains...), nil
}

func (m *memStore) DeleteMailDomain(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, d := range m.domains {
		if d.ID == id && d.TenantID == tenantID {
			m.domains = append(m.domains[:i], m.domains[i+1:]...)
			return nil
		}
	}
	return notFound("mail domain")
}

func (m *memStore) CreateMailbox(_ context.Context, b Mailbox) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mailboxes = append(m.mailboxes, b)
	return nil
}

func (m *memStore) UpdateMailbox(_ context.Context, b Mailbox) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.mailboxes {
		if m.mailboxes[i].ID == b.ID && m.mailboxes[i].TenantID == b.TenantID {
			m.mailboxes[i] = b
			return nil
		}
	}
	return notFound("mailbox")
}

func (m *memStore) GetMailbox(_ context.Context, tenantID, id string) (Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.mailboxes {
		if b.ID == id && b.TenantID == tenantID {
			return b, nil
		}
	}
	return Mailbox{}, notFound("mailbox")
}

func (m *memStore) ListMailboxes(_ context.Context, tenantID, domainID string) ([]Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Mailbox{}
	for _, b := range m.mailboxes {
		if b.TenantID == tenantID && b.DomainID == domainID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *memStore) ListAllMailboxes(_ context.Context) ([]Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Mailbox(nil), m.mailboxes...), nil
}

func (m *memStore) DeleteMailbox(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, b := range m.mailboxes {
		if b.ID == id && b.TenantID == tenantID {
			m.mailboxes = append(m.mailboxes[:i], m.mailboxes[i+1:]...)
			return nil
		}
	}
	return notFound("mailbox")
}

func (m *memStore) CreateAlias(_ context.Context, a Alias) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aliases = append(m.aliases, a)
	return nil
}

func (m *memStore) GetAlias(_ context.Context, tenantID, id string) (Alias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.aliases {
		if a.ID == id && a.TenantID == tenantID {
			return a, nil
		}
	}
	return Alias{}, notFound("alias")
}

func (m *memStore) ListAliases(_ context.Context, tenantID, domainID string) ([]Alias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Alias{}
	for _, a := range m.aliases {
		if a.TenantID == tenantID && a.DomainID == domainID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memStore) ListAllAliases(_ context.Context) ([]Alias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Alias(nil), m.aliases...), nil
}

func (m *memStore) DeleteAlias(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, a := range m.aliases {
		if a.ID == id && a.TenantID == tenantID {
			m.aliases = append(m.aliases[:i], m.aliases[i+1:]...)
			return nil
		}
	}
	return notFound("alias")
}

func (m *memStore) CreateApp(_ context.Context, a App) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apps = append(m.apps, a)
	return nil
}

func (m *memStore) UpdateApp(_ context.Context, a App) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.apps {
		if m.apps[i].ID == a.ID && m.apps[i].TenantID == a.TenantID {
			m.apps[i] = a
			return nil
		}
	}
	return notFound("app")
}

func (m *memStore) GetApp(_ context.Context, tenantID, id string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.ID == id && a.TenantID == tenantID {
			return a, nil
		}
	}
	return App{}, notFound("app")
}

func (m *memStore) ListApps(_ context.Context, tenantID string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []App{}
	for _, a := range m.apps {
		if a.TenantID == tenantID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memStore) ListAllApps(_ context.Context) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]App(nil), m.apps...), nil
}

func (m *memStore) DeleteApp(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, a := range m.apps {
		if a.ID == id && a.TenantID == tenantID {
			m.apps = append(m.apps[:i], m.apps[i+1:]...)
			return nil
		}
	}
	return notFound("app")
}

func (m *memStore) PutEnv(_ context.Context, e EnvVar) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.env {
		if m.env[i].TenantID == e.TenantID && m.env[i].AppID == e.AppID && m.env[i].Key == e.Key {
			m.env[i] = e
			return nil
		}
	}
	m.env = append(m.env, e)
	return nil
}

func (m *memStore) ListEnv(_ context.Context, tenantID, appID string) ([]EnvVar, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []EnvVar{}
	for _, e := range m.env {
		if e.TenantID == tenantID && e.AppID == appID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStore) DeleteEnv(_ context.Context, tenantID, appID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, e := range m.env {
		if e.TenantID == tenantID && e.AppID == appID && e.Key == key {
			m.env = append(m.env[:i], m.env[i+1:]...)
			return nil
		}
	}
	return notFound("env var")
}

func (m *memStore) CreateRelease(_ context.Context, r Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases = append(m.releases, r)
	return nil
}

func (m *memStore) UpdateRelease(_ context.Context, r Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.releases {
		if m.releases[i].ID == r.ID && m.releases[i].TenantID == r.TenantID {
			m.releases[i] = r
			return nil
		}
	}
	return notFound("release")
}

func (m *memStore) GetRelease(_ context.Context, tenantID, id string) (Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.releases {
		if r.ID == id && r.TenantID == tenantID {
			return r, nil
		}
	}
	return Release{}, notFound("release")
}

func (m *memStore) ListReleases(_ context.Context, tenantID, appID string) ([]Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Release{}
	for _, r := range m.releases {
		if r.TenantID == tenantID && r.AppID == appID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStore) DeleteRelease(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.releases {
		if r.ID == id && r.TenantID == tenantID {
			m.releases = append(m.releases[:i], m.releases[i+1:]...)
			return nil
		}
	}
	return notFound("release")
}

func (m *memStore) PutOwnership(_ context.Context, o DomainOwnership) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.ownership {
		if m.ownership[i].Domain == o.Domain {
			m.ownership[i] = o
			return nil
		}
	}
	m.ownership = append(m.ownership, o)
	return nil
}

func (m *memStore) GetOwnership(_ context.Context, domain string) (DomainOwnership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.ownership {
		if o.Domain == domain {
			return o, nil
		}
	}
	return DomainOwnership{}, notFound("domain")
}

func (m *memStore) ListOwnership(_ context.Context, tenantID string) ([]DomainOwnership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []DomainOwnership{}
	for _, o := range m.ownership {
		if o.TenantID == tenantID {
			out = append(out, o)
		}
	}
	return out, nil
}

// call records one command a fake runner was asked to execute.
type call struct {
	name string
	args []string
}

// fakeRunner records commands instead of executing them. failOn makes the
// first command whose name and first argument match that prefix fail, which
// is how the config-check rollback path is exercised.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []call
	failOn string
	err    error
}

// Run records the command and applies the configured failure.
func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...)})
	joined := strings.Join(append([]string{name}, args...), " ")
	if r.failOn != "" && strings.Contains(joined, r.failOn) {
		if r.err != nil {
			return []byte("rejected"), r.err
		}
		return []byte("rejected"), ErrCommandFailed
	}
	return nil, nil
}

// ran reports whether any recorded command contains the fragment.
func (r *fakeRunner) ran(fragment string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.Contains(strings.Join(append([]string{c.name}, c.args...), " "), fragment) {
			return true
		}
	}
	return false
}

// fakeOrchestrator satisfies Orchestrator without a container runtime.
type fakeOrchestrator struct {
	mu        sync.Mutex
	deploys   int
	removed   []string
	replicas  int
	started   int
	stopped   int
	logLines  []string
	failNext  bool
	lastSpec  WorkloadSpec
	workloads []string
}

func (o *fakeOrchestrator) Deploy(_ context.Context, spec WorkloadSpec) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failNext {
		o.failNext = false
		return "", ErrCommandFailed
	}
	o.deploys++
	o.lastSpec = spec
	id := "workload-" + spec.ReleaseID
	o.workloads = append(o.workloads, id)
	return id, nil
}

func (o *fakeOrchestrator) Start(_ context.Context, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started++
	return nil
}

func (o *fakeOrchestrator) Stop(_ context.Context, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped++
	return nil
}

func (o *fakeOrchestrator) Scale(_ context.Context, _ string, replicas int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.replicas = replicas
	return nil
}

func (o *fakeOrchestrator) Remove(_ context.Context, workloadID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.removed = append(o.removed, workloadID)
	return nil
}

func (o *fakeOrchestrator) Logs(_ context.Context, _ string, _ int) ([]string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.logLines...), nil
}

// fakeProver answers domain ownership challenges from a fixed map.
type fakeProver struct {
	txt  map[string][]string
	http map[string]string
}

func (p fakeProver) TXTRecords(_ context.Context, name string) ([]string, error) {
	return p.txt[name], nil
}

func (p fakeProver) HTTPToken(_ context.Context, domain, _ string) (string, error) {
	return p.http[domain], nil
}

// fixedQuota reports the same ceiling for every resource.
type fixedQuota struct{ limit int64 }

func (q fixedQuota) Limit(_ context.Context, _, _ string) (int64, error) { return q.limit, nil }

// testEnv bundles a Service with the fakes backing it.
type testEnv struct {
	svc     *Service
	store   *memStore
	runner  *fakeRunner
	orch    *fakeOrchestrator
	prover  *fakeProver
	root    string
	counter int
}

// newTestEnv builds a Service rooted in a temp dir with deterministic clock
// and identifiers. Nothing here touches a real service or writes outside t.TempDir.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{
		store:  newMemStore(),
		runner: &fakeRunner{},
		orch:   &fakeOrchestrator{},
		prover: &fakeProver{txt: map[string][]string{}, http: map[string]string{}},
		root:   t.TempDir(),
	}
	clock := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	svc, err := New(Options{
		Root:              env.root,
		Store:             env.store,
		Runner:            env.runner,
		Orchestrator:      env.orch,
		Prover:            env.prover,
		EncryptionKey:     key,
		Nameservers:       []string{"ns1.example.net", "ns2.example.net"},
		Hostmaster:        "hostmaster",
		MailHostname:      "mail.example.net",
		DefaultPHPVersion: "8.3",
		Now:               func() time.Time { return clock },
		NewID: func() string {
			env.counter++
			return "id" + strings.Repeat("0", 3) + itoa(env.counter)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env.svc = svc
	return env
}

// itoa renders a small positive integer without importing strconv into every
// test file.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}

// verify marks a domain as owned and verified by a tenant.
func (e *testEnv) verify(t *testing.T, tenantID, domain string) {
	t.Helper()
	err := e.store.PutOwnership(context.Background(), DomainOwnership{
		Domain:   domain,
		TenantID: tenantID,
		Token:    "token",
		Method:   VerifyDNS,
		Verified: true,
	})
	if err != nil {
		t.Fatalf("PutOwnership: %v", err)
	}
}
