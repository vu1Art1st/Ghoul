// Package client is the server-side Toshell REST client. It deliberately exposes
// only read endpoints used by Phase 3; write operations stay behind the later
// policy/approval layers.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	BaseURL   string
	APIKeyRef string
	Timeout   time.Duration
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
	Error       string     `json:"error,omitempty"`
	ExitCode    int32      `json:"exit_code"`
	Timeout     uint32     `json:"timeout"`
	Path        string     `json:"path,omitempty"`
	PID         uint32     `json:"pid,omitempty"`
	Data        string     `json:"data,omitempty"`
	Progress    int        `json:"progress,omitempty"`
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
	apiKey, err := ResolveAPIKey(cfg.APIKeyRef)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}, nil
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

func (c *Client) ChannelsHealth(ctx context.Context) (*ChannelHealth, error) {
	var out ChannelHealth
	if _, err := c.get(ctx, "/api/v1/channels/health", &out); err != nil {
		return nil, err
	}
	return &out, nil
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
