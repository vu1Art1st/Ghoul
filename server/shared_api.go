package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/hosttools"
	"github.com/Autumn-27/artex/shared"
)

const settingInstallProxy = "install_proxy"

func (s *Server) installProxy() string {
	v, _, _ := s.m.pg.GetSetting(settingInstallProxy)
	return strings.TrimSpace(v)
}

func validateInstallProxy(raw string) error {
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, "\r\n\t") {
		return fmt.Errorf("代理包含非法字符")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("代理地址无效")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("仅支持 http/https/socks5/socks5h 代理")
	}
	return nil
}

func (s *Server) hostenvStatus(w http.ResponseWriter, r *http.Request) {
	mode := "binary"
	if _, err := os.Stat("/.dockerenv"); err == nil {
		mode = "docker"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deploy_mode": mode, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"tools":     hosttools.Inventory(s.shared.Python()),
		"python":    s.shared.PythonStatus(),
		"installer": map[string]any{"available": s.jobs != nil, "privileged_helper": hosttools.RemoteFromEnv() != nil, "proxy": s.installProxy()},
	})
}

func (s *Server) hostenvInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	job, err := s.jobs.Start("tool", req.ID)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, job)
}

func (s *Server) hostenvInstallBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > 100 {
		writeErr(w, 400, "一次需选择 1-100 个工具")
		return
	}
	started, skipped := []any{}, []map[string]string{}
	seen := map[string]bool{}
	for _, id := range req.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		job, err := s.jobs.Start("tool", id)
		if err != nil {
			skipped = append(skipped, map[string]string{"id": id, "reason": err.Error()})
			continue
		}
		started = append(started, job)
	}
	writeJSON(w, 202, map[string]any{"started": started, "skipped": skipped})
}

func (s *Server) hostenvJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobs.List(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": jobs})
}

func (s *Server) hostenvJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if job == nil {
		writeErr(w, 404, "安装任务不存在")
		return
	}
	writeJSON(w, 200, job)
}

func (s *Server) hostenvCancel(w http.ResponseWriter, r *http.Request) {
	if !s.jobs.Cancel(r.PathValue("id")) {
		writeErr(w, 409, "任务不存在或已结束")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) hostenvProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proxy *string `json:"proxy"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Proxy == nil {
		writeErr(w, 400, "缺少 proxy")
		return
	}
	value := strings.TrimSpace(*req.Proxy)
	if err := validateInstallProxy(value); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.m.pg.SetSetting(settingInstallProxy, value); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "proxy": value})
}

func (s *Server) hostenvProxyCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proxy string `json:"proxy"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if strings.TrimSpace(req.Proxy) == "" {
		req.Proxy = s.installProxy()
	}
	if err := validateInstallProxy(req.Proxy); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	if req.Proxy != "" {
		u, _ := url.Parse(req.Proxy)
		client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	}
	reqHTTP, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	start := time.Now()
	resp, err := client.Do(reqHTTP)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	resp.Body.Close()
	writeJSON(w, 200, map[string]any{"ok": resp.StatusCode < 500, "status": resp.StatusCode, "latency_ms": time.Since(start).Milliseconds()})
}

type sharedPublishRequest struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	Language       string   `json:"language"`
	Entrypoint     string   `json:"entrypoint"`
	Usage          string   `json:"usage"`
	Dependencies   []string `json:"dependencies"`
	SourcePath     string   `json:"source_path"`
	SourceTaskID   int64    `json:"source_task_id"`
	SourceIntentID int64    `json:"source_intent_id"`
	AssetID        int64    `json:"asset_id"`
}

func (s *Server) sharedList(w http.ResponseWriter, r *http.Request) {
	assets, err := s.shared.List(r.Context(), r.URL.Query().Get("q"), false)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"assets": assets})
}

func (s *Server) sharedPublish(w http.ResponseWriter, r *http.Request) {
	var req sharedPublishRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	a, err := s.shared.Publish(r.Context(), shared.PublishInput{
		Name: req.Name, Kind: req.Kind, Description: req.Description, Tags: req.Tags,
		Language: req.Language, Entrypoint: req.Entrypoint, Usage: req.Usage,
		Dependencies: req.Dependencies, SourcePath: req.SourcePath,
		SourceTaskID: req.SourceTaskID, SourceIntentID: req.SourceIntentID, AssetID: req.AssetID,
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, a)
}

func (s *Server) sharedGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a, err := s.shared.Get(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if a == nil {
		writeErr(w, 404, "共享资产不存在")
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) sharedStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Status string `json:"status"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Status != "disabled" && req.Status != "published" {
		writeErr(w, 400, "仅支持停用或重新启用")
		return
	}
	a, _ := s.shared.Get(r.Context(), id)
	if a == nil || a.CurrentVersion == 0 {
		writeErr(w, 404, "已发布资产不存在")
		return
	}
	if err := s.shared.SetStatus(r.Context(), id, req.Status); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) sharedPackages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.shared.PythonStatus())
}

func (s *Server) sharedPackageInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec string `json:"spec"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	job, err := s.installer.Start("python-package", req.Spec)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, job)
}

func (s *Server) sharedPackageRemove(w http.ResponseWriter, r *http.Request) {
	spec := strings.TrimSpace(r.URL.Query().Get("name"))
	if err := hosttools.ValidatePackageSpec(spec); err != nil || strings.ContainsAny(spec, "=<>!~[](),@") {
		writeErr(w, 400, "包名非法")
		return
	}
	job, err := s.installer.Start("python-uninstall", spec)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, job)
}
