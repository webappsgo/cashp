package nodes

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// publicCallback is an IP-literal URL in the TEST-NET-3 documentation range.
// Every URL in these tests is an IP literal on purpose: the SSRF guard only
// resolves DNS for named hosts, and this package's tests must never open a
// network connection.
const publicCallback = "https://203.0.113.10:8443/wake"

func TestSetCallbackURLIsManagedOnly(t *testing.T) {
	e := newEnv(t)
	managed, _ := e.enroll(t, RoleManaged, "managed-a")
	cluster, _ := e.enroll(t, RoleCluster, "cluster-a")

	if _, err := e.svc.SetCallbackURL(e.ctx, cluster.ID, publicCallback, "admin"); !errors.Is(err, ErrCallbackNotAllowed) {
		t.Fatalf("cluster node accepted a callback URL: %v", err)
	}

	updated, err := e.svc.SetCallbackURL(e.ctx, managed.ID, publicCallback, "admin")
	if err != nil {
		t.Fatalf("SetCallbackURL: %v", err)
	}
	if updated.CallbackURL != publicCallback {
		t.Fatalf("callback = %q", updated.CallbackURL)
	}

	cleared, err := e.svc.SetCallbackURL(e.ctx, managed.ID, "", "admin")
	if err != nil {
		t.Fatalf("SetCallbackURL(clear): %v", err)
	}
	if cleared.CallbackURL != "" {
		t.Fatalf("callback = %q, want cleared", cleared.CallbackURL)
	}
}

func TestSetCallbackURLRejectsSSRFTargets(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "managed-a")

	hostile := []string{
		"http://127.0.0.1/wake",
		"http://[::1]/wake",
		"http://10.0.0.5/wake",
		"http://192.168.1.2/wake",
		"http://172.16.4.4/wake",
		"http://169.254.169.254/latest/meta-data",
		"http://localhost/wake",
		"ftp://203.0.113.10/wake",
		"file:///etc/passwd",
		"not a url at all",
	}
	for _, raw := range hostile {
		if _, err := e.svc.SetCallbackURL(e.ctx, node.ID, raw, "admin"); !errors.Is(err, ErrCallbackNotAllowed) {
			t.Fatalf("SetCallbackURL(%q) error = %v, want ErrCallbackNotAllowed", raw, err)
		}
	}

	stored, err := e.svc.Get(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.CallbackURL != "" {
		t.Fatalf("a rejected URL was stored: %q", stored.CallbackURL)
	}
	if e.http.count() != 0 {
		t.Fatal("the SSRF guard let a request reach the transport")
	}
}

func TestNotifySendsAVerifiableSignedPush(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "managed-a")
	node, err := e.svc.SetCallbackURL(e.ctx, node.ID, publicCallback, "admin")
	if err != nil {
		t.Fatalf("SetCallbackURL: %v", err)
	}

	if err := e.svc.Notify(e.ctx, node); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if e.http.count() != 1 {
		t.Fatalf("transport saw %d requests, want 1", e.http.count())
	}

	req := e.http.last()
	if req.Method != http.MethodPost || req.URL.String() != publicCallback {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get(HeaderNodeID); got != node.ID {
		t.Fatalf("%s = %q", HeaderNodeID, got)
	}
	stamp := req.Header.Get(HeaderTimestamp)
	if stamp != strconv.FormatInt(e.clock.now().Unix(), 10) {
		t.Fatalf("%s = %q", HeaderTimestamp, stamp)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(string(body), "adm_") {
		t.Fatal("a credential leaked into the notification body")
	}

	key, err := e.svc.signingKey(e.ctx, node.ID)
	if err != nil {
		t.Fatalf("signingKey: %v", err)
	}
	signature := req.Header.Get(HeaderSignature)
	if !VerifyNotification(key, stamp, node.ID, body, signature, e.clock.now()) {
		t.Fatal("the push signature does not verify with the node's own key")
	}
	if strings.Contains(signature, key) {
		t.Fatal("the signing key leaked into the signature header")
	}
}

func TestNotifyFailureModes(t *testing.T) {
	e := newEnv(t)
	managed, _ := e.enroll(t, RoleManaged, "managed-a")
	cluster, _ := e.enroll(t, RoleCluster, "cluster-a")

	if err := e.svc.Notify(e.ctx, cluster); !errors.Is(err, ErrCallbackNotAllowed) {
		t.Fatalf("Notify(cluster) error = %v, want ErrCallbackNotAllowed", err)
	}
	if err := e.svc.Notify(e.ctx, managed); !errors.Is(err, ErrNoCallback) {
		t.Fatalf("Notify without a callback error = %v, want ErrNoCallback", err)
	}

	managed, err := e.svc.SetCallbackURL(e.ctx, managed.ID, publicCallback, "admin")
	if err != nil {
		t.Fatalf("SetCallbackURL: %v", err)
	}

	e.http.status = http.StatusInternalServerError
	if err := e.svc.Notify(e.ctx, managed); !errors.Is(err, ErrNotifyFailed) {
		t.Fatalf("Notify(500) error = %v, want ErrNotifyFailed", err)
	}

	e.http.status = 0
	e.http.err = errors.New("dial tcp 10.1.2.3:8443: connect: refused")
	err = e.svc.Notify(e.ctx, managed)
	if !errors.Is(err, ErrNotifyFailed) {
		t.Fatalf("Notify(transport error) error = %v, want ErrNotifyFailed", err)
	}
	if strings.Contains(err.Error(), "10.1.2.3") {
		t.Fatal("an internal address leaked into the caller-facing error")
	}
	e.http.err = nil

	// A stored URL that is no longer safe is refused at use, not only at
	// storage time.
	poisoned := managed
	poisoned.CallbackURL = "http://169.254.169.254/latest/meta-data"
	before := e.http.count()
	if err := e.svc.Notify(e.ctx, poisoned); !errors.Is(err, ErrCallbackNotAllowed) {
		t.Fatalf("Notify(link-local) error = %v, want ErrCallbackNotAllowed", err)
	}
	if e.http.count() != before {
		t.Fatal("a link-local push reached the transport")
	}
}

func TestNotifyWithoutTransport(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "managed-a")
	node, err := e.svc.SetCallbackURL(e.ctx, node.ID, publicCallback, "admin")
	if err != nil {
		t.Fatalf("SetCallbackURL: %v", err)
	}

	e.svc.http = nil
	if err := e.svc.Notify(e.ctx, node); !errors.Is(err, ErrTransportUnavailable) {
		t.Fatalf("Notify without a transport error = %v", err)
	}
}

func TestVerifyNotificationRejectsTamperingAndReplay(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stamp := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"node_id":"managed-a","event":"tasks_available","issued_at":1}`)
	key := "0123456789abcdef"
	signature := SignNotification(key, stamp, "managed-a", body)

	if !VerifyNotification(key, stamp, "managed-a", body, signature, now) {
		t.Fatal("a well-formed push failed to verify")
	}
	if !VerifyNotification(key, stamp, "managed-a", body, signature, now.Add(NotifySkew)) {
		t.Fatal("a push at exactly the skew bound failed to verify")
	}

	cases := []struct {
		name      string
		key       string
		stamp     string
		nodeID    string
		body      []byte
		signature string
		now       time.Time
	}{
		{"wrong key", "fedcba9876543210", stamp, "managed-a", body, signature, now},
		{"wrong node", key, stamp, "managed-b", body, signature, now},
		{"tampered body", key, stamp, "managed-a", append(body, ' '), signature, now},
		{"tampered signature", key, stamp, "managed-a", body, strings.Repeat("0", len(signature)), now},
		{"empty signature", key, stamp, "managed-a", body, "", now},
		{"unparsable timestamp", key, "not-a-number", "managed-a", body, signature, now},
		{"replayed later", key, stamp, "managed-a", body, signature, now.Add(NotifySkew + time.Second)},
		{"issued in the future", key, stamp, "managed-a", body, signature, now.Add(-NotifySkew - time.Second)},
	}
	for _, tc := range cases {
		if VerifyNotification(tc.key, tc.stamp, tc.nodeID, tc.body, tc.signature, tc.now) {
			t.Fatalf("%s: push verified but should not have", tc.name)
		}
	}
}

func TestDispatchNotifyFailureDoesNotFailDispatch(t *testing.T) {
	e := newEnv(t)
	node, _ := e.enroll(t, RoleManaged, "managed-a")
	if _, err := e.svc.SetCallbackURL(e.ctx, node.ID, publicCallback, "admin"); err != nil {
		t.Fatalf("SetCallbackURL: %v", err)
	}
	e.http.status = http.StatusBadGateway

	task, err := e.svc.Dispatch(e.ctx, DispatchRequest{
		NodeID: node.ID, Action: "agent.ping", Actor: "admin", Notify: true,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if task.State != TaskQueued {
		t.Fatalf("task = %+v", task)
	}
	if e.http.count() != 1 {
		t.Fatalf("transport saw %d pushes, want 1", e.http.count())
	}
}
