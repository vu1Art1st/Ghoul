// Package toshellcontract defines the versioned Toshell REST contract used by
// Ghoul and the token-budget helpers shared by tests and the snapshot tool.
package toshellcontract

import (
	"encoding/json"
	"math"
	"strings"
)

// HealthSnapshot is the normalized /api/v1/health response.
type HealthSnapshot struct {
	Status       string       `json:"status"`
	Timestamp    string       `json:"timestamp"`
	ConfigReload ConfigReload `json:"config_reload"`
}

// ConfigReload is the health response's configuration reload status.
type ConfigReload struct {
	OK       bool   `json:"ok"`
	At       string `json:"at"`
	Attempts int    `json:"attempts"`
	Failures int    `json:"failures"`
}

// ToolSnapshot is one entry from /api/v1/mcp/tools.
type ToolSnapshot struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  []string       `json:"parameters"`
	Required    []string       `json:"required,omitempty"`
	Level       string         `json:"level"`
	Schema      map[string]any `json:"schema"`
}

// ToolsSnapshot is the complete /api/v1/mcp/tools response.
type ToolsSnapshot struct {
	Tools  []ToolSnapshot `json:"tools"`
	Count  int            `json:"count"`
	Levels map[string]int `json:"levels"`
	Notice string         `json:"notice"`
}

// SessionsSnapshot is the /api/v1/sessions response with an empty fixture.
type SessionsSnapshot struct {
	Sessions []json.RawMessage `json:"sessions"`
	Count    int               `json:"count"`
}

// LevelContract is the reviewed tool-to-risk-level mapping.
type LevelContract map[string][]string

// SurfaceParameter is one parameter in a Ghoul-native C2 tool schema.
type SurfaceParameter struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

// SurfaceTool is one Ghoul-native c2_* tool exposed to an agent role.
type SurfaceTool struct {
	Name         string                      `json:"name"`
	Description  string                      `json:"description"`
	Level        string                      `json:"level"`
	SourceTools  []string                    `json:"source_tools"`
	Planner      bool                        `json:"planner"`
	Worker       bool                        `json:"worker"`
	Required     []string                    `json:"required,omitempty"`
	Parameters   map[string]SurfaceParameter `json:"parameters"`
	Additional   bool                        `json:"additional_properties"`
	OutputBudget int                         `json:"output_token_budget"`
}

// ToolSurface is the reviewed 6-8 tool agent surface.
type ToolSurface struct {
	Version        int           `json:"version"`
	MaxTools       int           `json:"max_tools"`
	SchemaBudget   int           `json:"schema_token_budget"`
	OutputBudget   int           `json:"output_token_budget"`
	PlannerDefault []string      `json:"planner_default"`
	Tools          []SurfaceTool `json:"tools"`
}

// JSONSchema converts the compact surface definition to function-calling JSON Schema.
func (t SurfaceTool) JSONSchema() map[string]any {
	props := make(map[string]any, len(t.Parameters))
	for name, p := range t.Parameters {
		prop := map[string]any{"type": p.Type, "description": p.Description}
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		props[name] = prop
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": t.Additional,
	}
	if len(t.Required) > 0 {
		schema["required"] = t.Required
	}
	return schema
}

// Measurement is a reproducible character-based token estimate.
type Measurement struct {
	ToolCount                 int `json:"tool_count"`
	JSONBytes                 int `json:"json_bytes"`
	EstimatedTokens           int `json:"estimated_tokens"`
	EstimatedTokensWithSafety int `json:"estimated_tokens_with_safety"`
}

// SourceMetadata pins the Toshell source used for a contract snapshot.
type SourceMetadata struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	License string `json:"license"`
}

// TokenBaseline records both the full Toshell surface and the reviewed
// Ghoul-native surface so regressions are visible in review.
type TokenBaseline struct {
	Source             SourceMetadata `json:"source"`
	Estimator          string         `json:"estimator"`
	SafetyFactor       float64        `json:"safety_factor"`
	FullToshell        Measurement    `json:"full_toshell"`
	GhoulNativeSurface Measurement    `json:"ghoul_native_surface"`
	SchemaTokenBudget  int            `json:"schema_token_budget"`
	OutputTokenBudget  int            `json:"output_token_budget"`
	SurfaceUnderBudget bool           `json:"surface_under_budget"`
}

// EstimateTokens follows the same public calibration as Toshell's context
// estimator: CJK characters count as one token, all other characters as four
// characters per token, rounded up.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk := 0
	other := 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0x3400 && r <= 0x4DBF:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0x3040 && r <= 0x30FF:
		return true
	case r >= 0xAC00 && r <= 0xD7AF:
		return true
	case r >= 0x3000 && r <= 0x303F:
		return true
	case r >= 0xFF01 && r <= 0xFF60:
		return true
	}
	return false
}

// MeasureTools measures the complete serialized tool definitions.
func MeasureTools(tools []ToolSnapshot) (Measurement, error) {
	return measure(len(tools), tools)
}

// MeasureSurface measures the reviewed Ghoul-native tool definitions.
func MeasureSurface(surface ToolSurface) (Measurement, error) {
	return measure(len(surface.Tools), surface.Tools)
}

func measure(count int, value any) (Measurement, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return Measurement{}, err
	}
	tokens := EstimateTokens(string(b))
	return Measurement{
		ToolCount:                 count,
		JSONBytes:                 len(b),
		EstimatedTokens:           tokens,
		EstimatedTokensWithSafety: int(math.Ceil(float64(tokens) * 1.3)),
	}, nil
}

// HasSourceTool reports whether a Ghoul-native tool maps to a Toshell tool.
func HasSourceTool(tool SurfaceTool, name string) bool {
	for _, source := range tool.SourceTools {
		if strings.EqualFold(source, name) {
			return true
		}
	}
	return false
}
