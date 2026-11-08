package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const plannerHandoffTimeout = 2 * time.Second

func (p *Planner) SetHandoffStore(store *db.DB) {
	p.todoMu.Lock()
	p.handoffStore = store
	if p.handoffVersions == nil {
		p.handoffVersions = map[int64]int64{}
	}
	p.todoMu.Unlock()
}

func plannerTodosToDB(items []actool.Todo) []db.PlannerHandoffTodo {
	out := make([]db.PlannerHandoffTodo, 0, len(items))
	for _, item := range items {
		out = append(out, db.PlannerHandoffTodo{Content: item.Content, Status: string(item.Status)})
	}
	return out
}

func plannerTodosFromDB(items []db.PlannerHandoffTodo) []actool.Todo {
	out := make([]actool.Todo, 0, len(items))
	for _, item := range items {
		status := actool.TodoStatus(item.Status)
		if status != actool.TodoPending && status != actool.TodoInProgress && status != actool.TodoCompleted {
			status = actool.TodoPending
		}
		out = append(out, actool.Todo{Content: item.Content, Status: status})
	}
	return out
}

func (p *Planner) restorePlannerHandoff(ctx context.Context, taskID, expID int64, todos *actool.TodoStore, emit func(db.Activity)) {
	if p.handoffStore == nil || len(todos.List()) > 0 {
		return
	}
	save, restore := p.handoffStore.PlannerHandoffOptions()
	_ = save
	if !restore {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, plannerHandoffTimeout)
	defer cancel()
	handoff, err := p.handoffStore.LoadPlannerHandoff(queryCtx, taskID)
	if err != nil || handoff == nil {
		if err != nil && emit != nil {
			emit(db.Activity{Worker: "planner", Kind: "planner_handoff", IsError: true,
				Summary: "Planner 交接恢复失败，继续使用内存待办", Detail: err.Error()})
		}
		return
	}
	todos.Restore(plannerTodosFromDB(handoff.Todos))
	p.todoMu.Lock()
	p.handoffVersions[expID] = handoff.Version
	p.todoMu.Unlock()
	if emit != nil {
		metadata, _ := json.Marshal(map[string]any{"version": handoff.Version, "todos": len(handoff.Todos)})
		emit(db.Activity{Worker: "planner", Kind: "planner_handoff", Summary: "Planner 交接已恢复",
			Detail: "已恢复跨唤醒规划待办；业务状态、权限和检查预算不受影响", Metadata: metadata})
	}
}

func (p *Planner) savePlannerHandoff(ctx context.Context, taskID, expID int64, todos *actool.TodoStore, emit func(db.Activity)) {
	if p.handoffStore == nil {
		return
	}
	save, _ := p.handoffStore.PlannerHandoffOptions()
	if !save {
		return
	}
	p.todoMu.Lock()
	expected := p.handoffVersions[expID]
	p.todoMu.Unlock()
	queryCtx, cancel := context.WithTimeout(ctx, plannerHandoffTimeout)
	defer cancel()
	version, err := p.handoffStore.SavePlannerHandoff(queryCtx, taskID, expected, plannerTodosToDB(todos.List()))
	if err != nil {
		if emit != nil {
			emit(db.Activity{Worker: "planner", Kind: "planner_handoff", IsError: true,
				Summary: "Planner 交接保存失败", Detail: err.Error()})
		}
		return
	}
	p.todoMu.Lock()
	p.handoffVersions[expID] = version
	p.todoMu.Unlock()
}
