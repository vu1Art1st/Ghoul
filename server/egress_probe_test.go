package server

import (
	"testing"
)

func TestParseProbeExitIP(t *testing.T) {
	for _, body := range []string{`{"ip":"203.0.113.7"}`, `203.0.113.7`, "  203.0.113.7\n"} {
		if got := parseProbeExitIP([]byte(body)); got != "203.0.113.7" {
			t.Fatalf("parseProbeExitIP(%q) = %q", body, got)
		}
	}
	if got := parseProbeExitIP([]byte(`{"ip":"not-an-ip"}`)); got != "" {
		t.Fatalf("invalid IP accepted: %q", got)
	}
}

func TestValidateEgressProbeTargetRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/",
		"https://user:pass@example.com/",
		"https://127.0.0.1/",
		"https://[::1]/",
		"https://169.254.169.254/latest/meta-data/",
	} {
		if _, err := validateEgressProbeTarget(raw); err == nil {
			t.Fatalf("unsafe probe target accepted: %s", raw)
		}
	}
}
