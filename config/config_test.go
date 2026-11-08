package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPostgresDSNPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, []byte(`{"database":{"host":"10.1.2.3","port":6000,"user":"u","password":"p","dbname":"d","sslmode":"require"}}`), 0o644)
	t.Setenv("ARTEX_CONFIG", cfgPath)

	// no env DSN → built from config file fields
	t.Setenv("ARTEX_PG_DSN", "")
	got, _, err := PostgresDSN()
	want := "postgres://u:p@10.1.2.3:6000/d?sslmode=require"
	if err != nil || got != want {
		t.Fatalf("from file: got %q err %v want %q", got, err, want)
	}

	// env wins over config file
	t.Setenv("ARTEX_PG_DSN", "postgres://envwins/x")
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://envwins/x" {
		t.Fatalf("env should win, got %q err %v", got, err)
	}

	// no env, no file → error (no built-in default)
	t.Setenv("ARTEX_PG_DSN", "")
	t.Setenv("ARTEX_CONFIG", filepath.Join(dir, "nope.json"))
	if got, _, err := PostgresDSN(); err == nil {
		t.Fatalf("missing config should error, got %q", got)
	}

	// full dsn in config file is used verbatim
	os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://full/dsn"}}`), 0o644)
	t.Setenv("ARTEX_CONFIG", cfgPath)
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://full/dsn" {
		t.Fatalf("file dsn verbatim, got %q err %v", got, err)
	}
}

func TestC2DefaultsFailClosed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://example/db"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_CONFIG", cfgPath)

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
	t.Setenv("ARTEX_CONFIG", cfgPath)

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
