package server

import (
	"context"
	"net/http"
	"strings"
)

func (s *Server) proxyTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proxy    *string `json:"proxy"`
		ProbeURL string  `json:"probe_url"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	proxyRaw := s.m.GlobalProxy()
	if req.Proxy != nil {
		proxyRaw = strings.TrimSpace(*req.Proxy)
	}
	target := strings.TrimSpace(req.ProbeURL)
	if target == "" {
		target = defaultEgressProbeURL
	}
	ctx, cancel := context.WithTimeout(r.Context(), egressProbeTimeout)
	defer cancel()
	result := probeEgress(ctx, proxyRaw, target, egressProbeTimeout)
	if ctx.Err() != nil && result.Error == "" {
		result.Error = ctx.Err().Error()
	}
	writeJSON(w, 200, result)
}
