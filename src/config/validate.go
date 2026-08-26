package config

import "fmt"

// Validate checks cfg for internal consistency. It never mutates cfg —
// callers apply defaults before calling Validate.
func Validate(cfg *Config) error {
	switch cfg.Mode {
	case "production", "development", "debug", "":
	default:
		return fmt.Errorf("config: invalid mode %q", cfg.Mode)
	}

	if cfg.Port != 0 {
		if cfg.Port < 0 || cfg.Port > 65535 {
			return fmt.Errorf("config: port %d out of range", cfg.Port)
		}
	}

	switch cfg.Database.Driver {
	case "sqlite", "postgres", "mysql", "mssql", "mongodb", "":
	default:
		return fmt.Errorf("config: unsupported database driver %q", cfg.Database.Driver)
	}

	return nil
}

// parsePort parses and range-checks a port string, rejecting anything
// outside 0-65535.
func parsePort(s string) (int, error) {
	var p int
	if _, err := fmt.Sscanf(s, "%d", &p); err != nil {
		return 0, err
	}
	if p < 0 || p > 65535 {
		return 0, fmt.Errorf("config: port %q out of range", s)
	}
	return p, nil
}
