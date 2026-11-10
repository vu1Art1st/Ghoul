package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/mcphttp"
	"github.com/Autumn-27/norma/mcp"
	actool "github.com/Autumn-27/norma/tool"
)

// mcpClient is the shared surface of a connected MCP server (stdio, Streamable HTTP,
// or legacy SSE),
// so tools/list and cleanup are handled uniformly regardless of transport.
type mcpClient interface {
	Tools(context.Context) ([]actool.CoreTool, error)
	Close() error
}

// connectMCP dials one MCP server per its transport. Callers must Close the client.
func connectMCP(ctx context.Context, m *db.MCPServer) (mcpClient, error) {
	env, err := resolveMCPEnv(m.Env)
	if err != nil {
		return nil, err
	}
	var cl mcpClient
	switch m.Transport {
	case "stdio":
		if m.Command == "" {
			return nil, fmt.Errorf("stdio 传输缺少命令")
		}
		cl, err = mcp.NewStdioClient(ctx, m.Name, m.Command, env, jsonStrSlice(m.Args)...)
	case "http":
		if m.URL == "" {
			return nil, fmt.Errorf("http 传输缺少 URL")
		}
		// env map doubles as HTTP headers (e.g. Authorization).
		cl, err = mcphttp.New(ctx, m.Name, m.URL, env, m.Insecure)
	case "sse":
		if m.URL == "" {
			return nil, fmt.Errorf("sse 传输缺少 URL")
		}
		cl, err = mcphttp.NewSSE(ctx, m.Name, m.URL, env, m.Insecure)
	default:
		return nil, fmt.Errorf("未知传输方式 %q", m.Transport)
	}
	if err != nil {
		return nil, err
	}
	if len(m.ToolAllowlist) == 0 {
		return cl, nil
	}
	tools, err := cl.Tools(ctx)
	if err != nil {
		_ = cl.Close()
		return nil, err
	}
	return &allowlistedMCPClient{inner: cl, tools: filterMCPTools(tools, m.ToolAllowlist, m.Name)}, nil
}

type allowlistedMCPClient struct {
	inner mcpClient
	tools []actool.CoreTool
}

func (c *allowlistedMCPClient) Tools(context.Context) ([]actool.CoreTool, error) {
	return append([]actool.CoreTool(nil), c.tools...), nil
}

func (c *allowlistedMCPClient) Close() error {
	return c.inner.Close()
}

func filterMCPTools(tools []actool.CoreTool, allowlist []string, serverName string) []actool.CoreTool {
	allowed := make(map[string]bool, len(allowlist))
	for _, name := range allowlist {
		if name = strings.TrimSpace(name); name != "" {
			allowed[name] = true
			if strings.TrimSpace(serverName) != "" {
				allowed["mcp__"+serverName+"__"+name] = true
			}
		}
	}
	filtered := make([]actool.CoreTool, 0, len(tools))
	for _, tool := range tools {
		if allowed[tool.Name()] {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// resolveMCPEnv resolves env:NAME and file:PATH references in MCP env/header
// values. This lets built-in integrations keep only a secret reference in the DB
// while still sending the real header at connection time.
func resolveMCPEnv(raw json.RawMessage) (map[string]string, error) {
	env := jsonStrMap(raw)
	for key, value := range env {
		resolved, err := resolveMCPValue(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		env[key] = resolved
	}
	return env, nil
}

func resolveMCPValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(value, "env:"))
		if name == "" {
			return "", fmt.Errorf("empty env reference")
		}
		resolved := strings.TrimSpace(os.Getenv(name))
		if resolved == "" {
			return "", fmt.Errorf("environment variable %s is empty", name)
		}
		return resolved, nil
	case strings.HasPrefix(value, "file:"):
		path := strings.TrimSpace(strings.TrimPrefix(value, "file:"))
		if path == "" {
			return "", fmt.Errorf("empty file reference")
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.Size() <= 0 || info.Size() > 64<<10 {
			return "", fmt.Errorf("secret file has invalid size")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("secret file must not be group/world accessible")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		resolved := strings.TrimSpace(string(raw))
		if resolved == "" {
			return "", fmt.Errorf("secret file is empty")
		}
		return resolved, nil
	default:
		return value, nil
	}
}

// discoverAndCacheMCP connects to one MCP, lists its tools, and persists the tool
// names to mcp_tools_cache so the UI shows them without a live connection.
func (s *Server) discoverAndCacheMCP(ctx context.Context, m *db.MCPServer) error {
	cl, err := connectMCP(ctx, m)
	if err != nil {
		return err
	}
	defer cl.Close()
	ts, err := cl.Tools(ctx)
	if err != nil {
		return err
	}
	tools := make([]db.MCPTool, 0, len(ts))
	for _, t := range ts {
		tools = append(tools, db.MCPTool{Name: t.Name(), Description: t.Description()})
	}
	if err := s.m.pg.SaveMCPTools(m.ID, tools); err != nil {
		return err
	}
	log.Printf("[mcp] %s 发现 %d 个工具并已缓存", m.Name, len(tools))
	return nil
}

// discoverEmptyMCPsOnStartup fills the tool cache for any enabled MCP that has none
// yet (notably the seeded browser MCP on first run). Runs sequentially in one
// goroutine so we never spawn many stdio servers (npx) at once, and never blocks
// startup. Best-effort: a failure leaves the cache empty to retry next start.
func (s *Server) discoverEmptyMCPsOnStartup() {
	servers, err := s.m.pg.ListMCP()
	if err != nil {
		log.Printf("[mcp] 启动自动发现: 读取列表失败: %v", err)
		return
	}
	for _, m := range servers {
		if !m.Enabled || len(m.Tools) > 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
		if err := s.discoverAndCacheMCP(ctx, m); err != nil {
			log.Printf("[mcp] 启动自动发现 %s 失败: %v", m.Name, err)
		}
		cancel()
	}
}
