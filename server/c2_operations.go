package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	toshellapproval "github.com/Autumn-27/artex/integrations/toshell/approval"
	"github.com/Autumn-27/artex/integrations/toshell/policy"
	"github.com/Autumn-27/artex/integrations/toshell/sanitize"
)

type c2OperationRequest struct {
	TaskID         int64
	SessionID      string
	Tool           string
	Action         string
	Params         map[string]any
	Actor          string
	Source         string
	IdempotencyKey string
}

type c2OperationDetail struct {
	Operation *db.C2Operation          `json:"operation"`
	Approvals []db.C2OperationApproval `json:"approvals"`
	Evidence  []db.C2Evidence          `json:"evidence"`
	Scope     json.RawMessage          `json:"scope"`
}

func (s *c2Service) operationPolicyConfig() policy.Config {
	return policy.Config{
		Enabled:       s != nil && s.cfg.Enabled,
		AllowConfirm:  s != nil && s.cfg.AllowConfirm,
		AllowDanger:   s != nil && s.cfg.AllowDanger,
		AllowTunnel:   s != nil && s.cfg.AllowTunnel,
		AllowUnscoped: s != nil && s.cfg.AllowUnscopedTasks,
	}
}

func (s *c2Service) boundSessionForTask(ctx context.Context, taskID int64, sessionID string) (*db.C2Session, error) {
	if s == nil || s.pg == nil {
		return nil, policy.ErrDisabled
	}
	sessionID = strings.TrimSpace(sessionID)
	if taskID <= 0 || sessionID == "" {
		return nil, policy.ErrSessionRequired
	}
	items, err := s.pg.ListC2Sessions(ctx, db.C2SessionFilter{TaskID: taskID, BoundOnly: true, Limit: 1000})
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ExternalSessionID != sessionID {
			continue
		}
		if items[i].Binding == nil {
			return nil, policy.ErrSessionMismatch
		}
		expired := items[i].Binding.ExpiresAt != nil && time.Now().After(*items[i].Binding.ExpiresAt)
		if !items[i].Binding.Active || expired {
			return nil, policy.ErrSessionInactive
		}
		return &items[i], nil
	}
	return nil, policy.ErrSessionMismatch
}

func (s *c2Service) allowRate(taskID int64) bool {
	if s == nil {
		return false
	}
	limit := s.cfg.MaxOperationsPerMinute
	if limit <= 0 {
		limit = 30
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if s.rate == nil {
		s.rate = map[int64][]time.Time{}
	}
	items := s.rate[taskID]
	kept := items[:0]
	for _, item := range items {
		if item.After(cutoff) {
			kept = append(kept, item)
		}
	}
	if len(kept) >= limit {
		s.rate[taskID] = kept
		return false
	}
	s.rate[taskID] = append(kept, now)
	return true
}

func c2ParamsHash(params map[string]any) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validateC2Params(tool string, params map[string]any) error {
	switch tool {
	case "c2_recon":
		action, _ := params["action"].(string)
		allowed := map[string]bool{
			"user_info": true, "system_info": true, "net_info": true,
			"net_connections": true, "env_vars": true, "scheduled_tasks": true,
		}
		if !allowed[action] {
			return fmt.Errorf("recon action is not allowed")
		}
	case "c2_file_list":
		path, _ := params["path"].(string)
		path = strings.TrimSpace(path)
		if path == "" || strings.ContainsRune(path, 0) || len(path) > 4096 {
			return errors.New("file path is required and must be at most 4096 bytes")
		}
	case "c2_exec":
		command, _ := params["command"].(string)
		command = strings.TrimSpace(command)
		if command == "" || strings.ContainsRune(command, 0) || len(command) > 32<<10 {
			return errors.New("command is required and must be at most 32 KiB")
		}
		if n := intFromAny(params["timeout_sec"]); n < 0 || n > 300 {
			return errors.New("timeout_sec must be between 0 and 300")
		}
	case "c2_screenshot":
		if n := intFromAny(params["monitor"]); n < 0 || n > 16 {
			return errors.New("monitor must be between 0 and 16")
		}
		if n := intFromAny(params["max_width"]); n < 0 || n > 7680 {
			return errors.New("max_width must be between 0 and 7680")
		}
		if n := intFromAny(params["quality"]); n != 0 && (n < 20 || n > 95) {
			return errors.New("quality must be between 20 and 95")
		}
		if format, _ := params["format"].(string); format != "" && format != "auto" && format != "png" && format != "jpeg" {
			return errors.New("format must be auto, png, or jpeg")
		}
	case "c2_tunnel":
		action, _ := params["action"].(string)
		if action != "list" && action != "start" && action != "stop" {
			return errors.New("tunnel action must be list, start, or stop")
		}
		if n := intFromAny(params["local_port"]); n < 0 || n > 65535 {
			return errors.New("local_port must be between 0 and 65535")
		}
		if action == "stop" {
			id, _ := params["tunnel_id"].(string)
			if strings.TrimSpace(id) == "" {
				return errors.New("tunnel_id is required for stop")
			}
		}
	}
	return nil
}

func c2RedactParams(value any, maxBytes int) any {
	switch v := value.(type) {
	case string:
		return sanitize.Text(v, maxBytes).Text
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = c2RedactParams(item, maxBytes)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = c2RedactParams(item, maxBytes)
		}
		return out
	default:
		return value
	}
}

func (s *c2Service) submitOperation(ctx context.Context, req c2OperationRequest) (*db.C2Operation, error) {
	if s == nil || s.client == nil {
		return nil, policy.ErrDisabled
	}
	req.Tool = policy.NormalizeAction(req.Tool)
	action, ok := policy.Lookup(req.Tool)
	if !ok {
		return nil, fmt.Errorf("%w: %s", policy.ErrUnknownAction, req.Tool)
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	if req.Actor == "" {
		req.Actor = "agent"
	}
	if req.Source == "" {
		req.Source = "agent"
	}
	if err := validateC2Params(req.Tool, req.Params); err != nil {
		return nil, err
	}
	paramsHash, err := c2ParamsHash(req.Params)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		seed := fmt.Sprintf("%d|%s|%s|%s|%s", req.TaskID, req.SessionID, req.Tool, req.Action, paramsHash)
		sum := sha256.Sum256([]byte(seed))
		req.IdempotencyKey = hex.EncodeToString(sum[:16])
	}
	existing, err := s.pg.GetC2OperationByIdempotency(ctx, s.instanceID, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ParamsHash != paramsHash {
			return nil, policy.ErrIdempotencyReused
		}
		return existing, nil
	}
	session, err := s.boundSessionForTask(ctx, req.TaskID, req.SessionID)
	if err != nil {
		return nil, err
	}
	scopeRaw, scopeRows, err := s.pg.TaskScopeSnapshot(ctx, req.TaskID)
	if err != nil {
		return nil, err
	}
	sessionState := policy.Session{
		ExternalID: session.ExternalSessionID, Status: session.Status,
		BindingID: session.Binding.ID, TaskID: session.Binding.TaskID, Active: session.Binding.Active,
		Expired: session.Binding.ExpiresAt != nil && time.Now().After(*session.Binding.ExpiresAt),
	}
	decision, err := policy.Evaluate(policy.Request{
		TaskID: req.TaskID, SessionID: req.SessionID, Action: req.Tool,
		ScopeRows: scopeRows, Session: sessionState, Config: s.operationPolicyConfig(),
	})
	if err != nil {
		return nil, err
	}
	if !s.allowRate(req.TaskID) {
		return nil, policy.ErrRateLimited
	}
	redacted, _ := json.Marshal(c2RedactParams(req.Params, s.cfg.OutputMaxBytes))
	executionParams, _ := json.Marshal(req.Params)
	ttl := s.cfg.ApprovalTTLSeconds
	if ttl <= 0 {
		ttl = 300
	}
	expiry := time.Now().Add(time.Duration(ttl) * time.Second)
	status := "pending_approval"
	if !decision.ApprovalNeeded {
		status = "approved"
	}
	op, err := s.pg.CreateC2Operation(ctx, db.C2OperationInput{
		InstanceID: s.instanceID, TaskID: req.TaskID, C2SessionID: session.ID,
		BindingID: session.Binding.ID, Tool: req.Tool, Action: req.Action,
		Level: string(action.Level), Actor: req.Actor, Source: req.Source,
		ParamsJSON: redacted, ExecutionParams: executionParams, ParamsHash: paramsHash, IdempotencyKey: req.IdempotencyKey,
		Status: status, ApprovalRequired: decision.ApprovalNeeded,
		ApprovalStages: decision.ApprovalStages, ScopeSnapshot: scopeRaw, ExpiresAt: &expiry,
	})
	if err != nil {
		return nil, err
	}
	if !decision.ApprovalNeeded {
		return op, nil
	}
	task, _ := s.pg.GetTask(req.TaskID)
	taskSnapshot := c2TaskSnapshot(task)
	sessionSnapshot := c2SessionSnapshot(session)
	for stage := 1; stage <= decision.ApprovalStages; stage++ {
		claims := toshellapproval.Claims{
			OperationID: op.ID, Stage: stage, ParamsHash: paramsHash, TaskID: req.TaskID,
			SessionID: req.SessionID, BindingID: session.Binding.ID, ExpiresUnix: expiry.Unix(),
		}
		token, err := toshellapproval.Sign(s.secret, claims)
		if err != nil {
			return nil, err
		}
		if _, err := s.pg.CreateC2OperationApproval(ctx, db.C2OperationApprovalInput{
			OperationID: op.ID, Stage: stage, TokenHash: toshellapproval.Hash(token),
			ParamsHash: paramsHash, TaskSnapshot: taskSnapshot, SessionSnapshot: sessionSnapshot,
			ExpiresAt: expiry,
		}); err != nil {
			return nil, err
		}
	}
	return s.pg.GetC2Operation(ctx, op.ID)
}

func (s *c2Service) challengeApproval(ctx context.Context, operationID int64, stage int) (string, *db.C2OperationApproval, error) {
	op, err := s.pg.GetC2Operation(ctx, operationID)
	if err != nil || op == nil {
		return "", nil, errors.New("C2 operation not found")
	}
	approval, err := s.pg.GetC2OperationApproval(ctx, operationID, stage)
	if err != nil || approval == nil {
		return "", nil, errors.New("C2 approval stage not found")
	}
	if approval.Status != "pending" {
		return "", approval, fmt.Errorf("approval stage is %s", approval.Status)
	}
	claims := toshellapproval.Claims{
		OperationID: operationID, Stage: stage, ParamsHash: op.ParamsHash,
		TaskID: op.TaskID, ExpiresUnix: approval.ExpiresAt.Unix(),
	}
	var sessionSnapshot struct {
		SessionID string `json:"session_id"`
		BindingID int64  `json:"binding_id"`
	}
	_ = json.Unmarshal(approval.SessionSnapshot, &sessionSnapshot)
	claims.SessionID = sessionSnapshot.SessionID
	claims.BindingID = sessionSnapshot.BindingID
	token, err := toshellapproval.Sign(s.secret, claims)
	if err != nil {
		return "", nil, err
	}
	if toshellapproval.Hash(token) != approval.TokenHash {
		return "", nil, policy.ErrApprovalInvalid
	}
	return token, approval, nil
}

func (s *c2Service) approveOperation(ctx context.Context, operationID int64, stage int, token, actor, reason string) (*db.C2OperationApproval, bool, error) {
	_, approval, err := s.challengeApproval(ctx, operationID, stage)
	if err != nil {
		return nil, false, err
	}
	claims, err := toshellapproval.Verify(s.secret, token, time.Now())
	if err != nil {
		if errors.Is(err, toshellapproval.ErrExpired) {
			return nil, false, policy.ErrApprovalExpired
		}
		return nil, false, policy.ErrApprovalInvalid
	}
	op, _ := s.pg.GetC2Operation(ctx, operationID)
	var sessionSnapshot struct {
		SessionID string `json:"session_id"`
		BindingID int64  `json:"binding_id"`
	}
	_ = json.Unmarshal(approval.SessionSnapshot, &sessionSnapshot)
	if op == nil || !toshellapproval.Matches(claims, operationID, stage, op.ParamsHash, op.TaskID, sessionSnapshot.SessionID, sessionSnapshot.BindingID) {
		return nil, false, policy.ErrApprovalInvalid
	}
	if approval.TokenHash != toshellapproval.Hash(token) {
		return nil, false, policy.ErrApprovalInvalid
	}
	return s.pg.ApproveC2OperationApproval(ctx, operationID, stage, actor, reason)
}

func (s *c2Service) rejectOperation(ctx context.Context, operationID int64, stage int, actor, reason string) error {
	return s.pg.RejectC2OperationApproval(ctx, operationID, stage, actor, reason)
}

func (s *c2Service) executeOperation(ctx context.Context, operationID int64) (*db.C2Operation, error) {
	op, err := s.pg.GetC2Operation(ctx, operationID)
	if err != nil || op == nil {
		return nil, errors.New("C2 operation not found")
	}
	if op.Status == "pending_approval" {
		return nil, policy.ErrApprovalRequired
	}
	if op.Status == "dispatched" || op.Status == "running" {
		return op, nil
	}
	if op.Status != "approved" {
		return nil, fmt.Errorf("C2 operation is %s", op.Status)
	}
	if op.Level == "danger" && !s.cfg.AllowDanger {
		return nil, policy.ErrDangerDisabled
	}
	if err := s.pg.MarkC2OperationRunning(ctx, op.ID); err != nil {
		return nil, err
	}
	taskID, immediate, evidence, err := s.dispatchOperation(ctx, op)
	if err != nil {
		_ = s.pg.MarkC2OperationFailed(ctx, op.ID, err.Error())
		return nil, err
	}
	if immediate {
		if err := s.recordEvidence(ctx, op, evidence); err != nil {
			_ = s.pg.MarkC2OperationFailed(ctx, op.ID, err.Error())
			return nil, err
		}
		_ = s.pg.MarkC2OperationResult(ctx, op.ID, "completed", sanitize.Summary(evidence.Content, 600), evidence.SHA256, "")
		return s.pg.GetC2Operation(ctx, op.ID)
	}
	if err := s.pg.MarkC2OperationDispatched(ctx, op.ID, taskID); err != nil {
		return nil, err
	}
	go s.watchOperationTask(context.Background(), op.ID, taskID)
	return s.pg.GetC2Operation(ctx, op.ID)
}

func (s *c2Service) dispatchOperation(ctx context.Context, op *db.C2Operation) (int64, bool, db.C2Evidence, error) {
	session, err := s.pg.GetC2SessionByExternalID(ctx, s.instanceID, c2SessionIDFromOperation(ctx, s.pg, op))
	if err != nil || session == nil {
		return 0, false, db.C2Evidence{}, errors.New("C2 session no longer exists")
	}
	var params map[string]any
	rawParams := op.ExecutionParams
	if len(rawParams) == 0 {
		rawParams = op.ParamsJSON
	}
	_ = json.Unmarshal(rawParams, &params)
	action := strings.ToLower(strings.TrimSpace(op.Action))
	if action == "" {
		action, _ = params["action"].(string)
	}
	switch op.Tool {
	case "c2_recon":
		allowed := map[string]bool{
			"user_info": true, "system_info": true, "net_info": true,
			"net_connections": true, "env_vars": true, "scheduled_tasks": true,
		}
		if !allowed[action] {
			return 0, false, db.C2Evidence{}, fmt.Errorf("recon action is not allowed: %s", action)
		}
		out, err := s.client.InvokeTool(ctx, "exec", map[string]string{"session_id": session.ExternalSessionID, "kind": action})
		if err != nil {
			return 0, false, db.C2Evidence{}, err
		}
		return int64FromAny(out["task_id"]), false, db.C2Evidence{}, nil
	case "c2_file_list":
		path, _ := params["path"].(string)
		ref, err := s.client.FileList(ctx, session.ExternalSessionID, path)
		if err != nil {
			return 0, false, db.C2Evidence{}, err
		}
		return int64(ref.TaskID), false, db.C2Evidence{}, nil
	case "c2_process_list":
		ref, err := s.client.ProcessList(ctx, session.ExternalSessionID)
		if err != nil {
			return 0, false, db.C2Evidence{}, err
		}
		return int64(ref.TaskID), false, db.C2Evidence{}, nil
	case "c2_screenshot":
		ref, err := s.client.Screenshot(ctx, session.ExternalSessionID, params)
		if err != nil {
			return 0, false, db.C2Evidence{}, err
		}
		return int64(ref.TaskID), false, db.C2Evidence{}, nil
	case "c2_exec":
		command, _ := params["command"].(string)
		timeout := uint32(120)
		if n, ok := params["timeout_sec"].(float64); ok && n > 0 && n <= 300 {
			timeout = uint32(n)
		}
		ref, err := s.client.SubmitCommand(ctx, session.ExternalSessionID, command, timeout)
		if err != nil {
			return 0, false, db.C2Evidence{}, err
		}
		return int64(ref.TaskID), false, db.C2Evidence{}, nil
	case "c2_tunnel":
		switch action {
		case "list":
			items, err := s.client.Tunnels(ctx)
			if err != nil {
				return 0, false, db.C2Evidence{}, err
			}
			raw, _ := json.Marshal(items)
			return 0, true, db.C2Evidence{Kind: "tunnel", Title: "Tunnel list", Content: string(raw), MetadataJSON: json.RawMessage(`{"action":"list"}`)}, nil
		case "start":
			port := intFromAny(params["local_port"])
			out, err := s.client.StartTunnel(ctx, session.ExternalSessionID, port, "Ghoul approved operation")
			if err != nil {
				return 0, false, db.C2Evidence{}, err
			}
			raw, _ := json.Marshal(out)
			return 0, true, db.C2Evidence{Kind: "tunnel", Title: "Tunnel start", Content: string(raw), MetadataJSON: json.RawMessage(`{"action":"start"}`)}, nil
		case "stop":
			id, _ := params["tunnel_id"].(string)
			out, err := s.client.CloseTunnel(ctx, id)
			if err != nil {
				return 0, false, db.C2Evidence{}, err
			}
			raw, _ := json.Marshal(out)
			return 0, true, db.C2Evidence{Kind: "tunnel", Title: "Tunnel stop", Content: string(raw), MetadataJSON: json.RawMessage(`{"action":"stop"}`)}, nil
		default:
			return 0, false, db.C2Evidence{}, fmt.Errorf("tunnel action is not allowed: %s", action)
		}
	default:
		return 0, false, db.C2Evidence{}, fmt.Errorf("unsupported C2 tool: %s", op.Tool)
	}
}

func (s *c2Service) watchOperationTask(ctx context.Context, operationID, taskID int64) {
	deadline := time.NewTimer(3 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			_ = s.pg.MarkC2OperationFailed(context.Background(), operationID, "Toshell task wait timed out")
			return
		case <-ticker.C:
			task, err := s.client.TaskRaw(ctx, fmt.Sprintf("%d", taskID))
			if err != nil {
				continue
			}
			status := strings.ToLower(strings.TrimSpace(task.Status))
			if status != "completed" && status != "failed" && status != "timeout" && status != "cancelled" {
				continue
			}
			output := task.Output
			if output == "" && task.OutputB64 != "" {
				if raw, err := base64.StdEncoding.DecodeString(task.OutputB64); err == nil {
					output = string(raw)
				}
			}
			maxBytes := s.cfg.OutputMaxBytes
			if maxBytes <= 0 {
				maxBytes = 256 << 10
			}
			result := sanitize.Text(output, maxBytes)
			kind := "output"
			mime := "text/plain"
			switch task.TaskType {
			case "file_list":
				kind = "file_list"
			case "process_list":
				kind = "process_list"
			case "screenshot":
				kind, mime = "screenshot", "image/png"
			}
			op, _ := s.pg.GetC2Operation(ctx, operationID)
			if op == nil {
				return
			}
			evidence := db.C2Evidence{
				Kind: kind, Title: task.TaskType, Mime: mime, Content: result.Text,
				SHA256: result.SHA256, Size: int64(result.Size), Redacted: result.Redacted,
				MetadataJSON: c2MustJSON(map[string]any{"task_id": taskID, "status": status, "exit_code": task.ExitCode}),
			}
			if err := s.recordEvidence(ctx, op, evidence); err != nil {
				_ = s.pg.MarkC2OperationFailed(ctx, operationID, err.Error())
				return
			}
			final := "completed"
			errText := ""
			if status != "completed" || task.ExitCode != 0 {
				final = "failed"
				errText = firstNonEmpty(task.Error, status)
			}
			_ = s.pg.MarkC2OperationResult(ctx, operationID, final, sanitize.Summary(output, 600), result.SHA256, errText)
			return
		}
	}
}

func (s *c2Service) recordEvidence(ctx context.Context, op *db.C2Operation, item db.C2Evidence) error {
	item.OperationID = op.ID
	item.TaskID = op.TaskID
	item.C2SessionID = op.C2SessionID
	if item.Kind == "" {
		item.Kind = "output"
	}
	if item.Title == "" {
		item.Title = op.Tool
	}
	if item.Mime == "" {
		item.Mime = "text/plain"
	}
	_, err := s.pg.AddC2Evidence(ctx, item)
	return err
}

func (s *c2Service) operationDetail(ctx context.Context, id int64) (*c2OperationDetail, error) {
	op, err := s.pg.GetC2Operation(ctx, id)
	if err != nil || op == nil {
		return nil, err
	}
	approvals, err := s.pg.ListC2OperationApprovals(ctx, id)
	if err != nil {
		return nil, err
	}
	evidence, err := s.pg.ListC2Evidence(ctx, id)
	if err != nil {
		return nil, err
	}
	return &c2OperationDetail{Operation: op, Approvals: approvals, Evidence: evidence, Scope: op.ScopeSnapshot}, nil
}

func (s *c2Service) bindEvidenceToFinding(ctx context.Context, evidenceID, findingID int64, actor, reason string) error {
	item, err := s.pg.GetC2Evidence(ctx, evidenceID)
	if err != nil || item == nil {
		return errors.New("C2 evidence not found")
	}
	if item.FindingID != nil && *item.FindingID == findingID {
		return nil
	}
	metadata := item.MetadataJSON
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	var meta map[string]any
	_ = json.Unmarshal(metadata, &meta)
	meta["source"] = "c2"
	meta["c2_evidence_id"] = item.ID
	meta["c2_operation_id"] = item.OperationID
	meta["c2_session_id"] = item.C2SessionID
	raw, _ := json.Marshal(meta)
	if _, _, err := s.pg.AddFindingEvidence(ctx, findingID, nil, actor, reason, db.FindingEvidence{
		Kind: "c2_" + item.Kind, Title: item.Title, Content: item.Content,
		SHA256: item.SHA256, Size: item.Size, Redacted: item.Redacted, MetadataJSON: raw,
	}); err != nil {
		return err
	}
	return s.pg.SetC2EvidenceFinding(ctx, evidenceID, findingID)
}

func c2SessionIDFromOperation(ctx context.Context, pg *db.DB, op *db.C2Operation) string {
	var id string
	_ = pg.QueryRowContext(ctx, `SELECT external_session_id FROM c2_sessions WHERE id=$1`, op.C2SessionID).Scan(&id)
	return id
}

func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		var out int64
		_, _ = fmt.Sscan(n, &out)
		return out
	default:
		return 0
	}
}

func intFromAny(v any) int {
	return int(int64FromAny(v))
}

func c2MustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func c2TaskSnapshot(task *db.Task) json.RawMessage {
	if task == nil {
		return json.RawMessage(`{}`)
	}
	return c2MustJSON(map[string]any{
		"task_id": task.ID, "name": task.Name, "goal": task.Goal, "status": task.Status,
	})
}

func c2SessionSnapshot(session *db.C2Session) json.RawMessage {
	if session == nil || session.Binding == nil {
		return json.RawMessage(`{}`)
	}
	return c2MustJSON(map[string]any{
		"session_id": session.ExternalSessionID, "binding_id": session.Binding.ID,
		"hostname": session.Hostname, "username": session.Username, "os": session.OS,
		"arch": session.Arch, "status": session.Status, "raw_hash": session.RawHash,
	})
}
