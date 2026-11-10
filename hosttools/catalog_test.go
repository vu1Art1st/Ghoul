package hosttools

import "testing"

func TestCatalogIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range catalog {
		if tool.ID == "" || seen[tool.ID] {
			t.Fatalf("invalid or duplicate tool id %q", tool.ID)
		}
		seen[tool.ID] = true
		if tool.Kind == KindPython && tool.Package == "" {
			t.Fatalf("%s has no package", tool.ID)
		}
	}
}

func TestValidatePackageSpec(t *testing.T) {
	for _, valid := range []string{"ddddocr", "ddddocr==1.5.6", "requests>=2.31,<3", "package[extra]"} {
		if err := ValidatePackageSpec(valid); err != nil {
			t.Errorf("%q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "-r requirements.txt", "ddddocr;id", "../../x", "git+https://x", "ddddocr --index-url http://x"} {
		if err := ValidatePackageSpec(invalid); err == nil {
			t.Errorf("%q accepted", invalid)
		}
	}
}

func TestLookupRejectsUnknownTool(t *testing.T) {
	if _, ok := Lookup("sh"); ok {
		t.Fatal("shell must never be an install tool")
	}
	if _, ok := Lookup("ddddocr"); !ok {
		t.Fatal("ddddocr should be in catalog")
	}
}
