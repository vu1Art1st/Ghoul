package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func TestFilterMCPTools(t *testing.T) {
	mk := func(name string) actool.CoreTool {
		return actool.Build(actool.Spec{
			Name: name,
			Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
				return actool.Text("ok"), nil
			},
		})
	}
	got := filterMCPTools([]actool.CoreTool{mk("mcp__Toshell__session_list"), mk("mcp__Toshell__exec")}, []string{"session_list"}, "Toshell")
	if len(got) != 1 || got[0].Name() != "mcp__Toshell__session_list" {
		t.Fatalf("filtered tools = %v", got)
	}
}

func TestResolveMCPEnvReferences(t *testing.T) {
	t.Setenv("TEST_MCP_TOKEN", "env-token")
	path := t.TempDir() + "/token"
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"X-Env":  "env:TEST_MCP_TOKEN",
		"X-File": "file:" + path,
	})
	env, err := resolveMCPEnv(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env["X-Env"] != "env-token" || env["X-File"] != "file-token" {
		t.Fatalf("resolved env = %#v", env)
	}
}
