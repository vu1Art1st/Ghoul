package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	PlannerHandoffSaveSetting    = "planner_handoff_save"
	PlannerHandoffRestoreSetting = "planner_handoff_restore"
)

var ErrPlannerHandoffConflict = errors.New("planner handoff version changed")

type PlannerHandoffTodo struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type PlannerHandoff struct {
	TaskID    int64                `json:"task_id"`
	Version   int64                `json:"version"`
	Todos     []PlannerHandoffTodo `json:"todos"`
	UpdatedAt time.Time            `json:"updated_at"`
}

func (d *DB) PlannerHandoffOptions() (save, restore bool) {
	return d.GetBool(PlannerHandoffSaveSetting, true), d.GetBool(PlannerHandoffRestoreSetting, true)
}

func (d *DB) LoadPlannerHandoff(ctx context.Context, taskID int64) (*PlannerHandoff, error) {
	var out PlannerHandoff
	var raw []byte
	err := d.QueryRowContext(ctx, `SELECT task_id,version,todos,updated_at FROM planner_handoffs WHERE task_id=$1`, taskID).
		Scan(&out.TaskID, &out.Version, &raw, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &out.Todos); err != nil {
		return nil, fmt.Errorf("decode planner handoff: %w", err)
	}
	return &out, nil
}

func (d *DB) SavePlannerHandoff(ctx context.Context, taskID, expectedVersion int64, todos []PlannerHandoffTodo) (int64, error) {
	raw, err := json.Marshal(todos)
	if err != nil {
		return 0, err
	}
	if expectedVersion <= 0 {
		var version int64
		err := d.QueryRowContext(ctx, `
INSERT INTO planner_handoffs(task_id,version,todos,updated_at)
VALUES($1,1,$2,now())
ON CONFLICT(task_id) DO NOTHING
RETURNING version`, taskID, string(raw)).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrPlannerHandoffConflict
		}
		return version, err
	}
	var version int64
	err = d.QueryRowContext(ctx, `
UPDATE planner_handoffs
SET version=version+1,todos=$3,updated_at=now()
WHERE task_id=$1 AND version=$2
RETURNING version`, taskID, expectedVersion, string(raw)).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPlannerHandoffConflict
	}
	return version, err
}
