package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestResolveAPIKeyReference(t *testing.T) {
	t.Setenv("TEST_TOSHELL_KEY", "secret-value")
	if got, err := ResolveAPIKey("env:TEST_TOSHELL_KEY"); err != nil || got != "secret-value" {
		t.Fatalf("env ref: %q %v", got, err)
	}
	if _, err := ResolveAPIKey("literal-secret"); err != ErrBadAPIKeyRef {
		t.Fatalf("literal ref should fail: %v", err)
	}
	file := t.TempDir() + "/key"
	if err := os.WriteFile(file, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveAPIKey("file:" + file); err != nil || got != "file-secret" {
		t.Fatalf("file ref: %q %v", got, err)
	}
}

func TestClientReadEndpoints(t *testing.T) {
	t.Setenv("TEST_TOSHELL_KEY", "secret-value")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "secret-value" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/health":
			_, _ = w.Write([]byte(`{"ok":true,"version":"v1.4.3"}`))
		case "/api/v1/sessions":
			_, _ = w.Write([]byte(`{"sessions":[{"id":"s1","hostname":"host"}],"count":1}`))
		case "/api/v1/tasks":
			_, _ = w.Write([]byte(`{"tasks":[{"id":7,"session_id":"s1","status":"completed"}],"count":1}`))
		case "/api/v1/listeners":
			_, _ = w.Write([]byte(`{"listeners":[{"id":"l1","name":"tcp"}],"count":1}`))
		case "/api/v1/channels/health":
			_, _ = w.Write([]byte(`{"channels":[{"type":"tcp","online":1}],"total_online":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := New(Config{BaseURL: server.URL, APIKeyRef: "env:TEST_TOSHELL_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	health, err := c.Health(context.Background())
	if err != nil || !health.OK {
		t.Fatalf("health: %+v %v", health, err)
	}
	sessions, err := c.Sessions(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "s1" {
		t.Fatalf("sessions: %+v %v", sessions, err)
	}
	tasks, err := c.Tasks(context.Background(), "s1")
	if err != nil || len(tasks) != 1 || tasks[0].ID != 7 {
		t.Fatalf("tasks: %+v %v", tasks, err)
	}
	listeners, err := c.Listeners(context.Background())
	if err != nil || len(listeners) != 1 {
		t.Fatalf("listeners: %+v %v", listeners, err)
	}
	channels, err := c.ChannelsHealth(context.Background())
	if err != nil || channels.TotalOnline != 1 {
		t.Fatalf("channels: %+v %v", channels, err)
	}
}

func TestClientRejectsUnsafeTransportAndHost(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://192.0.2.10:18081", APIKeyRef: "env:TEST_TOSHELL_KEY"}); err == nil {
		t.Fatal("non-loopback HTTP sidecar must be rejected")
	}
	if _, err := New(Config{BaseURL: "https://sidecar.example.com", APIKeyRef: "env:TEST_TOSHELL_KEY"}); err == nil {
		t.Fatal("non-allowlisted sidecar host must be rejected")
	}
}

func TestHostAllowed(t *testing.T) {
	if !hostAllowed("127.0.0.1", nil) || !hostAllowed("localhost", nil) {
		t.Fatal("loopback hosts should be allowed by default")
	}
	if hostAllowed("sidecar.example.com", nil) {
		t.Fatal("remote host should require an allowlist")
	}
	if !hostAllowed("sidecar.example.com", []string{"sidecar.example.com"}) {
		t.Fatal("explicit host allowlist should work")
	}
}
