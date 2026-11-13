package server

import (
	"context"
	"encoding/json"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

func (s *Server) knowledgeTools() []actool.CoreTool {
	return []actool.CoreTool{s.toolKnowledgeSearch()}
}

func (s *Server) seedKnowledgeTools() {
	for _, t := range s.knowledgeTools() {
		schema, _ := json.Marshal(t.InputSchema())
		agents, _ := json.Marshal([]string{"planner", "mainagent", "worker"})
		_ = s.m.pg.SeedTool(t.Name(), t.Description(), schema, agents)
		for _, key := range []string{"planner", "mainagent", "worker"} {
			_ = s.m.pg.AddAgentToToolBinding(key, []string{t.Name()})
		}
	}
}

func (s *Server) toolKnowledgeSearch() actool.CoreTool {
	return wrTool("knowledge_search",
		"统一检索历史任务、内部 PoC、CTF 赛题和公开漏洞情报。scope=tasks|poc|ctf|cve|all。只读；返回结果可用于决定 source_task_ids 或 shared_materialize。",
		objSchema(map[string]any{
			"scope": map[string]any{
				"type": "string",
				"enum": []string{"tasks", "poc", "ctf", "cve", "all"},
			},
			"q":       strParam("目标、产品、组件、CVE、漏洞名或 CTF 题目关键词"),
			"version": strParam("可选组件版本，用于 CVE 匹配"),
			"task_id": map[string]any{"type": "integer", "description": "当前任务 ID，用于排除自身并推荐历史任务"},
			"limit":   map[string]any{"type": "integer", "description": "每类结果上限，默认 10，最大 30"},
		}, "scope", "q"),
		func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
			var in struct {
				Scope   string `json:"scope"`
				Q       string `json:"q"`
				Version string `json:"version"`
				TaskID  int64  `json:"task_id"`
				Limit   int    `json:"limit"`
			}
			_ = json.Unmarshal(raw, &in)
			in.Q = strings.TrimSpace(in.Q)
			if in.Limit <= 0 || in.Limit > 30 {
				in.Limit = 10
			}
			out := map[string]any{"scope": in.Scope, "query": in.Q}
			switch in.Scope {
			case "tasks":
				items, err := s.m.pg.SuggestTaskRelations(ctx, in.TaskID, []string{in.Q}, in.Limit)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				out["tasks"] = items
			case "poc":
				items, err := s.m.pg.SearchPocRecordsScope(ctx, in.Q, "", in.Limit)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				out["pocs"] = items
			case "ctf":
				items, err := s.m.pg.SearchPocRecordsScope(ctx, in.Q, "ctf", in.Limit)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				out["ctf"] = items
			case "cve":
				if s.vulnIntel == nil || s.vulnIntel.store == nil {
					return actool.Errorf("漏洞情报库不可用"), nil
				}
				items, err := s.vulnIntel.store.SearchVulns(ctx, "", in.Q, in.Version, in.Limit)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				out["cves"] = items
			case "all":
				taskItems, _ := s.m.pg.SuggestTaskRelations(ctx, in.TaskID, []string{in.Q}, in.Limit)
				pocItems, _ := s.m.pg.SearchPocRecordsScope(ctx, in.Q, "", in.Limit)
				ctfItems, _ := s.m.pg.SearchPocRecordsScope(ctx, in.Q, "ctf", in.Limit)
				out["tasks"] = taskItems
				out["pocs"] = pocItems
				out["ctf"] = ctfItems
				if s.vulnIntel != nil && s.vulnIntel.store != nil {
					if cves, err := s.vulnIntel.store.SearchVulns(ctx, "", in.Q, in.Version, in.Limit); err == nil {
						out["cves"] = cves
					}
				}
			default:
				return actool.Errorf("不支持的 scope: " + in.Scope), nil
			}
			return jsonResult(out)
		})
}
