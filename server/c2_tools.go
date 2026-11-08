package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func (s *Server) seedC2Tools() {
	if s.c2 == nil {
		return
	}
	for _, tool := range s.c2Tools() {
		schema, _ := json.Marshal(tool.InputSchema())
		agents := []string{"worker"}
		switch tool.Name() {
		case "c2_session_list", "c2_session_context", "c2_recon":
			agents = []string{"planner", "worker"}
		}
		bindings, _ := json.Marshal(agents)
		_ = s.m.pg.SeedTool(tool.Name(), tool.Description(), schema, bindings)
	}
}

// c2Tools is the fixed eight-tool Ghoul surface. Read tools are available to
// planner and worker; Confirm and Danger tools are worker-only and deferred
// until the worker's task has an active C2 session binding.
func (s *Server) c2Tools() []actool.CoreTool {
	if s.c2 == nil || !s.c2.enabled() {
		return nil
	}
	return []actool.CoreTool{
		roTool("c2_session_list", "List Toshell sessions bound to the current Ghoul task. Read-only.",
			objSchema(map[string]any{}), func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
				taskID := agent.RunInfoFrom(ctx).TaskID
				items, err := s.m.pg.ListC2Sessions(ctx, db.C2SessionFilter{TaskID: taskID, BoundOnly: true, Limit: 100})
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{"count": len(items), "sessions": items})
			}),
		roTool("c2_session_context", "Read context, listener, capabilities, and recent task summaries for one bound Toshell session.",
			objSchema(map[string]any{"session_id": strParam("task-bound Toshell session ID")}, "session_id"),
			func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
				var in struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				session, err := s.c2BoundSession(ctx, in.SessionID)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				tasks, _ := s.m.pg.ListC2Tasks(ctx, s.c2.instance(), in.SessionID, 20)
				listeners, _ := s.m.pg.ListC2Listeners(ctx, s.c2.instance())
				var listener any
				for _, item := range listeners {
					if item.ExternalListenerID == session.ListenerID {
						listener = item
						break
					}
				}
				return jsonResult(map[string]any{"session": session, "listener": listener, "recent_tasks": tasks})
			}),
		roTool("c2_recon", "Execute one allow-listed read-only recon action on the task-bound Toshell session. Raw output is materialized as evidence and only a summary is returned.",
			objSchema(map[string]any{
				"session_id": strParam("task-bound Toshell session ID"),
				"action":     map[string]any{"type": "string", "enum": []string{"user_info", "system_info", "net_info", "net_connections", "env_vars", "scheduled_tasks"}},
			}, "session_id", "action"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
				var in struct {
					SessionID string `json:"session_id"`
					Action    string `json:"action"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				session, err := s.c2BoundSession(ctx, in.SessionID)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				allowed := map[string]bool{"user_info": true, "system_info": true, "net_info": true,
					"net_connections": true, "env_vars": true, "scheduled_tasks": true}
				if !allowed[in.Action] {
					return actool.Errorf("recon action is not allowed"), nil
				}
				op, err := s.c2.submitOperation(ctx, c2OperationRequest{
					TaskID: agent.RunInfoFrom(ctx).TaskID, SessionID: session.ExternalSessionID,
					Tool: "c2_recon", Action: in.Action, Params: map[string]any{"action": in.Action},
					Actor: "agent", Source: "agent",
				})
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				op, err = s.c2.executeOperation(ctx, op.ID)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return c2OperationToolResult(op)
			}),
		roTool("c2_file_list", "List a directory on a task-bound Toshell session. Creates a pending Confirm operation requiring human approval.",
			objSchema(map[string]any{
				"session_id": strParam("task-bound Toshell session ID"),
				"path":       strParam("absolute directory path on the target"),
			}, "session_id", "path"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
				var in struct {
					SessionID string `json:"session_id"`
					Path      string `json:"path"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return s.submitC2Tool(ctx, "c2_file_list", in.SessionID, map[string]any{"path": in.Path})
			}),
		roTool("c2_process_list", "List processes on a task-bound Toshell session. Creates a pending Confirm operation requiring human approval.",
			objSchema(map[string]any{"session_id": strParam("task-bound Toshell session ID")}, "session_id"),
			func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
				var in struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return s.submitC2Tool(ctx, "c2_process_list", in.SessionID, map[string]any{})
			}),
		roTool("c2_screenshot", "Capture a screenshot from a task-bound Toshell session. Creates a pending Confirm operation requiring human approval.",
			objSchema(map[string]any{
				"session_id": strParam("task-bound Toshell session ID"),
				"monitor":    map[string]any{"type": "integer", "minimum": 0, "maximum": 16},
				"max_width":  map[string]any{"type": "integer", "minimum": 0, "maximum": 7680},
				"format":     map[string]any{"type": "string", "enum": []string{"auto", "png", "jpeg"}},
				"quality":    map[string]any{"type": "integer", "minimum": 20, "maximum": 95},
			}, "session_id"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
				var in struct {
					SessionID string `json:"session_id"`
					Monitor   *int   `json:"monitor"`
					MaxWidth  *int   `json:"max_width"`
					Format    string `json:"format"`
					Quality   *int   `json:"quality"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				params := map[string]any{}
				if in.Monitor != nil {
					params["monitor"] = *in.Monitor
				}
				if in.MaxWidth != nil {
					params["max_width"] = *in.MaxWidth
				}
				if in.Format != "" {
					params["format"] = in.Format
				}
				if in.Quality != nil {
					params["quality"] = *in.Quality
				}
				return s.submitC2Tool(ctx, "c2_screenshot", in.SessionID, params)
			}),
		roTool("c2_tunnel", "List, start, or stop a Toshell SOCKS5 tunnel. Start and stop require human approval.",
			objSchema(map[string]any{
				"session_id": strParam("task-bound Toshell session ID"),
				"action":     map[string]any{"type": "string", "enum": []string{"list", "start", "stop"}},
				"local_port": map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
				"tunnel_id":  strParam("tunnel/session id for stop"),
			}, "session_id", "action"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
				var in struct {
					SessionID string `json:"session_id"`
					Action    string `json:"action"`
					LocalPort int    `json:"local_port"`
					TunnelID  string `json:"tunnel_id"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				params := map[string]any{"action": in.Action}
				if in.LocalPort > 0 {
					params["local_port"] = in.LocalPort
				}
				if in.TunnelID != "" {
					params["tunnel_id"] = in.TunnelID
				}
				return s.submitC2Tool(ctx, "c2_tunnel", in.SessionID, params)
			}),
		roTool("c2_exec", "Run one command on a task-bound Toshell session. Danger level; disabled unless c2.allow_danger is enabled and two approval stages are completed.",
			objSchema(map[string]any{
				"session_id":  strParam("task-bound Toshell session ID"),
				"command":     strParam("exact command to execute"),
				"timeout_sec": map[string]any{"type": "integer", "minimum": 1, "maximum": 300},
			}, "session_id", "command"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
				var in struct {
					SessionID  string `json:"session_id"`
					Command    string `json:"command"`
					TimeoutSec int    `json:"timeout_sec"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				params := map[string]any{"command": in.Command}
				if in.TimeoutSec > 0 {
					params["timeout_sec"] = in.TimeoutSec
				}
				return s.submitC2Tool(ctx, "c2_exec", in.SessionID, params)
			}),
	}
}

func (s *Server) submitC2Tool(ctx context.Context, tool, sessionID string, params map[string]any) (actool.Result, error) {
	if s.c2 == nil {
		return actool.Errorf("C2 adapter is disabled"), nil
	}
	taskID := agent.RunInfoFrom(ctx).TaskID
	if _, err := s.c2BoundSession(ctx, sessionID); err != nil {
		return actool.Errorf(err.Error()), nil
	}
	op, err := s.c2.submitOperation(ctx, c2OperationRequest{
		TaskID: taskID, SessionID: sessionID, Tool: tool, Params: params,
		Actor: "agent", Source: "agent",
	})
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return c2OperationToolResult(op)
}

func c2OperationToolResult(op *db.C2Operation) (actool.Result, error) {
	if op == nil {
		return actool.Errorf("C2 operation was not created"), nil
	}
	return jsonResult(map[string]any{
		"operation_id":      op.ID,
		"status":            op.Status,
		"tool":              op.Tool,
		"level":             op.Level,
		"approval_required": op.ApprovalRequired,
		"approval_stages":   op.ApprovalStages,
		"external_task_id":  op.ExternalTaskID,
		"output_summary":    op.OutputSummary,
		"note":              "Use the Ghoul C2 approval center. Raw output and approval tokens are never returned to the agent.",
	})
}

func (s *Server) c2BoundSession(ctx context.Context, externalID string) (*db.C2Session, error) {
	if s.c2 == nil {
		return nil, fmt.Errorf("C2 adapter is disabled")
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	taskID := agent.RunInfoFrom(ctx).TaskID
	items, err := s.m.pg.ListC2Sessions(ctx, db.C2SessionFilter{TaskID: taskID, BoundOnly: true, Limit: 1000})
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ExternalSessionID == externalID {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf("session is not bound to the current task")
}
