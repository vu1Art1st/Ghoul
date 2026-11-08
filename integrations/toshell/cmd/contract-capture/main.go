// Command contract-capture starts an isolated Toshell sidecar, captures the
// Phase 0 REST responses, and refreshes the reviewed token baseline.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	toshellcontract "github.com/Autumn-27/artex/integrations/toshell/contract"
)

const contractAPIKey = "ghoul-contract-capture-key"

type captureOptions struct {
	serverExe string
	workDir   string
	outDir    string
	surface   string
	apiPort   int
	version   string
	commit    string
}

func main() {
	var opts captureOptions
	flag.StringVar(&opts.serverExe, "server-exe", "", "path to a built Toshell server binary")
	flag.StringVar(&opts.workDir, "work-dir", "", "temporary sidecar directory (default: OS temp)")
	flag.StringVar(&opts.outDir, "out-dir", "integrations/toshell/contract", "snapshot output directory")
	flag.StringVar(&opts.surface, "surface", "integrations/toshell/tool_surface.json", "Ghoul-native tool surface")
	flag.IntVar(&opts.apiPort, "api-port", 0, "Toshell API port (0 chooses a free port)")
	flag.StringVar(&opts.version, "toshell-version", "v1.4.3", "pinned Toshell version")
	flag.StringVar(&opts.commit, "toshell-commit", "8bf4643c1affbe109993123475422f6d26897903", "pinned Toshell commit")
	flag.Parse()

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "contract-capture:", err)
		os.Exit(1)
	}
}

func run(opts captureOptions) error {
	if strings.TrimSpace(opts.serverExe) == "" {
		return errors.New("-server-exe is required")
	}
	serverExe, err := filepath.Abs(opts.serverExe)
	if err != nil {
		return err
	}
	if _, err := os.Stat(serverExe); err != nil {
		return fmt.Errorf("server executable: %w", err)
	}

	workDir := opts.workDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "ghoul-toshell-contract-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(workDir)
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.outDir, 0o755); err != nil {
		return err
	}

	apiPort := opts.apiPort
	if apiPort == 0 {
		apiPort, err = freePort()
		if err != nil {
			return err
		}
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(apiPort)
	cfgPath := filepath.Join(workDir, "server.yaml")
	if err := os.WriteFile(cfgPath, []byte(sidecarConfig(workDir, apiPort)), 0o600); err != nil {
		return err
	}

	stdout, err := os.Create(filepath.Join(workDir, "server.stdout.log"))
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(workDir, "server.stderr.log"))
	if err != nil {
		return err
	}
	defer stderr.Close()

	cmd := exec.Command(serverExe, "-config", cfgPath)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Toshell: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer stopProcess(cmd, done)

	client := &http.Client{Timeout: 5 * time.Second}
	if err := waitForHealth(client, baseURL, done, 30*time.Second); err != nil {
		tail, _ := os.ReadFile(filepath.Join(workDir, "server.stderr.log"))
		return fmt.Errorf("%w; stderr tail: %s", err, strings.TrimSpace(string(tail)))
	}

	healthRaw, err := fetch(client, baseURL+"/api/v1/health", "")
	if err != nil {
		return err
	}
	toolsRaw, err := fetch(client, baseURL+"/api/v1/mcp/tools", contractAPIKey)
	if err != nil {
		return err
	}
	sessionsRaw, err := fetch(client, baseURL+"/api/v1/sessions", contractAPIKey)
	if err != nil {
		return err
	}

	var health map[string]any
	if err := json.Unmarshal(healthRaw, &health); err != nil {
		return fmt.Errorf("decode health: %w", err)
	}
	health["timestamp"] = "<captured-at>"
	if reload, ok := health["config_reload"].(map[string]any); ok {
		reload["at"] = "<captured-at>"
	}

	var tools toshellcontract.ToolsSnapshot
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		return fmt.Errorf("decode tools: %w", err)
	}
	var sessions toshellcontract.SessionsSnapshot
	if err := json.Unmarshal(sessionsRaw, &sessions); err != nil {
		return fmt.Errorf("decode sessions: %w", err)
	}
	surfaceBytes, err := os.ReadFile(opts.surface)
	if err != nil {
		return fmt.Errorf("read tool surface: %w", err)
	}
	var surface toshellcontract.ToolSurface
	if err := json.Unmarshal(surfaceBytes, &surface); err != nil {
		return fmt.Errorf("decode tool surface: %w", err)
	}

	full, err := toshellcontract.MeasureTools(tools.Tools)
	if err != nil {
		return err
	}
	native, err := toshellcontract.MeasureSurface(surface)
	if err != nil {
		return err
	}
	baseline := toshellcontract.TokenBaseline{
		Source: toshellcontract.SourceMetadata{
			Project: "Toshell",
			Version: opts.version,
			Commit:  opts.commit,
			License: "MIT",
		},
		Estimator:          "CJK rune = 1 token; other rune = 1/4 token; rounded up; 1.3 safety factor",
		SafetyFactor:       1.3,
		FullToshell:        full,
		GhoulNativeSurface: native,
		SchemaTokenBudget:  surface.SchemaBudget,
		OutputTokenBudget:  surface.OutputBudget,
		SurfaceUnderBudget: native.EstimatedTokensWithSafety <= surface.SchemaBudget,
	}

	if err := writeJSON(filepath.Join(opts.outDir, "snapshot-health.json"), health); err != nil {
		return err
	}
	if err := writeRawJSON(filepath.Join(opts.outDir, "snapshot-mcp-tools.json"), toolsRaw); err != nil {
		return err
	}
	if err := writeRawJSON(filepath.Join(opts.outDir, "snapshot-sessions.json"), sessionsRaw); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(opts.outDir, "token_baseline.json"), baseline); err != nil {
		return err
	}

	fmt.Printf("captured %s (%s)\n", baseURL, strings.TrimSpace(opts.commit))
	fmt.Printf("full Toshell: %d tools, %d estimated tokens (+safety %d)\n",
		full.ToolCount, full.EstimatedTokens, full.EstimatedTokensWithSafety)
	fmt.Printf("Ghoul surface: %d tools, %d estimated tokens (+safety %d), budget %d\n",
		native.ToolCount, native.EstimatedTokens, native.EstimatedTokensWithSafety, surface.SchemaBudget)
	return nil
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func sidecarConfig(workDir string, apiPort int) string {
	dbPath := filepath.ToSlash(filepath.Join(workDir, "data", "toshell.db"))
	return fmt.Sprintf(`# Generated by contract-capture. Contains test-only credentials.
server:
  host: 127.0.0.1
  port: %d
  api_host: 127.0.0.1
  api_port: %d

listener:
  enabled: false
  host: 127.0.0.1
  port: %d
  protocol: tcp
  encryption_key: 0123456789abcdef0123456789abcdef

auth:
  enabled: true
  admin_username: admin
  admin_password: contract-admin-only
  jwt_enabled: true
  jwt_key: contract-jwt-key-not-for-production
  api_key_enabled: true
  api_keys:
    - %s

database:
  type: sqlite
  path: %q

logging:
  level: info
  format: text
  output: stdout

web:
  basic_auth_enabled: false
  unauth_mode: basic

ai:
  enabled: false

mcp:
  enabled: false
`, apiPort, apiPort, apiPort+1, contractAPIKey, dbPath)
}

func waitForHealth(client *http.Client, baseURL string, done <-chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err == nil {
				return errors.New("Toshell exited before becoming healthy")
			}
			return fmt.Errorf("Toshell exited before becoming healthy: %w", err)
		default:
		}
		resp, err := client.Get(baseURL + "/api/v1/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("Toshell health check timed out")
}

func fetch(client *http.Client, endpoint, apiKey string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func stopProcess(cmd *exec.Cmd, done <-chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	select {
	case <-done:
		return
	default:
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func writeRawJSON(path string, raw []byte) error {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact.Bytes(), "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	return os.WriteFile(path, pretty.Bytes(), 0o644)
}
