package config

import (
	"strings"
	"testing"
)

func TestConstants(t *testing.T) {
	if InternalOrg != "webappsgo" {
		t.Errorf("InternalOrg = %q, want webappsgo", InternalOrg)
	}
	if InternalName != "cashp" {
		t.Errorf("InternalName = %q, want cashp", InternalName)
	}
	if DefaultConfigFileName != "server.yml" {
		t.Errorf("DefaultConfigFileName = %q, want server.yml", DefaultConfigFileName)
	}
	if DefaultPortRangeMin >= DefaultPortRangeMax {
		t.Errorf("port range min %d must be < max %d", DefaultPortRangeMin, DefaultPortRangeMax)
	}
}

func TestDirsContainOrgAndName(t *testing.T) {
	dirs := map[string]string{
		"ConfigDir": ConfigDir(),
		"DataDir":   DataDir(),
		"CacheDir":  CacheDir(),
		"LogDir":    LogDir(),
	}

	for name, dir := range dirs {
		if dir == "" {
			t.Errorf("%s() returned empty string", name)
		}
		if !strings.Contains(dir, InternalOrg) || !strings.Contains(dir, InternalName) {
			t.Errorf("%s() = %q, want it to contain %q and %q", name, dir, InternalOrg, InternalName)
		}
	}
}

func TestConfigFilePath(t *testing.T) {
	path := ConfigFilePath()
	if !strings.HasSuffix(path, DefaultConfigFileName) {
		t.Errorf("ConfigFilePath() = %q, want suffix %q", path, DefaultConfigFileName)
	}
	if !strings.HasPrefix(path, ConfigDir()) {
		t.Errorf("ConfigFilePath() = %q, want prefix %q", path, ConfigDir())
	}
}

func TestIsRoot(t *testing.T) {
	// Just exercise the function; the actual value depends on the test
	// runner's UID and is not asserted.
	_ = isRoot()
}
