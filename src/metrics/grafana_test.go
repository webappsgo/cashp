package metrics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGrafanaDashboardCoversEveryCategory(t *testing.T) {
	r := New(Options{Namespace: "cashp"})

	dashboard, ok := r.GrafanaDashboard().(grafanaDashboard)
	if !ok {
		t.Fatal("GrafanaDashboard must return a dashboard definition")
	}

	if dashboard.SchemaVersion != grafanaSchemaVersion || dashboard.UID != "cashp-metrics" {
		t.Fatalf("dashboard identity = %d/%q", dashboard.SchemaVersion, dashboard.UID)
	}

	if len(dashboard.Templating.List) != 1 || dashboard.Templating.List[0].Type != "datasource" {
		t.Fatalf("datasource template variable missing: %+v", dashboard.Templating.List)
	}

	encoded, err := json.Marshal(dashboard)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	body := string(encoded)

	categories := []string{
		"cashp_" + MetricHTTPRequestsTotal,
		"cashp_" + MetricHTTPRequestDuration,
		"cashp_" + MetricDBQueriesTotal,
		"cashp_" + MetricCacheHitsTotal,
		"cashp_" + MetricSchedulerTasksTotal,
		"cashp_" + MetricSystemCPUUsagePercent,
		"cashp_" + MetricUsersTotal,
		"cashp_" + MetricAuthAttemptsTotal,
	}

	for _, want := range categories {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard has no panel for %s", want)
		}
	}

	for _, panel := range dashboard.Panels {
		if panel.Datasource.UID != grafanaDatasourceVar {
			t.Fatalf("panel %q pins a datasource: %q", panel.Title, panel.Datasource.UID)
		}

		if len(panel.Targets) == 0 {
			t.Fatalf("panel %q has no query", panel.Title)
		}
	}
}

func TestGrafanaPanelLayout(t *testing.T) {
	panels := buildGrafanaPanels([]panelSpec{
		{title: "one", targets: [][2]string{{"a", "a"}}},
		{title: "two", targets: [][2]string{{"b", "b"}}},
		{title: "three", targets: [][2]string{{"c", "c"}}},
	})

	if panels[0].GridPos.X != 0 || panels[1].GridPos.X != 12 || panels[2].GridPos.X != 0 {
		t.Fatalf("panels are not laid out two per row: %+v", panels)
	}

	if panels[2].GridPos.Y != 8 {
		t.Fatalf("second row y = %d, want 8", panels[2].GridPos.Y)
	}

	if panels[0].ID == panels[1].ID {
		t.Fatal("panel ids must be unique")
	}
}
