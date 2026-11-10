package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// SharedStore is the small server-owned surface exposed to model-facing tools.
type SharedStore interface {
	List(context.Context, string, bool) ([]*db.SharedAsset, error)
	Get(context.Context, int64) (*db.SharedAsset, error)
	Materialize(context.Context, int64, int, string) (string, error)
}

func SharedTools(store SharedStore) []actool.CoreTool {
	search := readTool("shared_search", "检索已审核发布的跨任务共享脚本、工具和语言库。优先复用已有能力，不要重复开发。", obj(map[string]any{"query": str("名称、用途或标签关键词；留空列出最近资产")}), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
		var args struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(in, &args)
		if store == nil {
			return actool.Errorf("共享空间不可用"), nil
		}
		assets, err := store.List(ctx, args.Query, true)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		type item struct {
			ID          int64    `json:"id"`
			Name        string   `json:"name"`
			Kind        string   `json:"kind"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
			Version     int      `json:"version"`
		}
		out := []item{}
		for _, a := range assets {
			out = append(out, item{a.ID, a.Name, a.Kind, a.Description, a.Tags, a.CurrentVersion})
		}
		return jsonResult(map[string]any{"assets": out, "hint": "用 shared_get 查看调用说明，再用 shared_materialize 复制到当前任务目录。"})
	})
	get := readTool("shared_get", "查看一个已发布共享资产的入口、依赖、使用说明和来源。", obj(map[string]any{"id": idp("shared_search 返回的资产 ID")}, "id"), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
		var args struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(in, &args)
		if store == nil {
			return actool.Errorf("共享空间不可用"), nil
		}
		a, err := store.Get(ctx, args.ID)
		if err != nil || a == nil || a.Status != "published" {
			return actool.Errorf("共享资产不存在或未发布"), nil
		}
		return jsonResult(a)
	})
	materialize := actool.Build(actool.Spec{
		Name: "shared_materialize", Description: "将共享资产的指定版本复制到当前任务工作目录，返回可执行入口路径。复制后可按任务需要修改，不影响共享版本。",
		Schema:      obj(map[string]any{"id": idp("资产 ID"), "version": intp("版本号；0=当前发布版本")}, "id"),
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			var args struct {
				ID      int64 `json:"id"`
				Version int   `json:"version"`
			}
			_ = json.Unmarshal(in, &args)
			if store == nil {
				return actool.Errorf("共享空间不可用"), nil
			}
			workDir := ""
			if tc != nil {
				workDir = tc.WorkingDir
			}
			entry, err := store.Materialize(ctx, args.ID, args.Version, workDir)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"entrypoint": entry, "message": fmt.Sprintf("已复制到当前任务目录；共享版本保持不变。入口：%s", entry)})
		},
	})
	return []actool.CoreTool{search, get, materialize}
}
