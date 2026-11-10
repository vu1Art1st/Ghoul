package toshellcontract

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value T
	if err := json.Unmarshal(b, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}

func TestHealthContract(t *testing.T) {
	got := readJSON[HealthSnapshot](t, "snapshot-health.json")
	if got.Status != "ok" {
		t.Fatalf("health.status = %q, want ok", got.Status)
	}
	if got.Timestamp != "<captured-at>" {
		if _, err := time.Parse(time.RFC3339, got.Timestamp); err != nil {
			t.Fatalf("health.timestamp is not normalized or RFC3339: %q: %v", got.Timestamp, err)
		}
	}
	if !got.ConfigReload.OK {
		t.Fatalf("health.config_reload.ok = false")
	}
	if got.ConfigReload.At != "<captured-at>" {
		if _, err := time.Parse(time.RFC3339, got.ConfigReload.At); err != nil {
			t.Fatalf("health.config_reload.at is not normalized or RFC3339: %q: %v", got.ConfigReload.At, err)
		}
	}
	if got.ConfigReload.Failures != 0 {
		t.Fatalf("health.config_reload.failures = %d, want 0", got.ConfigReload.Failures)
	}
}

func TestSessionsContract(t *testing.T) {
	got := readJSON[SessionsSnapshot](t, "snapshot-sessions.json")
	if got.Count != len(got.Sessions) {
		t.Fatalf("sessions.count = %d, len(sessions) = %d", got.Count, len(got.Sessions))
	}
	if got.Count != 0 {
		t.Fatalf("empty sidecar fixture should expose 0 sessions, got %d", got.Count)
	}
}

func TestToshellToolLevelsAreFrozen(t *testing.T) {
	snapshot := readJSON[ToolsSnapshot](t, "snapshot-mcp-tools.json")
	contract := readJSON[LevelContract](t, "expected_levels.json")

	if snapshot.Count != 38 || len(snapshot.Tools) != 38 {
		t.Fatalf("tool count = snapshot.count %d / len %d, want 38", snapshot.Count, len(snapshot.Tools))
	}
	if snapshot.Levels["read"] != 13 || snapshot.Levels["confirm"] != 14 || snapshot.Levels["danger"] != 11 {
		t.Fatalf("unexpected levels summary: %#v", snapshot.Levels)
	}

	want := map[string]string{}
	for level, names := range contract {
		for _, name := range names {
			if prev, exists := want[name]; exists {
				t.Fatalf("tool %s appears in both %s and %s", name, prev, level)
			}
			want[name] = level
		}
	}
	if len(want) != 38 {
		t.Fatalf("level contract contains %d tools, want 38", len(want))
	}

	got := map[string]string{}
	for _, tool := range snapshot.Tools {
		if tool.Name == "" {
			t.Fatal("tool with empty name")
		}
		if _, exists := got[tool.Name]; exists {
			t.Fatalf("duplicate tool %s", tool.Name)
		}
		got[tool.Name] = tool.Level
		if utf8.RuneCountInString(tool.Description) < 20 {
			t.Errorf("tool %s has a short description", tool.Name)
		}
		if tool.Schema["type"] != "object" {
			t.Errorf("tool %s schema.type = %v, want object", tool.Name, tool.Schema["type"])
		}
		props, ok := tool.Schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("tool %s schema.properties is missing", tool.Name)
			continue
		}
		if len(props) != len(tool.Parameters) {
			t.Errorf("tool %s parameters/schema mismatch: %d/%d", tool.Name, len(tool.Parameters), len(props))
		}
		for _, name := range tool.Parameters {
			if _, ok := props[name]; !ok {
				t.Errorf("tool %s parameter %s missing from schema", tool.Name, name)
			}
		}
		paramSet := stringSet(tool.Parameters)
		for _, name := range tool.Required {
			if !paramSet[name] {
				t.Errorf("tool %s required parameter %s is not declared", tool.Name, name)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("tool count mismatch: got %d want %d", len(got), len(want))
	}
	for name, level := range want {
		if got[name] != level {
			t.Errorf("tool %s level = %q, want %q", name, got[name], level)
		}
	}
}

func TestGhoulToolSurfaceContract(t *testing.T) {
	toshell := readJSON[ToolsSnapshot](t, "snapshot-mcp-tools.json")
	surface := readJSON[ToolSurface](t, "../tool_surface.json")
	toshellNames := map[string]bool{}
	for _, tool := range toshell.Tools {
		toshellNames[tool.Name] = true
	}

	if surface.Version != 1 {
		t.Fatalf("surface.version = %d, want 1", surface.Version)
	}
	if surface.MaxTools != 10 || len(surface.Tools) > surface.MaxTools {
		t.Fatalf("surface size = %d, max = %d", len(surface.Tools), surface.MaxTools)
	}
	if len(surface.PlannerDefault) != 3 {
		t.Fatalf("planner default has %d tools, want 3", len(surface.PlannerDefault))
	}
	if surface.OutputBudget != 4000 || surface.SchemaBudget != 2000 {
		t.Fatalf("unexpected budgets: schema=%d output=%d", surface.SchemaBudget, surface.OutputBudget)
	}

	byName := map[string]SurfaceTool{}
	for _, tool := range surface.Tools {
		if !strings.HasPrefix(tool.Name, "c2_") {
			t.Errorf("native tool %q lacks c2_ prefix", tool.Name)
		}
		if _, exists := byName[tool.Name]; exists {
			t.Errorf("duplicate native tool %s", tool.Name)
		}
		byName[tool.Name] = tool
		switch tool.Level {
		case "read", "confirm", "danger":
		default:
			t.Errorf("native tool %s has invalid level %q", tool.Name, tool.Level)
		}
		if tool.OutputBudget > surface.OutputBudget {
			t.Errorf("native tool %s output budget %d exceeds %d", tool.Name, tool.OutputBudget, surface.OutputBudget)
		}
		for _, source := range tool.SourceTools {
			if !toshellNames[source] {
				t.Errorf("native tool %s references unknown Toshell tool %s", tool.Name, source)
			}
			switch source {
			case "exec_module", "plugin_upload", "plugin_load", "fileless_exec", "credentials", "session_kill":
				t.Errorf("operator-only or destructive source %s leaked into %s", source, tool.Name)
			}
		}
		for _, required := range tool.Required {
			if _, ok := tool.Parameters[required]; !ok {
				t.Errorf("native tool %s required parameter %s is not defined", tool.Name, required)
			}
		}
	}

	for _, name := range surface.PlannerDefault {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("planner default references unknown tool %s", name)
			continue
		}
		if !tool.Planner || tool.Level != "read" {
			t.Errorf("planner default %s must be a read tool marked planner", name)
		}
	}
	for name, tool := range byName {
		if tool.Level != "read" && tool.Planner {
			t.Errorf("non-read tool %s cannot be a planner default", name)
		}
	}
	if got := byName["c2_exec"].Level; got != "danger" {
		t.Fatalf("c2_exec level = %q, want danger", got)
	}
	if got := byName["c2_recon"].Level; got != "read" {
		t.Fatalf("c2_recon level = %q, want read", got)
	}
	if len(byName["c2_recon"].Parameters["action"].Enum) == 0 {
		t.Fatal("c2_recon must enumerate its read-only actions")
	}

	measurement, err := MeasureSurface(surface)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.EstimatedTokensWithSafety > surface.SchemaBudget {
		t.Fatalf("native surface uses %d estimated tokens with safety, budget %d",
			measurement.EstimatedTokensWithSafety, surface.SchemaBudget)
	}
}

func TestTokenBaselineMatchesSnapshots(t *testing.T) {
	baseline := readJSON[TokenBaseline](t, "token_baseline.json")
	source := readJSON[SourceMetadata](t, "../source.json")
	tools := readJSON[ToolsSnapshot](t, "snapshot-mcp-tools.json")
	surface := readJSON[ToolSurface](t, "../tool_surface.json")

	if baseline.Source.Project != source.Project ||
		baseline.Source.Version != source.Version ||
		baseline.Source.Commit != source.Commit ||
		baseline.Source.License != source.License {
		t.Fatalf("token baseline source does not match source.json: %#v", baseline.Source)
	}
	full, err := MeasureTools(tools.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.FullToshell != full {
		t.Fatalf("full Toshell measurement drifted: got %#v want %#v", full, baseline.FullToshell)
	}
	native, err := MeasureSurface(surface)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.GhoulNativeSurface != native {
		t.Fatalf("native surface measurement drifted: got %#v want %#v", native, baseline.GhoulNativeSurface)
	}
	if baseline.SchemaTokenBudget != surface.SchemaBudget || baseline.OutputTokenBudget != surface.OutputBudget {
		t.Fatalf("token baseline budgets do not match tool_surface.json")
	}
	if !baseline.SurfaceUnderBudget || native.EstimatedTokensWithSafety > surface.SchemaBudget {
		t.Fatalf("native surface exceeds its schema budget")
	}
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func TestExpectedLevelsAreSortedForReview(t *testing.T) {
	contract := readJSON[LevelContract](t, "expected_levels.json")
	for level, names := range contract {
		if !sort.StringsAreSorted(names) {
			t.Errorf("%s tool list is not sorted; keep review diffs stable", level)
		}
	}
}
