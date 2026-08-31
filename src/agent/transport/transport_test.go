package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/security"
)

// adminToken is a syntactically valid admin agent token for the tests.
const adminToken = security.PrefixAdminAgent + "abcdefghijklmnopqrstuvwxyz012345"

// newTestClient builds a client pointed at one or more test servers.
func newTestClient(t *testing.T, primary string, cluster ...string) *Client {
	t.Helper()

	client, err := New(Options{
		Primary:    primary,
		Cluster:    cluster,
		Token:      adminToken,
		Version:    "test",
		APIVersion: config.DefaultAPIVersion,
		AdminPath:  "administration",
		Retry:      0,
		RetryDelay: time.Millisecond,
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// writeEnvelope replies with a successful envelope carrying data.
func writeEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()

	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal test data: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"ok":true,"data":` + string(encoded) + `}`)); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func TestScopeOf(t *testing.T) {
	body := strings.Repeat("a", 32)
	cases := []struct {
		token string
		want  Scope
		fails bool
	}{
		{security.PrefixAdminAgent + body, ScopeAdmin, false},
		{security.PrefixUserAgent + body, ScopeUser, false},
		{security.PrefixOrgAgent + body, ScopeOrg, false},
		{"adm_" + body, "", true},
		{"usr_" + body, "", true},
		{"not-a-token", "", true},
		{"", "", true},
	}

	for _, testCase := range cases {
		got, err := ScopeOf(testCase.token)
		if testCase.fails {
			if !errors.Is(err, ErrUnknownScope) {
				t.Errorf("ScopeOf(%q) error = %v, want ErrUnknownScope", testCase.token, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ScopeOf(%q): %v", testCase.token, err)
			continue
		}
		if got != testCase.want {
			t.Errorf("ScopeOf(%q) = %q, want %q", testCase.token, got, testCase.want)
		}
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	base := Options{
		Token:      adminToken,
		APIVersion: config.DefaultAPIVersion,
	}

	base.Primary = "ftp://panel.example.com"
	if _, err := New(base); err == nil {
		t.Error("New accepted a non-HTTP server URL")
	}

	base.Primary = "https://user:pass@panel.example.com"
	if _, err := New(base); err == nil {
		t.Error("New accepted a URL with embedded credentials")
	}

	base.Primary = "https://panel.example.com"
	base.Token = security.PrefixOrgAgent + strings.Repeat("a", 32)
	if _, err := New(base); !errors.Is(err, ErrOrgSlugRequired) {
		t.Errorf("New error = %v, want ErrOrgSlugRequired", err)
	}
}

func TestBasePathPerScope(t *testing.T) {
	admin := newTestClient(t, "https://panel.example.com")
	if got, want := admin.BasePath(), "server/administration/config/agents"; got != want {
		t.Errorf("admin BasePath = %q, want %q", got, want)
	}
	if got := admin.VersionedPath(admin.BasePath()); !strings.HasPrefix(got, "/api/"+config.DefaultAPIVersion+"/") {
		t.Errorf("VersionedPath = %q, want the configured API version", got)
	}

	user, err := New(Options{
		Primary:    "https://panel.example.com",
		Token:      security.PrefixUserAgent + strings.Repeat("b", 32),
		APIVersion: config.DefaultAPIVersion,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := user.BasePath(), "users/agents"; got != want {
		t.Errorf("user BasePath = %q, want %q", got, want)
	}

	org, err := New(Options{
		Primary:    "https://panel.example.com",
		Token:      security.PrefixOrgAgent + strings.Repeat("c", 32),
		OrgSlug:    "acme corp",
		APIVersion: config.DefaultAPIVersion,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := org.BasePath(); !strings.HasPrefix(got, "orgs/") || strings.Contains(got, " ") {
		t.Errorf("org BasePath = %q, want an encoded slug", got)
	}
}

func TestRegisterBuildsRequestAndDecodesEnvelope(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotAuth   string
		gotBody   registerRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")

		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := json.Unmarshal(payload, &gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}

		writeEnvelope(t, w, Registration{AgentID: "agt-1", Name: "web-01", Scope: "admin"})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	registration, err := client.Register(context.Background(), Identity{
		Hostname: "web-01",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "1.0.0",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if registration.AgentID != "agt-1" || registration.Name != "web-01" {
		t.Errorf("registration = %+v", registration)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if want := "/api/" + config.DefaultAPIVersion + "/server/administration/config/agents/register"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer "+adminToken {
		t.Errorf("authorization header = %q", gotAuth)
	}
	if gotBody.Hostname != "web-01" || gotBody.Token != adminToken {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestRegisterRequiresHostname(t *testing.T) {
	client := newTestClient(t, "https://panel.example.com")
	if _, err := client.Register(context.Background(), Identity{}); err == nil {
		t.Fatal("Register accepted an empty hostname")
	}
}

func TestErrorEnvelopeIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"ok":false,"error":"VALIDATION_ERROR","message":"hostname is required"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Register(context.Background(), Identity{Hostname: "web-01"})
	if err == nil {
		t.Fatal("expected an error for a failed envelope")
	}
	if !strings.Contains(err.Error(), "VALIDATION_ERROR") || !strings.Contains(err.Error(), "hostname is required") {
		t.Fatalf("error = %v, want the envelope code and message", err)
	}
}

func TestUnauthorizedIsNeverRetriedOrFailedOver(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a rejected credential must not be replayed against another node")
	}))
	defer backup.Close()

	client, err := New(Options{
		Primary:    server.URL,
		Cluster:    []string{backup.URL},
		Token:      adminToken,
		APIVersion: config.DefaultAPIVersion,
		Retry:      3,
		RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := client.Ping(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Ping error = %v, want ErrUnauthorized", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server was called %d times, want 1", got)
	}
}

func TestServerErrorFailsOverToClusterNode(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failing.Close()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, Cluster{Primary: "https://panel.example.com"})
	}))
	defer healthy.Close()

	client, err := New(Options{
		Primary:    failing.URL,
		Cluster:    []string{healthy.URL},
		Token:      adminToken,
		APIVersion: config.DefaultAPIVersion,
		Retry:      0,
		RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := client.Autodiscover(context.Background()); err != nil {
		t.Fatalf("Autodiscover: %v", err)
	}
	if client.ActiveServer() != healthy.URL {
		t.Fatalf("active server = %q, want the healthy node", client.ActiveServer())
	}
}

func TestAutodiscoverDiscardsUnusableURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != AutodiscoverPath {
			t.Errorf("autodiscover path = %q, want %q", r.URL.Path, AutodiscoverPath)
		}
		if _, err := w.Write([]byte(`{"ok":true,"data":{"primary":"file:///etc/passwd",` +
			`"cluster":["https://node-b.example.com","javascript:alert(1)"],` +
			`"agent_versions":{"linux-amd64":{"version":"2.0.0","sha256":"abc","url":"not a url"}}}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	cluster, err := client.Autodiscover(context.Background())
	if err != nil {
		t.Fatalf("Autodiscover: %v", err)
	}

	if cluster.Primary != "" {
		t.Errorf("primary = %q, want it discarded", cluster.Primary)
	}
	if len(cluster.Nodes) != 1 || cluster.Nodes[0] != "https://node-b.example.com" {
		t.Errorf("cluster nodes = %v", cluster.Nodes)
	}

	build, ok := cluster.BuildFor("linux-amd64")
	if !ok {
		t.Fatal("expected a published linux-amd64 build")
	}
	if build.URL != "" {
		t.Errorf("build URL = %q, want the unusable URL discarded", build.URL)
	}
	if _, ok := cluster.BuildFor("plan9-386"); ok {
		t.Error("BuildFor reported a build that was never published")
	}
}

func TestPollTasksRequiresRegistration(t *testing.T) {
	client := newTestClient(t, "https://panel.example.com")
	if _, err := client.PollTasks(context.Background(), ""); err == nil {
		t.Fatal("PollTasks accepted an empty agent id")
	}
	if err := client.SendHeartbeat(context.Background(), Heartbeat{}); err == nil {
		t.Fatal("SendHeartbeat accepted an empty agent id")
	}
	if err := client.ReportTaskResult(context.Background(), TaskResult{AgentID: "agt-1"}); err == nil {
		t.Fatal("ReportTaskResult accepted an empty task id")
	}
}

func TestPollTasksDecodesList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/agt-1/tasks") {
			t.Errorf("tasks path = %q", r.URL.Path)
		}
		writeEnvelope(t, w, []Task{{ID: "t1", AgentID: "agt-1", Action: "service.restart", Args: []string{"nginx"}}})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	tasks, err := client.PollTasks(context.Background(), "agt-1")
	if err != nil {
		t.Fatalf("PollTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" || tasks[0].Action != "service.restart" {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestSetClusterIgnoresInvalidPrimary(t *testing.T) {
	client := newTestClient(t, "https://panel.example.com")
	client.SetCluster("not-a-url", []string{"https://node-b.example.com", ""})

	if client.Primary() != "https://panel.example.com" {
		t.Errorf("primary = %q, want it unchanged", client.Primary())
	}
	if got := client.Cluster(); len(got) != 1 {
		t.Errorf("cluster = %v, want only the valid node", got)
	}
}
