package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/hosttools"
	actool "github.com/Autumn-27/norma/tool"
)

func (s *Server) runtimeTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolRuntimeStatus(),
		s.toolRuntimeEnsure(),
	}
}

func (s *Server) seedRuntimeTools() {
	bindings := map[string][]string{
		"runtime_status": {"planner", "mainagent", "worker"},
		"runtime_ensure": {"mainagent", "worker"},
	}
	for _, t := range s.runtimeTools() {
		schema, _ := json.Marshal(t.InputSchema())
		agents, _ := json.Marshal(bindings[t.Name()])
		_ = s.m.pg.SeedTool(t.Name(), t.Description(), schema, agents)
		for _, agentKey := range bindings[t.Name()] {
			_ = s.m.pg.AddAgentToToolBinding(agentKey, []string{t.Name()})
		}
	}
}

func (s *Server) toolRuntimeStatus() actool.CoreTool {
	return wrTool("runtime_status",
		"查看当前任务实际使用的 Python、Java、Node.js、uv、mise 路径、版本来源、回退原因和共享包状态。只读。",
		objSchema(map[string]any{}),
		func(_ context.Context, _ json.RawMessage) (actool.Result, error) {
			rt := agent.ResolveRuntime(s.m.dir)
			return jsonResult(map[string]any{
				"runtime":       rt,
				"shared_python": s.shared.PythonStatus(),
				"tools":         hosttools.Inventory(rt.Python),
				"policy": map[string]any{
					"prefer":                     "shared uv venv",
					"system_fallback":            true,
					"system_package_install":     true,
					"system_install_is_volatile": true,
				},
			})
		})
}

func (s *Server) toolRuntimeEnsure() actool.CoreTool {
	return wrTool("runtime_ensure",
		"修复或准备共享运行时。action=ensure_venv|install_python|install_package|install_system_package|repair|install_mise_tool。Python 包优先进入共享 uv venv；共享安装不可用时才允许 install_system_package。",
		objSchema(map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{"ensure_venv", "install_python", "install_package", "install_system_package", "repair", "install_mise_tool"},
			},
			"base_python": strParam("ensure_venv 可选：基础 Python 绝对路径或 python3/python"),
			"version":     strParam("install_python 可选：UV Python 版本，如 3.12"),
			"spec":        strParam("包规格或 mise 工具规格，如 websocket-client==1.8.0 / java@21"),
		}, "action"),
		func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
			if s.installer == nil {
				return actool.Errorf("安装器不可用"), nil
			}
			var in struct {
				Action     string `json:"action"`
				BasePython string `json:"base_python"`
				Version    string `json:"version"`
				Spec       string `json:"spec"`
			}
			_ = json.Unmarshal(raw, &in)
			kind, target := "", ""
			switch in.Action {
			case "ensure_venv":
				kind, target = "python-venv", strings.TrimSpace(in.BasePython)
			case "repair":
				kind, target = "python-venv", "repair"
			case "install_python":
				kind, target = "uv-python", strings.TrimSpace(in.Version)
			case "install_package":
				kind, target = "python-package", strings.TrimSpace(in.Spec)
			case "install_system_package":
				if err := s.requireFailedSharedPackage(ctx, strings.TrimSpace(in.Spec)); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				kind, target = "python-package-system", strings.TrimSpace(in.Spec)
			case "install_mise_tool":
				kind, target = "mise-tool", strings.TrimSpace(in.Spec)
			default:
				return actool.Errorf("不支持的 action: " + in.Action), nil
			}
			job, err := s.installer.Start(kind, target)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			done, err := s.waitInstallJob(ctx, job.ID, 10*time.Minute)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if done.State != "done" {
				msg := strings.TrimSpace(done.Error)
				if msg == "" {
					msg = "安装任务状态 " + done.State
				}
				return jsonResult(map[string]any{"ok": false, "job": done, "error": msg, "runtime": agent.ResolveRuntime(s.m.dir)})
			}
			return jsonResult(map[string]any{"ok": true, "job": done, "runtime": agent.ResolveRuntime(s.m.dir)})
		})
}

func (s *Server) requireFailedSharedPackage(ctx context.Context, spec string) error {
	if err := hosttools.ValidatePackageSpec(spec); err != nil {
		return err
	}
	jobs, err := s.installer.List(ctx)
	if err != nil {
		return fmt.Errorf("读取共享安装审计失败: %w", err)
	}
	for _, job := range jobs {
		if job.Kind != "python-package" || job.Target != spec {
			continue
		}
		switch job.State {
		case "failed", "cancelled", "interrupted":
			return nil
		}
	}
	return fmt.Errorf("系统 Python 安装仅允许在共享 uv 安装失败后执行；请先调用 runtime_ensure action=install_package spec=%s", spec)
}

func (s *Server) waitInstallJob(ctx context.Context, id string, max time.Duration) (*db.HostInstallJob, error) {
	deadline := time.NewTimer(max)
	defer deadline.Stop()
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := s.installer.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if job == nil {
			return nil, fmt.Errorf("安装任务不存在: %s", id)
		}
		switch job.State {
		case "done", "failed", "cancelled", "interrupted":
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-deadline.C:
			return job, fmt.Errorf("安装任务超时: %s", id)
		case <-ticker.C:
		}
	}
}
