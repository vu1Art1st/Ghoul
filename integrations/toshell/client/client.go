// Package client is the server-side Toshell REST client. It deliberately exposes
// only read endpoints used by Phase 3; write operations stay behind the later
// policy/approval layers.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

var (
	ErrDisabled       = errors.New("Toshell C2 adapter is disabled")
	ErrMissingAPIKey  = errors.New("Toshell API key reference is not configured")
	ErrBadAPIKeyRef   = errors.New("Toshell API key must be an env: or file: reference")
	ErrUpstreamStatus = errors.New("Toshell request failed")
)

type Config struct {
	BaseURL           string
	APIKeyRef         string
	Timeout           time.Duration
	AllowedHosts      []string
	AllowInsecureHTTP bool
	TLSCAFile         string
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type Health struct {
	OK        bool            `json:"ok"`
	Version   string          `json:"version,omitempty"`
	Commit    string          `json:"commit,omitempty"`
	Status    string          `json:"status,omitempty"`
	UptimeSec int64           `json:"uptime_sec,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

type Session struct {
	ID            string    `json:"id"`
	Hostname      string    `json:"hostname"`
	Username      string    `json:"username"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	PID           uint32    `json:"pid"`
	ProcessName   string    `json:"process_name"`
	ProcessPath   string    `json:"process_path"`
	IPAddresses   []string  `json:"ip_addresses"`
	MACAddresses  []string  `json:"mac_addresses"`
	Domain        string    `json:"domain"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	Status        string    `json:"status"`
	Listener      string    `json:"listener"`
	ListenerID    string    `json:"listener_id,omitempty"`
	RemoteAddr    string    `json:"remote_addr"`
	ParentRelay   string    `json:"parent_relay,omitempty"`
	CPUUsage      float32   `json:"cpu_usage"`
	MemoryUsed    uint64    `json:"memory_used"`
	ActiveModules []string  `json:"active_modules"`
	Comment       string    `json:"comment,omitempty"`
}

type Task struct {
	ID          uint64     `json:"id"`
	SessionID   string     `json:"session_id"`
	TaskType    string     `json:"task_type"`
	Command     string     `json:"command"`
	Args        []string   `json:"args"`
	ExecuteType string     `json:"execute_type"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Output      string     `json:"output,omitempty"`
	OutputB64   string     `json:"output_b64,omitempty"`
	OutputBytes int        `json:"output_bytes,omitempty"`
	Error       string     `json:"error,omitempty"`
	ExitCode    int32      `json:"exit_code"`
	Timeout     uint32     `json:"timeout"`
	Path        string     `json:"path,omitempty"`
	PID         uint32     `json:"pid,omitempty"`
	Data        string     `json:"data,omitempty"`
	Progress    int        `json:"progress,omitempty"`
}

type TaskRef struct {
	TaskID   uint64         `json:"task_id"`
	TaskType string         `json:"task_type,omitempty"`
	Message  string         `json:"message,omitempty"`
	Path     string         `json:"path,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

type Tunnel struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	Listening   bool   `json:"listening"`
	LocalPort   int    `json:"local_port"`
	Protocol    string `json:"protocol,omitempty"`
	Mode        string `json:"mode,omitempty"`
	TargetAddr  string `json:"target_addr,omitempty"`
	Remark      string `json:"remark,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	SessionHost string `json:"session_host,omitempty"`
	SessionUser string `json:"session_user,omitempty"`
}

type Listener struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Protocol    string `json:"protocol"`
	BindAddr    string `json:"bind_addr"`
	BindPort    int    `json:"bind_port"`
	PublicAddr  string `json:"public_addr"`
	Status      string `json:"status"`
	Connections int    `json:"connections"`
	CreatedAt   int64  `json:"created_at"`
	Options     string `json:"options,omitempty"`
}

type CreateListenerRequest struct {
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Protocol   string         `json:"protocol,omitempty"`
	BindAddr   string         `json:"bind_addr,omitempty"`
	BindPort   int            `json:"bind_port,omitempty"`
	PublicAddr string         `json:"public_addr,omitempty"`
	Options    map[string]any `json:"options,omitempty"`
}

type BuildResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Format      string `json:"format"`
	Size        int    `json:"size"`
	SHA256      string `json:"sha256"`
	BuildTime   string `json:"build_time"`
	DownloadURL string `json:"download_url"`
	HostedURL   string `json:"hosted_url"`
	Signed      bool   `json:"signed"`
}

type ChannelHealth struct {
	Channels     []Channel `json:"channels"`
	TotalOnline  int       `json:"total_online"`
	TotalSession int       `json:"total_session"`
	Timestamp    int64     `json:"timestamp"`
}

type Channel struct {
	Type      string `json:"type"`
	Online    int    `json:"online"`
	Total     int    `json:"total_session"`
	Listeners int    `json:"listeners"`
	Running   bool   `json:"running"`
}

type Event struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Time    int64           `json:"time"`
}

type sessionsResponse struct {
	Sessions []Session `json:"sessions"`
	Count    int       `json:"count"`
}

type tasksResponse struct {
	Tasks []Task `json:"tasks"`
	Count int    `json:"count"`
}

type listenersResponse struct {
	Listeners []Listener `json:"listeners"`
	Count     int        `json:"count"`
}

func ResolveAPIKey(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ErrMissingAPIKey
	}
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(ref, "env:"))
		if name == "" {
			return "", ErrMissingAPIKey
		}
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("%w: environment variable %s is empty", ErrMissingAPIKey, name)
		}
		return value, nil
	case strings.HasPrefix(ref, "file:"):
		path := strings.TrimSpace(strings.TrimPrefix(ref, "file:"))
		if path == "" {
			return "", ErrMissingAPIKey
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("%w: stat %s: %v", ErrMissingAPIKey, path, err)
		}
		if info.Size() <= 0 || info.Size() > 64<<10 {
			return "", fmt.Errorf("%w: file %s has an invalid size", ErrMissingAPIKey, path)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("%w: file %s must not be group/world accessible", ErrMissingAPIKey, path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%w: read %s: %v", ErrMissingAPIKey, path, err)
		}
		value := strings.TrimSpace(string(raw))
		if value == "" {
			return "", fmt.Errorf("%w: file %s is empty", ErrMissingAPIKey, path)
		}
		return value, nil
	default:
		return "", ErrBadAPIKeyRef
	}
}

func New(cfg Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("Toshell base_url is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid Toshell base_url %q", cfg.BaseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("Toshell base_url must use http or https")
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) && !cfg.AllowInsecureHTTP {
		return nil, errors.New("non-loopback Toshell base_url requires https or c2.allow_insecure_http")
	}
	if !hostAllowed(u.Hostname(), cfg.AllowedHosts) {
		return nil, fmt.Errorf("Toshell host %q is not in c2.allowed_hosts", u.Hostname())
	}
	apiKey, err := ResolveAPIKey(cfg.APIKeyRef)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // the sidecar connection must never inherit an agent proxy
	if u.Scheme == "https" {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if strings.TrimSpace(cfg.TLSCAFile) != "" {
			ca, err := os.ReadFile(cfg.TLSCAFile)
			if err != nil {
				return nil, fmt.Errorf("read c2.tls_ca_file: %w", err)
			}
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(ca) {
				return nil, errors.New("c2.tls_ca_file contains no valid certificates")
			}
			tlsCfg.RootCAs = pool
		}
		transport.TLSClientConfig = tlsCfg
	}
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 || !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				return errors.New("Toshell redirects must stay on the configured host")
			}
			return nil
		},
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    httpClient,
	}, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if len(allowed) == 0 {
		return isLoopbackHost(host)
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), host) {
			return true
		}
	}
	return false
}

func (c *Client) BaseURL() string { return c.baseURL }

// AuthHeaders returns the server-only authentication header for WebSocket
// upgrade requests. Callers must never expose this value to browsers or agents.
func (c *Client) AuthHeaders() http.Header {
	return http.Header{"X-API-Key": []string{c.apiKey}}
}

func (c *Client) EventsURL() (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/ws/events"
	return u.String(), nil
}

func (c *Client) Health(ctx context.Context) (*Health, error) {
	var out Health
	raw, err := c.get(ctx, "/api/v1/health", &out)
	if err != nil {
		return nil, err
	}
	out.Raw = raw
	return &out, nil
}

func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	var out sessionsResponse
	if _, err := c.get(ctx, "/api/v1/sessions", &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func (c *Client) Session(ctx context.Context, id string) (*Session, error) {
	var out Session
	if _, err := c.get(ctx, "/api/v1/sessions/"+url.PathEscape(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Tasks(ctx context.Context, sessionID string) ([]Task, error) {
	path := "/api/v1/tasks"
	if strings.TrimSpace(sessionID) != "" {
		path += "?session_id=" + url.QueryEscape(sessionID)
	}
	var out tasksResponse
	if _, err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

func (c *Client) Task(ctx context.Context, id string) (*Task, error) {
	var out Task
	if _, err := c.get(ctx, "/api/v1/tasks/"+url.PathEscape(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Listeners(ctx context.Context) ([]Listener, error) {
	var out listenersResponse
	if _, err := c.get(ctx, "/api/v1/listeners", &out); err != nil {
		return nil, err
	}
	return out.Listeners, nil
}

func (c *Client) CreateListener(ctx context.Context, req CreateListenerRequest) (*Listener, error) {
	var out Listener
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/listeners", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) StartListener(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/listeners/"+url.PathEscape(id)+"/start", nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) StopListener(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/listeners/"+url.PathEscape(id)+"/stop", nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) DeleteListener(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodDelete, "/api/v1/listeners/"+url.PathEscape(id), nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) BuildPayload(ctx context.Context, req map[string]any) (*BuildResponse, error) {
	var out BuildResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/builders", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ChannelsHealth(ctx context.Context) (*ChannelHealth, error) {
	var out ChannelHealth
	if _, err := c.get(ctx, "/api/v1/channels/health", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SubmitCommand(ctx context.Context, sessionID, command string, timeoutSec uint32) (*TaskRef, error) {
	body := map[string]any{"session_id": sessionID, "command": command, "task_type": "command"}
	if timeoutSec > 0 {
		body["timeout"] = timeoutSec
	}
	var out TaskRef
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/tasks", body, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) FileList(ctx context.Context, sessionID, path string) (*TaskRef, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("file path is required")
	}
	var out TaskRef
	endpoint := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/files?path=" + url.QueryEscape(path)
	if err := c.doJSON(ctx, http.MethodPost, endpoint, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ProcessList(ctx context.Context, sessionID string) (*TaskRef, error) {
	var out TaskRef
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/sessions/"+url.PathEscape(sessionID)+"/processes", nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Screenshot(ctx context.Context, sessionID string, options map[string]any) (*TaskRef, error) {
	var out TaskRef
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(sessionID)+"/screenshot", options, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) InvokeTool(ctx context.Context, name string, params map[string]string) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/mcp/tools/"+url.PathEscape(name), params, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Tunnels(ctx context.Context) ([]Tunnel, error) {
	var out struct {
		Servers []Tunnel `json:"servers"`
		Count   int      `json:"count"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/tunnels", nil, &out, true); err != nil {
		return nil, err
	}
	return out.Servers, nil
}

func (c *Client) StartTunnel(ctx context.Context, sessionID string, localPort int, remark string) (map[string]any, error) {
	body := map[string]any{"session_id": sessionID, "local_port": localPort, "remark": remark}
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/tunnels", body, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CloseTunnel(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodDelete, "/api/v1/tunnels/"+url.PathEscape(id), nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) TaskRaw(ctx context.Context, id string) (*Task, error) {
	var out Task
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"?raw=1", nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, dst any, retry bool) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	attempts := 1
	if retry && method == http.MethodGet {
		attempts = 3
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			}
		}
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return err
		}
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%w: %s %s: HTTP %d: %s", ErrUpstreamStatus, method, path, resp.StatusCode, truncate(string(raw), 256))
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return lastErr
			}
			continue
		}
		if dst != nil {
			if err := json.Unmarshal(raw, dst); err != nil {
				return fmt.Errorf("decode %s: %w", path, err)
			}
		}
		return nil
	}
	return lastErr
}

func (c *Client) get(ctx context.Context, path string, dst any) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%w: %s %s: HTTP %d: %s", ErrUpstreamStatus, http.MethodGet, path, resp.StatusCode, truncate(string(body), 256))
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return nil, lastErr
			}
			continue
		}
		if dst != nil {
			if err := json.Unmarshal(body, dst); err != nil {
				return nil, fmt.Errorf("decode %s: %w", path, err)
			}
		}
		return body, nil
	}
	return nil, lastErr
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
