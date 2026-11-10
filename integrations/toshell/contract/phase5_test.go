package toshellcontract

import (
	"encoding/json"
	"os"
	"testing"
)

func loadSurface(t *testing.T) ToolSurface {
	t.Helper()
	raw, err := os.ReadFile("../tool_surface.json")
	if err != nil {
		t.Fatal(err)
	}
	var surface ToolSurface
	if err := json.Unmarshal(raw, &surface); err != nil {
		t.Fatal(err)
	}
	return surface
}

func TestPhase5AutonomousSurfaceBudget(t *testing.T) {
	surface := loadSurface(t)
	measurement, err := MeasureSurface(surface)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("phase5 worker surface: tools=%d bytes=%d tokens=%d safety=%d",
		measurement.ToolCount, measurement.JSONBytes, measurement.EstimatedTokens, measurement.EstimatedTokensWithSafety)
	if measurement.ToolCount != 10 {
		t.Fatalf("C2 tool surface = %d, want 10", measurement.ToolCount)
	}
	if measurement.EstimatedTokensWithSafety > surface.SchemaBudget {
		t.Fatalf("C2 tool surface exceeds schema budget: %+v", measurement)
	}
	if len(surface.PlannerDefault) != 3 {
		t.Fatalf("planner default tools = %d, want 3", len(surface.PlannerDefault))
	}
}

func TestPhase5NoUnsafeNativeTools(t *testing.T) {
	surface := loadSurface(t)
	forbidden := map[string]bool{
		"exec_module": true, "byovd_load": true, "byovd_unload": true,
		"byovd_kill": true, "ppl_kill": true, "edr_blind": true,
		"edr_kill": true, "persistence": true, "inject": true, "av-ops": true,
	}
	for _, tool := range surface.Tools {
		if forbidden[tool.Name] {
			t.Fatalf("unsafe native tool leaked into Ghoul surface: %s", tool.Name)
		}
	}
}
