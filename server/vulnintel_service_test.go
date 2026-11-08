package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaskVulnIntelSecret(t *testing.T) {
	if got := maskVulnIntelSecret(""); got != "" {
		t.Fatalf("empty secret: %q", got)
	}
	if got := maskVulnIntelSecret("abcd1234"); got != "********1234" {
		t.Fatalf("masked secret: %q", got)
	}
}

func TestNucleiTemplateStats(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.yaml"), []byte("id: one"), 0o600); err != nil {
		t.Fatal(err)
	}
	stats := nucleiTemplateStats(dir)
	if stats["exists"] != true || stats["templates"] != 1 {
		t.Fatalf("stats: %#v", stats)
	}
}
