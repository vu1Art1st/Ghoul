package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

type c2SecurityCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (s *Server) c2Security(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeJSON(w, 200, map[string]any{
			"ok": false,
			"checks": []c2SecurityCheck{{Name: "adapter", OK: false, Detail: "C2 adapter is disabled"}},
			"notes":  []string{"Enable C2 only after reading the Toshell integration guide."},
		})
		return
	}
	cfg := s.c2.cfg
	checks := []c2SecurityCheck{
		{Name: "secret_reference", OK: strings.HasPrefix(cfg.APIKeyRef, "env:") || strings.HasPrefix(cfg.APIKeyRef, "file:"), Detail: "API key must use env: or file: reference"},
		{Name: "allowed_host", OK: c2HostAllowed(cfg.BaseURL, cfg.AllowedHosts), Detail: "Toshell host must be loopback or explicitly allowlisted"},
		{Name: "tls_or_loopback", OK: c2TransportSafe(cfg.BaseURL, cfg.AllowInsecureHTTP), Detail: "non-loopback sidecars require HTTPS"},
		{Name: "approval_ttl", OK: cfg.ApprovalTTLSeconds >= 60 && cfg.ApprovalTTLSeconds <= 3600, Detail: "approval tokens must expire within 60-3600 seconds"},
		{Name: "danger_dual_approval", OK: !cfg.AllowDanger || cfg.DangerApprovals == 2, Detail: "Danger operations require two approval stages"},
		{Name: "output_limit", OK: cfg.OutputMaxBytes >= 4096 && cfg.OutputMaxBytes <= 4<<20, Detail: "C2 output must be capped between 4 KiB and 4 MiB"},
		{Name: "ui_boundary", OK: true, Detail: "Ghoul never embeds Toshell in an iframe; the admin console opens in a new window"},
	}
	ok := true
	warnings := []string{}
	for _, check := range checks {
		if !check.OK {
			ok = false
			warnings = append(warnings, check.Name+": "+check.Detail)
		}
	}
	writeJSON(w, 200, map[string]any{
		"ok": ok, "checks": checks, "warnings": warnings,
		"policy": map[string]any{
			"allow_confirm": cfg.AllowConfirm, "allow_danger": cfg.AllowDanger,
			"allow_tunnel": cfg.AllowTunnel, "approval_ttl_seconds": cfg.ApprovalTTLSeconds,
			"danger_approvals": cfg.DangerApprovals, "max_operations_per_minute": cfg.MaxOperationsPerMinute,
			"output_max_bytes": cfg.OutputMaxBytes,
		},
		"notes": []string{
			"Toshell API keys are server-only and never returned by this endpoint.",
			"All C2 output is treated as untrusted data and is redacted before materialization.",
		},
	})
}

func c2HostAllowed(baseURL string, allowed []string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.Trim(u.Hostname(), "[]")
	if net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), host) {
			return true
		}
	}
	return false
}

func c2TransportSafe(baseURL string, allowInsecure bool) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	host := strings.Trim(u.Hostname(), "[]")
	return allowInsecure || strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}
