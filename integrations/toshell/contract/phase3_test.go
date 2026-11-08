package toshellcontract

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPhase3PlannerReadonlySurfaceBudget(t *testing.T) {
	raw, err := os.ReadFile("../tool_surface.json")
	if err != nil {
		t.Fatal(err)
	}
	var surface ToolSurface
	if err := json.Unmarshal(raw, &surface); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, name := range surface.PlannerDefault {
		want[name] = true
	}
	filtered := ToolSurface{
		Version: surface.Version, MaxTools: len(want), SchemaBudget: surface.SchemaBudget,
		OutputBudget: surface.OutputBudget, PlannerDefault: surface.PlannerDefault,
	}
	for _, tool := range surface.Tools {
		if want[tool.Name] {
			filtered.Tools = append(filtered.Tools, tool)
		}
	}
	measurement, err := MeasureSurface(filtered)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("phase3 planner readonly surface: tools=%d bytes=%d tokens=%d safety=%d",
		measurement.ToolCount, measurement.JSONBytes, measurement.EstimatedTokens, measurement.EstimatedTokensWithSafety)
	if measurement.ToolCount != 3 {
		t.Fatalf("planner default tools = %d, want 3", measurement.ToolCount)
	}
	if measurement.EstimatedTokensWithSafety > surface.SchemaBudget {
		t.Fatalf("planner readonly surface exceeds schema budget: %+v", measurement)
	}
}
