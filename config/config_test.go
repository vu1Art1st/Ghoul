package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresDSNPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, []byte(`{"database":{"host":"10.1.2.3","port":6000,"user":"u","password":"p","dbname":"d","sslmode":"require"}}`), 0o644)
	t.Setenv("GHOUL_CONFIG", cfgPath)
	t.Setenv("ARTEX_CONFIG", "")

	// no env DSN → built from config file fields
	t.Setenv("GHOUL_PG_DSN", "")
	t.Setenv("ARTEX_PG_DSN", "")
	got, _, err := PostgresDSN()
	want := "postgres://u:p@10.1.2.3:6000/d?sslmode=require"
	if err != nil || got != want {
		t.Fatalf("from file: got %q err %v want %q", got, err, want)
	}

	// env wins over config file
	t.Setenv("GHOUL_PG_DSN", "postgres://envwins/x")
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://envwins/x" {
		t.Fatalf("env should win, got %q err %v", got, err)
	}

	// no env, no file → error (no built-in default)
	t.Setenv("GHOUL_PG_DSN", "")
	t.Setenv("GHOUL_CONFIG", filepath.Join(dir, "nope.json"))
	if got, _, err := PostgresDSN(); err == nil {
		t.Fatalf("missing config should error, got %q", got)
	}

	// full dsn in config file is used verbatim
	os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://full/dsn"}}`), 0o644)
	t.Setenv("GHOUL_CONFIG", cfgPath)
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://full/dsn" {
		t.Fatalf("file dsn verbatim, got %q err %v", got, err)
	}
}

func TestLegacyEnvFallback(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://file/db"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOUL_CONFIG", "")
	t.Setenv("ARTEX_CONFIG", cfgPath)
	t.Setenv("GHOUL_PG_DSN", "")
	t.Setenv("ARTEX_PG_DSN", "postgres://legacy/db")

	got, source, err := PostgresDSN()
	if err != nil || got != "postgres://legacy/db" {
		t.Fatalf("legacy env fallback: got %q err %v", got, err)
	}
	if source == "" || !strings.Contains(source, "ARTEX_PG_DSN") {
		t.Fatalf("source should identify the legacy variable: %q", source)
	}
}

func TestPostgresDSNTestGuardRejectsProduction(t *testing.T) {
	t.Setenv("GHOUL_TEST_GUARD", "1")
	t.Setenv("GHOUL_TEST_PG_DSN", "postgres://ghoul:secret@db:5432/ghoul?sslmode=disable")
	t.Setenv("GHOUL_PG_DSN", "postgres://fallback/db")

	if _, _, err := PostgresDSN(); err == nil || !strings.Contains(err.Error(), "refusing test database") {
		t.Fatalf("production test DSN must be rejected, got %v", err)
	}

	t.Setenv("GHOUL_TEST_PG_DSN", "postgres://ghoul_test:secret@db:5432/ghoul_test?sslmode=disable")
	got, source, err := PostgresDSN()
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://ghoul_test:secret@db:5432/ghoul_test?sslmode=disable" ||
		!strings.Contains(source, "GHOUL_TEST_PG_DSN") {
		t.Fatalf("test DSN not selected: got=%q source=%q", got, source)
	}
}

func TestC2DefaultsFailClosed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://example/db"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOUL_CONFIG", cfgPath)
	t.Setenv("ARTEX_CONFIG", "")

	got := Load().EffectiveC2()
	if got.Provider != DefaultC2Provider {
		t.Fatalf("provider = %q, want %q", got.Provider, DefaultC2Provider)
	}
	if got.Enabled || got.AllowConfirm || got.AllowDanger {
		t.Fatalf("C2 defaults must be disabled: %+v", got)
	}
}

func TestC2HardeningDefaults(t *testing.T) {
	cfg := Config{C2: C2Config{Enabled: true, Provider: DefaultC2Provider, AllowConfirm: true, AllowDanger: true}}
	got := cfg.EffectiveC2()
	if got.ApprovalTTLSeconds != 300 || got.MaxOperationsPerMinute != 30 || got.OutputMaxBytes != 256<<10 {
		t.Fatalf("unsafe defaults: %+v", got)
	}
	if got.DangerApprovals != 2 {
		t.Fatalf("danger approvals = %d, want 2", got.DangerApprovals)
	}
	if !got.AllowDanger {
		t.Fatalf("explicit danger should remain enabled: %+v", got)
	}
}

func TestC2UnknownProviderDisablesWriteTiers(t *testing.T) {
	cfg := Config{C2: C2Config{
		Enabled:      true,
		Provider:     "other",
		AllowConfirm: true,
		AllowDanger:  true,
	}}
	got := cfg.EffectiveC2()
	if got.Enabled || got.AllowConfirm || got.AllowDanger {
		t.Fatalf("unknown provider must fail closed: %+v", got)
	}
}

func TestC2DangerRequiresConfirm(t *testing.T) {
	cfg := Config{C2: C2Config{
		Enabled:     true,
		Provider:    DefaultC2Provider,
		AllowDanger: true,
	}}
	got := cfg.EffectiveC2()
	if got.AllowDanger {
		t.Fatalf("danger must stay disabled without confirm: %+v", got)
	}
}

func TestC2ExplicitReadOnlyEnable(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	body := `{"database":{"dsn":"postgres://example/db"},"c2":{"enabled":true,"provider":"toshell","base_url":"http://127.0.0.1:18081","api_key_ref":"env:TOSHELL_API_KEY"}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOUL_CONFIG", cfgPath)
	t.Setenv("ARTEX_CONFIG", "")

	got := Load().EffectiveC2()
	if !got.Enabled || got.Provider != DefaultC2Provider {
		t.Fatalf("explicit read-only enable not preserved: %+v", got)
	}
	if got.AllowConfirm || got.AllowDanger {
		t.Fatalf("write tiers must remain disabled: %+v", got)
	}
	if got.APIKeyRef != "env:TOSHELL_API_KEY" {
		t.Fatalf("secret reference = %q", got.APIKeyRef)
	}
}

func TestC2EnvironmentOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"c2":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOUL_CONFIG", cfgPath)
	t.Setenv("ARTEX_CONFIG", "")
	t.Setenv("GHOUL_C2_ENABLED", "true")
	t.Setenv("GHOUL_C2_BASE_URL", "http://toshell:18081")
	t.Setenv("GHOUL_C2_CONSOLE_URL", "http://127.0.0.1:18081")
	t.Setenv("GHOUL_C2_API_KEY_REF", "file:/run/secrets/toshell_api_key")
	t.Setenv("GHOUL_C2_ALLOWED_HOSTS", "toshell, localhost")
	t.Setenv("GHOUL_C2_ALLOW_INSECURE_HTTP", "true")
	t.Setenv("GHOUL_C2_ALLOW_CONFIRM", "true")

	got := Load().EffectiveC2()
	if !got.Enabled || got.BaseURL != "http://toshell:18081" ||
		got.ConsoleURL != "http://127.0.0.1:18081" ||
		got.APIKeyRef != "file:/run/secrets/toshell_api_key" ||
		!got.AllowInsecureHTTP || !got.AllowConfirm || got.AllowDanger {
		t.Fatalf("environment overrides = %+v", got)
	}
	if len(got.AllowedHosts) != 2 || got.AllowedHosts[1] != "localhost" {
		t.Fatalf("allowed hosts = %#v", got.AllowedHosts)
	}
}
