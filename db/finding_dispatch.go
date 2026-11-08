package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type FindingDispatch struct {
	ID              int64     `json:"id,string"`
	FindingID       int64     `json:"finding_id,string"`
	TaskID          *int64    `json:"task_id,string"`
	AgentKey        string    `json:"agent_key"`
	ConversationID  *int64    `json:"conversation_id,string"`
	Status          string    `json:"status"`
	EvidenceVersion int64     `json:"evidence_version"`
	Reason          string    `json:"reason"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const findingDispatchCols = `id,finding_id,task_id,agent_key,conversation_id,status,evidence_version,reason,created_by,created_at,updated_at`

func scanFindingDispatch(row interface{ Scan(...any) error }) (*FindingDispatch, error) {
	var d FindingDispatch
	err := row.Scan(&d.ID, &d.FindingID, &d.TaskID, &d.AgentKey, &d.ConversationID, &d.Status,
		&d.EvidenceVersion, &d.Reason, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &d, err
}

// CreateFindingDispatch freezes the finding evidence version at dispatch time.
func (d *DB) CreateFindingDispatch(ctx context.Context, findingID int64, agentKey, reason, actor string) (*FindingDispatch, error) {
	agentKey = strings.TrimSpace(agentKey)
	if agentKey == "" {
		agentKey = "worker"
	}
	reason = strings.TrimSpace(reason)
	actor = strings.TrimSpace(actor)
	if reason == "" || actor == "" {
		return nil, errors.New("派发原因与操作者必填")
	}
	var out *FindingDispatch
	err := d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var taskID sql.NullInt64
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT task_id,evidence_version FROM findings WHERE id=$1 FOR UPDATE`, findingID).Scan(&taskID, &version); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrFindingNotFound
			}
			return err
		}
		var err error
		out, err = scanFindingDispatch(tx.QueryRowContext(ctx, `INSERT INTO finding_dispatches
			(finding_id,task_id,agent_key,evidence_version,reason,created_by)
			VALUES($1,$2,$3,$4,$5,$6) RETURNING `+findingDispatchCols,
			findingID, taskID, agentKey, version, reason, actor))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE findings
			SET evidence_audit=evidence_audit || jsonb_build_array(jsonb_build_object(
			  'at',now(),'event','dispatch','actor',$2::text,'agent',$3::text,'reason',$4::text))
			WHERE id=$1`, findingID, actor, agentKey, reason)
		return err
	})
	return out, err
}

func (d *DB) ListFindingDispatches(ctx context.Context, findingID int64) ([]FindingDispatch, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+findingDispatchCols+`
		FROM finding_dispatches WHERE finding_id=$1 ORDER BY id DESC`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingDispatch{}
	for rows.Next() {
		item, err := scanFindingDispatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}
