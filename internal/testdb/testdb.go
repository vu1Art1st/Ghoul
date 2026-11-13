// Package testdb enforces an explicit, non-production PostgreSQL target for
// integration tests.
package testdb

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	// DSNEnv is the only supported database variable for test processes.
	DSNEnv = "GHOUL_TEST_PG_DSN"
	// GuardEnv switches config.PostgresDSN into fail-closed test mode.
	GuardEnv = "GHOUL_TEST_GUARD"
)

// ErrNotConfigured means integration tests should skip unless a test DSN is supplied.
var ErrNotConfigured = errors.New("GHOUL_TEST_PG_DSN is not configured")

// Configure enters test mode, rejecting a production database and making the
// validated test DSN available through the legacy GHOUL_PG_DSN variable used by
// existing test helpers.
func Configure() error {
	if err := os.Setenv(GuardEnv, "1"); err != nil {
		return fmt.Errorf("set %s: %w", GuardEnv, err)
	}
	dsn, err := ConfiguredDSN()
	if errors.Is(err, ErrNotConfigured) {
		_ = os.Unsetenv("GHOUL_PG_DSN")
		_ = os.Unsetenv("ARTEX_PG_DSN")
		return err
	}
	if err != nil {
		return err
	}
	if err := os.Setenv("GHOUL_PG_DSN", dsn); err != nil {
		return fmt.Errorf("set GHOUL_PG_DSN: %w", err)
	}
	_ = os.Unsetenv("ARTEX_PG_DSN")
	return nil
}

// ConfiguredDSN returns the validated test DSN.
func ConfiguredDSN() (string, error) {
	dsn := strings.TrimSpace(os.Getenv(DSNEnv))
	if dsn == "" {
		return "", ErrNotConfigured
	}
	if err := ValidateDSN(dsn); err != nil {
		return "", err
	}
	return dsn, nil
}

// ValidateDSN rejects non-PostgreSQL URLs and production database names.
func ValidateDSN(dsn string) error {
	name, err := DatabaseName(dsn)
	if err != nil {
		return err
	}
	if !IsTestDatabaseName(name) {
		return fmt.Errorf("refusing test database %q: expected ghoul_test, ghoul_test_* or ghoul_ci_*", name)
	}
	return nil
}

// DatabaseName extracts the effective database name from a PostgreSQL URL.
func DatabaseName(dsn string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return "", fmt.Errorf("parse test DSN: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("test DSN must use postgres:// or postgresql://")
	}
	name := strings.TrimPrefix(u.Path, "/")
	if queryName := strings.TrimSpace(u.Query().Get("dbname")); queryName != "" {
		if name != "" && name != queryName {
			return "", fmt.Errorf("test DSN has conflicting database names %q and %q", name, queryName)
		}
		name = queryName
	}
	if name == "" {
		return "", fmt.Errorf("test DSN does not name a database")
	}
	return name, nil
}

// IsTestDatabaseName reports whether name is reserved for test data.
func IsTestDatabaseName(name string) bool {
	return name == "ghoul_test" ||
		strings.HasPrefix(name, "ghoul_test_") ||
		strings.HasPrefix(name, "ghoul_ci_")
}
