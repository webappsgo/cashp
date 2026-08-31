package api

import (
	"fmt"
	"net/http"
	"strings"
)

// ReadyResponse is the readiness payload. Like health it is a bare object so
// orchestrators can parse it without unwrapping an envelope.
type ReadyResponse struct {
	Ready  bool       `json:"ready"`
	Status string     `json:"status"`
	Checks ChecksInfo `json:"checks"`
}

// Ready serves the readiness probe. It reuses the health collector so
// readiness and health can never disagree about component state.
type Ready struct {
	health *Health
}

// NewReady builds the readiness handler on top of a health collector.
func NewReady(h *Health) *Ready {
	return &Ready{health: h}
}

// ServeHTTP renders readiness in the negotiated format, answering 503 for
// any state in which the instance must not receive traffic.
func (rh *Ready) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snapshot := rh.health.Snapshot(r.Context())
	status := StatusCode(snapshot.Status)
	resp := ReadyResponse{
		Ready:  status == http.StatusOK,
		Status: snapshot.Status,
		Checks: snapshot.Checks,
	}
	w.Header().Set("Cache-Control", "no-store")
	Write(w, r, status, Body{
		JSON:  resp,
		Text:  resp.RenderText(),
		Title: snapshot.Project.Name + " - Readiness",
	})
}

// RenderText renders readiness as dot-notation plain text.
func (rr ReadyResponse) RenderText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ready: %t\n", rr.Ready)
	fmt.Fprintf(&b, "status: %s\n", rr.Status)
	fmt.Fprintf(&b, "checks.database: %s\n", rr.Checks.Database)
	fmt.Fprintf(&b, "checks.cache: %s\n", rr.Checks.Cache)
	fmt.Fprintf(&b, "checks.disk: %s\n", rr.Checks.Disk)
	fmt.Fprintf(&b, "checks.scheduler: %s\n", rr.Checks.Scheduler)
	for _, opt := range []struct{ name, value string }{
		{"cluster", rr.Checks.Cluster},
		{"tor", rr.Checks.Tor},
		{"i2p", rr.Checks.I2P},
	} {
		if opt.value != "" {
			fmt.Fprintf(&b, "checks.%s: %s\n", opt.name, opt.value)
		}
	}
	for _, k := range sortedCheckKeys(rr.Checks.Extra) {
		fmt.Fprintf(&b, "checks.%s: %s\n", k, rr.Checks.Extra[k])
	}
	return b.String()
}
