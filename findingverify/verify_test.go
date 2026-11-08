package findingverify

import "testing"

func TestChecklistIncludesClassSpecificChecks(t *testing.T) {
	checks := Checklist("SQLi")
	if len(checks) <= len(generalChecks) {
		t.Fatalf("expected SQLi-specific checks, got %d", len(checks))
	}
	other := Checklist("unknown-class")
	if len(other) != len(generalChecks) {
		t.Fatalf("unknown class should only get general checks: %d", len(other))
	}
}

func TestPivotHints(t *testing.T) {
	hints := PivotHints([]string{"auth-bypass"}, "", 1)
	if len(hints) != 1 || hints[0] == "" {
		t.Fatalf("pivot hints: %#v", hints)
	}
	if hints := PivotHints(nil, "SSRF response differs", 2); len(hints) != 2 {
		t.Fatalf("summary fallback: %#v", hints)
	}
}
