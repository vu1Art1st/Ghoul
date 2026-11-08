package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/integrations/toshell/policy"
)

func (s *Server) c2Operations(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeJSON(w, 200, map[string]any{"items": []db.C2Operation{}, "count": 0, "enabled": false})
		return
	}
	q := r.URL.Query()
	items, err := s.m.pg.ListC2Operations(r.Context(), db.C2OperationFilter{
		TaskID:    int64(atoiDefault(q.Get("task_id"), 0)),
		Status:    strings.TrimSpace(q.Get("status")),
		SessionID: strings.TrimSpace(q.Get("session_id")),
		Limit:     atoiDefault(q.Get("limit"), 100),
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items), "enabled": true})
}

func (s *Server) c2OperationDetail(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid C2 operation id")
		return
	}
	detail, err := s.c2.operationDetail(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if detail == nil || detail.Operation == nil {
		writeErr(w, 404, "C2 operation not found")
		return
	}
	writeJSON(w, 200, detail)
}

func (s *Server) c2CreateOperation(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	var req struct {
		TaskID         int64          `json:"task_id"`
		SessionID      string         `json:"session_id"`
		Tool           string         `json:"tool"`
		Action         string         `json:"action"`
		Params         map[string]any `json:"params"`
		IdempotencyKey string         `json:"idempotency_key"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	op, err := s.c2.submitOperation(r.Context(), c2OperationRequest{
		TaskID: req.TaskID, SessionID: req.SessionID, Tool: req.Tool, Action: req.Action,
		Params: req.Params, Actor: "user", Source: "manual", IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeC2PolicyError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{
		"operation": op, "requires_approval": op.ApprovalRequired,
		"status": op.Status,
	})
}

func (s *Server) c2ChallengeOperation(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid C2 operation id")
		return
	}
	var req struct {
		Stage int `json:"stage"`
	}
	_ = decode(r, &req)
	if req.Stage <= 0 {
		req.Stage = 1
	}
	token, approval, err := s.c2.challengeApproval(r.Context(), id, req.Stage)
	if err != nil {
		writeC2PolicyError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"operation_id": id, "stage": req.Stage, "token": token,
		"expires_at": approval.ExpiresAt, "warning": "This token is single-use and bound to the operation parameters.",
	})
}

func (s *Server) c2ApproveOperation(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid C2 operation id")
		return
	}
	var req struct {
		Stage   int    `json:"stage"`
		Token   string `json:"token"`
		Reason  string `json:"reason"`
		Execute bool   `json:"execute"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Stage <= 0 {
		req.Stage = 1
	}
	approval, allApproved, err := s.c2.approveOperation(r.Context(), id, req.Stage, req.Token, "user", req.Reason)
	if err != nil {
		writeC2PolicyError(w, err)
		return
	}
	var op *db.C2Operation
	if allApproved && req.Execute {
		op, err = s.c2.executeOperation(r.Context(), id)
		if err != nil {
			writeC2PolicyError(w, err)
			return
		}
	} else {
		op, _ = s.m.pg.GetC2Operation(r.Context(), id)
	}
	writeJSON(w, 200, map[string]any{"approval": approval, "operation": op, "all_approved": allApproved})
}

func (s *Server) c2RejectOperation(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid C2 operation id")
		return
	}
	var req struct {
		Stage  int    `json:"stage"`
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Stage <= 0 {
		req.Stage = 1
	}
	if err := s.c2.rejectOperation(r.Context(), id, req.Stage, "user", req.Reason); err != nil {
		writeC2PolicyError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"operation_id": id, "status": "denied"})
}

func (s *Server) c2ExecuteOperation(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid C2 operation id")
		return
	}
	op, err := s.c2.executeOperation(r.Context(), id)
	if err != nil {
		writeC2PolicyError(w, err)
		return
	}
	writeJSON(w, 200, op)
}

func (s *Server) c2BindEvidenceFinding(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "invalid evidence id")
		return
	}
	var req struct {
		FindingID int64  `json:"finding_id"`
		Reason    string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.FindingID <= 0 || strings.TrimSpace(req.Reason) == "" {
		writeErr(w, 400, "finding_id and reason are required")
		return
	}
	if err := s.c2.bindEvidenceToFinding(r.Context(), id, req.FindingID, "user", req.Reason); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "evidence_id": id, "finding_id": req.FindingID})
}

func writeC2PolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, policy.ErrRateLimited):
		writeErr(w, 429, err.Error())
	case errors.Is(err, policy.ErrDisabled), errors.Is(err, policy.ErrConfirmDisabled),
		errors.Is(err, policy.ErrDangerDisabled), errors.Is(err, policy.ErrTunnelDisabled),
		errors.Is(err, policy.ErrSessionMismatch),
		errors.Is(err, policy.ErrSessionInactive), errors.Is(err, policy.ErrScopeRequired):
		writeErr(w, 403, err.Error())
	case errors.Is(err, policy.ErrApprovalRequired), errors.Is(err, policy.ErrApprovalInvalid),
		errors.Is(err, policy.ErrApprovalExpired), errors.Is(err, policy.ErrApprovalConsumed):
		writeErr(w, 409, err.Error())
	default:
		writeErr(w, 400, err.Error())
	}
}
