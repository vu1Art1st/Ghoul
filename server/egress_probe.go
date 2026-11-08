package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultEgressProbeURL = "https://api.ipify.org?format=json"
const egressProbeTimeout = 20 * time.Second

type egressProbeResult struct {
	OK        bool   `json:"ok"`
	ExitIP    string `json:"exit_ip,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	Target    string `json:"target"`
	ViaProxy  bool   `json:"via_proxy"`
}

func validateEgressProbeTarget(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("probe URL must be an absolute HTTPS URL without credentials")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if !isPublicProbeIP(ip) {
			return nil, fmt.Errorf("probe target resolves to a non-public address")
		}
		return u, nil
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve probe target: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("probe target has no IP addresses")
	}
	for _, ip := range ips {
		if !isPublicProbeIP(ip) {
			return nil, fmt.Errorf("probe target resolves to a non-public address")
		}
	}
	return u, nil
}

func isPublicProbeIP(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

func probeEgress(ctx context.Context, proxyRaw, targetRaw string, timeout time.Duration) egressProbeResult {
	result := egressProbeResult{Target: targetRaw, ViaProxy: strings.TrimSpace(proxyRaw) != ""}
	target, err := validateEgressProbeTarget(targetRaw)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	if result.ViaProxy {
		proxyURL, err := url.Parse(strings.TrimSpace(proxyRaw))
		if err != nil || proxyURL.Hostname() == "" ||
			(proxyURL.Scheme != "http" && proxyURL.Scheme != "https" && proxyURL.Scheme != "socks5" && proxyURL.Scheme != "socks5h") {
			result.Error = "proxy must be http(s):// or socks5://host:port"
			return result
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "ghoul-egress-probe/1.0")
	resp, err := client.Do(req)
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Sprintf("probe returned HTTP %d", resp.StatusCode)
		return result
	}
	result.ExitIP = parseProbeExitIP(body)
	result.OK = result.ExitIP != ""
	if !result.OK {
		result.Error = "probe response did not contain an IP address"
	}
	return result
}

func parseProbeExitIP(body []byte) string {
	var payload struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal(body, &payload) == nil && net.ParseIP(strings.TrimSpace(payload.IP)) != nil {
		return strings.TrimSpace(payload.IP)
	}
	text := strings.TrimSpace(string(body))
	if net.ParseIP(text) != nil {
		return text
	}
	return ""
}
