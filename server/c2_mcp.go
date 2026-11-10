package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/Autumn-27/artex/db"
)

const toshellMCPName = "Toshell"

// Toshell exposes all registered MCP tools, including Confirm/Danger entries.
// The sidecar allowlist blocks writes, and this client-side allowlist keeps the
// model-facing surface read-only as well.
var toshellMCPReadTools = []string{
	"attack_suggest",
	"intel_query",
	"playbook_status",
	"plugin_list",
	"result_read",
	"session_context",
	"session_list",
	"task_result",
	"task_wait",
	"tool_download_status",
	"tool_list",
	"tunnel_list",
	"web_search",
}

// seedToshellMCP wires the sidecar's read-only MCP surface into Ghoul. It is
// enabled only when GHOUL_C2_MCP_URL is set, so native/external deployments can
// keep the generic MCP entry untouched.
func (s *Server) seedToshellMCP() {
	url := strings.TrimSpace(os.Getenv("GHOUL_C2_MCP_URL"))
	if url == "" || strings.TrimSpace(os.Getenv("GHOUL_C2_MCP_AUTOCONFIG")) == "false" {
		return
	}
	tokenRef := strings.TrimSpace(os.Getenv("GHOUL_C2_MCP_TOKEN_REF"))
	if tokenRef == "" {
		tokenRef = "file:/run/secrets/toshell_mcp_token"
	}
	headers, _ := json.Marshal(map[string]string{"X-MCP-Token": tokenRef})
	row := &dbMCPServer{
		Name:          toshellMCPName,
		Transport:     "http",
		URL:           url,
		Env:           headers,
		Enabled:       true,
		ToolAllowlist: append([]string(nil), toshellMCPReadTools...),
	}
	existing, err := s.findMCPByName(toshellMCPName)
	if err == nil && existing != nil {
		row.ID = existing.ID
		row.Enabled = existing.Enabled
	}
	if _, err := s.m.pg.SaveMCP(row.toDB()); err != nil {
		log.Printf("[c2] seed Toshell MCP: %v", err)
		return
	}
}

type dbMCPServer struct {
	ID            int64
	Name          string
	Transport     string
	URL           string
	Env           json.RawMessage
	Enabled       bool
	ToolAllowlist []string
}

func (r *dbMCPServer) toDB() *db.MCPServer {
	return &db.MCPServer{
		ID: r.ID, Name: r.Name, Transport: r.Transport, URL: r.URL,
		Env: r.Env, Enabled: r.Enabled, ToolAllowlist: r.ToolAllowlist,
	}
}

func (s *Server) c2MCPStatus(ctx context.Context) map[string]any {
	row, err := s.findMCPByName(toshellMCPName)
	if err != nil || row == nil {
		return map[string]any{"configured": false}
	}
	missing := missingMCPTools(row.Tools, toshellMCPReadTools, toshellMCPName)
	consoleURL := ""
	if s.c2 != nil {
		consoleURL = s.c2.cfg.ConsoleURL
	}
	return map[string]any{
		"configured":        true,
		"enabled":           row.Enabled,
		"url":               row.URL,
		"tool_count":        len(row.Tools),
		"allowlist_count":   len(row.ToolAllowlist),
		"expected_readonly": len(toshellMCPReadTools),
		"missing_tools":     missing,
		"console_url":       consoleURL,
		"version_hint":      "Update TOSHELL_VERSION, rebuild the sidecar, then refresh this MCP.",
	}
}

func missingMCPTools(cached, expected []string, serverName string) []string {
	known := make(map[string]bool, len(cached))
	for _, name := range cached {
		known[normalizeMCPToolName(name, serverName)] = true
	}
	missing := make([]string, 0)
	for _, name := range expected {
		name = strings.TrimSpace(name)
		if name != "" && !known[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

func normalizeMCPToolName(name, serverName string) string {
	name = strings.TrimSpace(name)
	prefix := "mcp__" + strings.TrimSpace(serverName) + "__"
	if strings.TrimSpace(serverName) != "" && strings.HasPrefix(name, prefix) {
		return strings.TrimPrefix(name, prefix)
	}
	return name
}
