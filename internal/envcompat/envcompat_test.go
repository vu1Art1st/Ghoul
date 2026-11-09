package envcompat

import "testing"

func TestGetPrefersPrimary(t *testing.T) {
	t.Setenv("GHOUL_TEST_VALUE", "new")
	t.Setenv("ARTEX_TEST_VALUE", "old")
	if got := Get("GHOUL_TEST_VALUE", "ARTEX_TEST_VALUE"); got != "new" {
		t.Fatalf("Get() = %q, want new", got)
	}
}

func TestGetFallsBackToLegacy(t *testing.T) {
	t.Setenv("GHOUL_TEST_VALUE", "")
	t.Setenv("ARTEX_TEST_VALUE", "old")
	if got := Get("GHOUL_TEST_VALUE", "ARTEX_TEST_VALUE"); got != "old" {
		t.Fatalf("Get() = %q, want old", got)
	}
}
