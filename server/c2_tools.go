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
		bindings, _ := json.Marshal([]string{"planner", "worker"})
		if err := s.m.pg.SeedTool(tool.Name(), tool.Description(), schema, bindings); err != nil {
			continue
		}
	}
}

// c2Tools is the Phase 3 read-only C2 surface. The schemas stay out of the
// planner's base prompt and are added as deferred host tools only when the C2
// adapter is enabled.
func (s *Server) c2Tools() []actool.CoreTool {
	if s.c2 == nil || !s.c2.enabled() {
		return nil
	}
	return []actool.CoreTool{
		roTool("c2_session_list", "列出已绑定到当前任务的 Toshell 会话。只读；不向目标下发任何动作。",
			objSchema(map[string]any{}), func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
				taskID := agent.RunInfoFrom(ctx).TaskID
				items, err := s.m.pg.ListC2Sessions(ctx, db.C2SessionFilter{TaskID: taskID, BoundOnly: true, Limit: 100})
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{"count": len(items), "sessions": items})
			}),
		roTool("c2_session_context", "读取一个已绑定 Toshell 会话的身份、状态、监听器和近期任务摘要。只读。",
			objSchema(map[string]any{"session_id": strParam("已绑定到当前任务的 Toshell 会话 ID")}, "session_id"),
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
		roTool("c2_recon", "Phase 3 只读侦察：读取已绑定会话和近期任务，返回白名单动作建议；本阶段不执行命令、不创建 Toshell 任务。",
			objSchema(map[string]any{
				"session_id": strParam("已绑定到当前任务的 Toshell 会话 ID"),
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
					return actool.Errorf("action 不在只读白名单内"), nil
				}
				tasks, _ := s.m.pg.ListC2Tasks(ctx, s.c2.instance(), in.SessionID, 20)
				return jsonResult(map[string]any{
					"executed":     false,
					"session":      session,
					"action":       in.Action,
					"recent_tasks": tasks,
					"note":         "Phase 3 只读接入不创建任务；审批式 recon 在 Phase 4/5 启用",
				})
			}),
	}
}

func (s *Server) c2BoundSession(ctx context.Context, externalID string) (*db.C2Session, error) {
	if s.c2 == nil {
		return nil, fmt.Errorf("C2 adapter is disabled")
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return nil, fmt.Errorf("session_id 不能为空")
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
	return nil, fmt.Errorf("会话未绑定到当前任务或不存在")
}
