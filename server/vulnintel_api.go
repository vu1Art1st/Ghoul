package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/traffic"
)

func maskVulnIntelSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return "********" + value[len(value)-4:]
}

func (s *Server) vulnintelStatus(w http.ResponseWriter, r *http.Request) {
	if s.vulnIntel == nil || s.vulnIntel.store == nil {
		writeErr(w, 503, "漏洞情报库未初始化")
		return
	}
	status := s.vulnIntel.store.StatusSnapshot(r.Context())
	running, startedAt, phase := s.vulnIntel.State()
	status["enabled"] = s.m.pg.GetBool(settingVulnIntelOn, true)
	status["sync_running"] = running
	status["sync_phase"] = phase
	if !startedAt.IsZero() {
		status["sync_started_at"] = startedAt.UTC().Format(time.RFC3339)
	}
	nvdKey, _, _ := s.m.pg.GetSetting(settingVulnIntelNVDKey)
	githubToken, _, _ := s.m.pg.GetSetting(settingVulnIntelGitHubToken)
	proxy, _, _ := s.m.pg.GetSetting(settingVulnIntelProxy)
	status["config"] = map[string]any{
		"enabled":             s.m.pg.GetBool(settingVulnIntelOn, true),
		"nvd_key_set":         strings.TrimSpace(nvdKey) != "",
		"nvd_api_key":         maskVulnIntelSecret(nvdKey),
		"github_token_set":    strings.TrimSpace(githubToken) != "",
		"github_token":        maskVulnIntelSecret(githubToken),
		"proxy":               strings.TrimSpace(proxy),
		"poc_enabled":         s.m.pg.GetBool(settingVulnIntelPoc, false),
		"backfill_days":       s.vulnIntel.intSetting(settingVulnIntelBackfillDays, 0),
		"max_runtime_minutes": s.vulnIntel.intSetting(settingVulnIntelRuntime, 30),
	}
	status["nuclei"] = nucleiTemplateStats(s.vulnIntel.nucleiDir)
	writeJSON(w, 200, status)
}

func (s *Server) vulnintelSearch(w http.ResponseWriter, r *http.Request) {
	if s.vulnIntel == nil || s.vulnIntel.store == nil {
		writeErr(w, 503, "漏洞情报库未初始化")
		return
	}
	q := r.URL.Query()
	product := strings.TrimSpace(q.Get("product"))
	if product == "" {
		writeErr(w, 400, "product is required")
		return
	}
	hits, err := s.vulnIntel.store.SearchVulns(r.Context(), q.Get("vendor"), product, q.Get("version"), atoiDefault(q.Get("limit"), 20))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": hits, "count": len(hits)})
}

func (s *Server) vulnintelDetail(w http.ResponseWriter, r *http.Request) {
	if s.vulnIntel == nil || s.vulnIntel.store == nil {
		writeErr(w, 503, "漏洞情报库未初始化")
		return
	}
	item, err := s.vulnIntel.store.GetVuln(r.Context(), r.PathValue("cveId"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if item == nil {
		writeErr(w, 404, "CVE not found")
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) vulnintelPoCs(w http.ResponseWriter, r *http.Request) {
	if s.vulnIntel == nil || s.vulnIntel.store == nil {
		writeErr(w, 503, "漏洞情报库未初始化")
		return
	}
	items, err := s.vulnIntel.store.GetPoCs(r.Context(), r.PathValue("cveId"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) vulnintelSyncNow(w http.ResponseWriter, r *http.Request) {
	if s.vulnIntel == nil {
		writeErr(w, 503, "漏洞情报库未初始化")
		return
	}
	if !s.vulnIntel.SyncNow() {
		writeErr(w, 409, "已有一轮同步在进行中")
		return
	}
	writeJSON(w, 202, map[string]any{"accepted": true})
}

func (s *Server) vulnintelSyncCancel(w http.ResponseWriter, r *http.Request) {
	if s.vulnIntel == nil || !s.vulnIntel.Cancel() {
		writeErr(w, 409, "当前没有在运行的同步")
		return
	}
	writeJSON(w, 200, map[string]any{"cancelling": true})
}

func (s *Server) vulnintelSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled       *bool   `json:"enabled"`
		NVDAPIKey     *string `json:"nvd_api_key"`
		GitHubToken   *string `json:"github_token"`
		Proxy         *string `json:"proxy"`
		PocEnabled    *bool   `json:"poc_enabled"`
		BackfillDays  *int    `json:"backfill_days"`
		MaxRuntimeMin *int    `json:"max_runtime_minutes"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	setSecret := func(key string, value *string) error {
		if value == nil {
			return nil
		}
		v := strings.TrimSpace(*value)
		if v == "" {
			return nil
		}
		if v == "-" {
			return s.m.pg.SetSetting(key, "")
		}
		return s.m.pg.SetSetting(key, v)
	}
	if req.Enabled != nil {
		if err := s.m.pg.SetBool(settingVulnIntelOn, *req.Enabled); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if err := setSecret(settingVulnIntelNVDKey, req.NVDAPIKey); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := setSecret(settingVulnIntelGitHubToken, req.GitHubToken); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if req.Proxy != nil {
		value := strings.TrimSpace(*req.Proxy)
		if value != "" {
			if _, err := traffic.ValidateProxyURL(value); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		}
		if err := s.m.pg.SetSetting(settingVulnIntelProxy, value); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if req.PocEnabled != nil {
		if err := s.m.pg.SetBool(settingVulnIntelPoc, *req.PocEnabled); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if req.BackfillDays != nil {
		value := *req.BackfillDays
		if value < 0 {
			value = 0
		}
		if err := s.m.pg.SetSetting(settingVulnIntelBackfillDays, itoa(value)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if req.MaxRuntimeMin != nil {
		value := *req.MaxRuntimeMin
		if value < 5 {
			value = 5
		}
		if err := s.m.pg.SetSetting(settingVulnIntelRuntime, itoa(value)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
