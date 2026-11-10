package server

import "testing"

func TestMissingMCPToolsAcceptsPrefixedCacheNames(t *testing.T) {
	cached := []string{
		"mcp__Toshell__session_list",
		"mcp__Toshell__session_context",
		"mcp__Toshell__result_read",
	}
	expected := []string{"session_list", "session_context", "result_read"}
	if missing := missingMCPTools(cached, expected, toshellMCPName); len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
}

func TestMissingMCPToolsReportsOnlyActuallyMissingTools(t *testing.T) {
	cached := []string{"mcp__Toshell__session_list", "result_read"}
	expected := []string{"session_list", "session_context", "result_read"}
	missing := missingMCPTools(cached, expected, toshellMCPName)
	if len(missing) != 1 || missing[0] != "session_context" {
		t.Fatalf("missing = %v", missing)
	}
}
