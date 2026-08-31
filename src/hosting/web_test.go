package hosting

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// createTestSite provisions a verified site for a tenant.
func createTestSite(t *testing.T, env *testEnv, tenantID, name, domain string) Site {
	t.Helper()
	env.verify(t, tenantID, domain)
	site, err := env.svc.CreateSite(context.Background(), tenantID, CreateSiteRequest{
		Name:          name,
		PrimaryDomain: domain,
		PHPVersion:    "8.3",
	})
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	return site
}

func TestCreateSiteGeneratesVhostAndTree(t *testing.T) {
	env := newTestEnv(t)
	site := createTestSite(t, env, "tenant1", "shop", "shop.example.com")

	if site.DocRoot != "shop/public" {
		t.Fatalf("doc root = %q", site.DocRoot)
	}
	docRoot := filepath.Join(env.root, DirSites, "tenant1", "shop", "public")
	if _, err := os.Stat(filepath.Join(docRoot, "index.html")); err != nil {
		t.Fatalf("placeholder page missing: %v", err)
	}
	conf := filepath.Join(env.root, DirNginx, "tenant1", "shop.conf")
	content, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	rendered := string(content)
	for _, want := range []string{
		"server_name shop.example.com;",
		`root "` + docRoot + `";`,
		"fastcgi_pass unix:/run/php/php8.3-fpm.sock;",
		"server_tokens off;",
		"client_max_body_size 64m;",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("generated config missing %q:\n%s", want, rendered)
		}
	}
	if !env.runner.ran("nginx -t") || !env.runner.ran("nginx -s reload") {
		t.Fatal("nginx was not validated and reloaded")
	}

	again, err := env.svc.RenderSiteConfig(context.Background(), "tenant1", site.ID)
	if err != nil {
		t.Fatalf("RenderSiteConfig: %v", err)
	}
	if string(again) != rendered {
		t.Fatal("rendering the same site twice produced different output")
	}
}

func TestCreateSiteRejectsUnverifiedDomain(t *testing.T) {
	env := newTestEnv(t)
	_, err := env.svc.CreateSite(context.Background(), "tenant1", CreateSiteRequest{
		Name:          "shop",
		PrimaryDomain: "notmine.example.com",
	})
	if !apperr.Is(err, apperr.CodeForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	if len(env.runner.calls) != 0 {
		t.Fatal("a command ran for an unverified domain")
	}
}

func TestCreateSiteRejectsInjectionAttempts(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		name string
		req  CreateSiteRequest
	}{
		{"directive in domain", CreateSiteRequest{Name: "a", PrimaryDomain: "evil.com;}\nserver{listen 8080"}},
		{"traversal in name", CreateSiteRequest{Name: "../../etc", PrimaryDomain: "ok.example.com"}},
		{"slash in name", CreateSiteRequest{Name: "a/b", PrimaryDomain: "ok.example.com"}},
		{"brace in alias", CreateSiteRequest{Name: "a", PrimaryDomain: "ok.example.com", Aliases: []string{"x{}.com"}}},
		{"space in domain", CreateSiteRequest{Name: "a", PrimaryDomain: "ok.example.com root /etc"}},
		{"php version", CreateSiteRequest{Name: "a", PrimaryDomain: "ok.example.com", PHPVersion: "8.3; rm -rf /"}},
		{"git remote flag", CreateSiteRequest{Name: "a", PrimaryDomain: "ok.example.com", GitRemote: "--upload-pack=touch"}},
	}
	env.verify(t, "tenant1", "ok.example.com")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := env.svc.CreateSite(context.Background(), "tenant1", tc.req); err == nil {
				t.Fatal("hostile input was accepted")
			}
			if len(env.store.sites) != 0 {
				t.Fatal("hostile input created a site")
			}
		})
	}
}

func TestSiteTenantIsolation(t *testing.T) {
	env := newTestEnv(t)
	site := createTestSite(t, env, "tenant1", "shop", "shop.example.com")

	if _, err := env.svc.GetSite(context.Background(), "tenant2", site.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 read tenant1's site: %v", err)
	}
	php := "8.2"
	if _, err := env.svc.UpdateSite(context.Background(), "tenant2", site.ID, UpdateSiteRequest{PHPVersion: &php}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 mutated tenant1's site: %v", err)
	}
	if err := env.svc.DeleteSite(context.Background(), "tenant2", site.ID, true); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 deleted tenant1's site: %v", err)
	}
	if _, err := env.svc.DisableSite(context.Background(), "tenant2", site.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("tenant2 disabled tenant1's site: %v", err)
	}
	sites, err := env.svc.ListSites(context.Background(), "tenant2")
	if err != nil {
		t.Fatalf("ListSites: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("tenant2 sees %d sites of another tenant", len(sites))
	}
}

func TestDeleteSiteRequiresConfirmation(t *testing.T) {
	env := newTestEnv(t)
	site := createTestSite(t, env, "tenant1", "shop", "shop.example.com")

	if err := env.svc.DeleteSite(context.Background(), "tenant1", site.ID, false); !apperr.Is(err, apperr.CodeBadRequest) {
		t.Fatalf("delete without confirmation: %v", err)
	}
	if err := env.svc.DeleteSite(context.Background(), "tenant1", site.ID, true); err != nil {
		t.Fatalf("DeleteSite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.root, DirNginx, "tenant1", "shop.conf")); !os.IsNotExist(err) {
		t.Fatal("the server block survived the delete")
	}
}

func TestSiteConfigRollsBackWhenNginxRejectsIt(t *testing.T) {
	env := newTestEnv(t)
	env.verify(t, "tenant1", "shop.example.com")
	env.runner.failOn = "nginx -t"

	_, err := env.svc.CreateSite(context.Background(), "tenant1", CreateSiteRequest{
		Name:          "shop",
		PrimaryDomain: "shop.example.com",
	})
	if !apperr.Is(err, apperr.CodeValidation) {
		t.Fatalf("want validation error, got %v", err)
	}
	if strings.Contains(err.Error(), env.root) {
		t.Fatalf("error leaked a host path: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(env.root, DirNginx, "tenant1", "shop.conf")); !os.IsNotExist(statErr) {
		t.Fatal("the rejected config was left in place")
	}
	if len(env.store.sites) != 0 {
		t.Fatal("a site was persisted despite a rejected config")
	}
}

func TestEnableDisableSiteTogglesConfig(t *testing.T) {
	env := newTestEnv(t)
	site := createTestSite(t, env, "tenant1", "shop", "shop.example.com")
	conf := filepath.Join(env.root, DirNginx, "tenant1", "shop.conf")

	if _, err := env.svc.DisableSite(context.Background(), "tenant1", site.ID); err != nil {
		t.Fatalf("DisableSite: %v", err)
	}
	if _, err := os.Stat(conf); !os.IsNotExist(err) {
		t.Fatal("a disabled site kept its server block")
	}
	if _, err := env.svc.EnableSite(context.Background(), "tenant1", site.ID); err != nil {
		t.Fatalf("EnableSite: %v", err)
	}
	if _, err := os.Stat(conf); err != nil {
		t.Fatalf("an enabled site has no server block: %v", err)
	}
}

func TestSiteUsageCountsFiles(t *testing.T) {
	env := newTestEnv(t)
	site := createTestSite(t, env, "tenant1", "shop", "shop.example.com")
	body := make([]byte, 3*bytesPerMB)
	target := filepath.Join(env.root, DirSites, "tenant1", "shop", "public", "big.bin")
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	usage, err := env.svc.SiteUsage(context.Background(), "tenant1", site.ID)
	if err != nil {
		t.Fatalf("SiteUsage: %v", err)
	}
	if usage.DiskUsedMB < 3 {
		t.Fatalf("disk usage = %d MB", usage.DiskUsedMB)
	}
	if err = env.svc.SweepUsage(context.Background()); err != nil {
		t.Fatalf("SweepUsage: %v", err)
	}
	stored, err := env.svc.GetSite(context.Background(), "tenant1", site.ID)
	if err != nil {
		t.Fatalf("GetSite: %v", err)
	}
	if stored.DiskUsedMB < 3 {
		t.Fatalf("swept usage = %d MB", stored.DiskUsedMB)
	}
}

func TestSitePathsStayInsideTenantRoot(t *testing.T) {
	env := newTestEnv(t)
	site := Site{TenantID: "tenant1", Name: "shop", DocRoot: "../../../../etc/passwd"}
	if _, err := env.svc.siteDocRoot(site); err == nil {
		t.Fatal("a traversal doc root resolved")
	}
	site.DocRoot = "shop/public"
	resolved, err := env.svc.siteDocRoot(site)
	if err != nil {
		t.Fatalf("siteDocRoot: %v", err)
	}
	prefix := filepath.Join(env.root, DirSites, "tenant1") + string(os.PathSeparator)
	if !strings.HasPrefix(resolved, prefix) {
		t.Fatalf("doc root %q escaped %q", resolved, prefix)
	}
}
