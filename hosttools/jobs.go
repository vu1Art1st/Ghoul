package hosttools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Autumn-27/artex/db"
)

type JobRunner interface {
	Start(kind, target string) (*db.HostInstallJob, error)
	Get(context.Context, string) (*db.HostInstallJob, error)
	List(context.Context) ([]*db.HostInstallJob, error)
	Cancel(string) bool
}

type proxiedJobStarter interface {
	StartTool(target, proxy string) (*db.HostInstallJob, error)
}

// Handler exposes only catalog installs on a caller-provided local listener.
func Handler(runner JobRunner) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeHelperJSON(w, 200, map[string]any{"ok": true, "service": "ghoul-hostd"})
	})
	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID, Proxy string }
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
			writeHelperJSON(w, 400, map[string]string{"error": "bad json"})
			return
		}
		var job *db.HostInstallJob
		var err error
		if proxied, ok := runner.(proxiedJobStarter); ok {
			job, err = proxied.StartTool(req.ID, req.Proxy)
		} else {
			job, err = runner.Start("tool", req.ID)
		}
		if err != nil {
			writeHelperJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeHelperJSON(w, 202, job)
	})
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := runner.List(r.Context())
		if err != nil {
			writeHelperJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeHelperJSON(w, 200, map[string]any{"jobs": jobs})
	})
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, err := runner.Get(r.Context(), r.PathValue("id"))
		if err != nil || job == nil {
			writeHelperJSON(w, 404, map[string]string{"error": "job not found"})
			return
		}
		writeHelperJSON(w, 200, job)
	})
	mux.HandleFunc("POST /jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !runner.Cancel(r.PathValue("id")) {
			writeHelperJSON(w, 409, map[string]string{"error": "job not active"})
			return
		}
		writeHelperJSON(w, 200, map[string]bool{"ok": true})
	})
	return mux
}

func writeHelperJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

type RemoteRunner struct {
	Client  *http.Client
	ProxyFn func() string
}

func (r RemoteRunner) call(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://ghoul-hostd"+path, body)
	if err != nil {
		return err
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&payload)
		if payload.Error == "" {
			payload.Error = resp.Status
		}
		return fmt.Errorf("%s", payload.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (r RemoteRunner) Start(_, target string) (*db.HostInstallJob, error) {
	var job db.HostInstallJob
	body, _ := json.Marshal(map[string]string{"id": target})
	if r.ProxyFn != nil {
		body, _ = json.Marshal(map[string]string{"id": target, "proxy": r.ProxyFn()})
	}
	err := r.call(context.Background(), http.MethodPost, "/jobs", bytes.NewReader(body), &job)
	return &job, err
}
func (r RemoteRunner) Get(ctx context.Context, id string) (*db.HostInstallJob, error) {
	var job db.HostInstallJob
	if err := r.call(ctx, http.MethodGet, "/jobs/"+id, nil, &job); err != nil {
		return nil, err
	}
	return &job, nil
}
func (r RemoteRunner) List(ctx context.Context) ([]*db.HostInstallJob, error) {
	var result struct {
		Jobs []*db.HostInstallJob `json:"jobs"`
	}
	if err := r.call(ctx, http.MethodGet, "/jobs", nil, &result); err != nil {
		return nil, err
	}
	return result.Jobs, nil
}
func (r RemoteRunner) Cancel(id string) bool {
	var result map[string]bool
	return r.call(context.Background(), http.MethodPost, "/jobs/"+id+"/cancel", nil, &result) == nil && result["ok"]
}
