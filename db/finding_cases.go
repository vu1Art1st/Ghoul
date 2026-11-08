package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrFindingCaseConflict = errors.New("漏洞组已变更，请重新读取后再操作")

type FindingCase struct {
	ID             int64     `json:"id,string"`
	TaskID         *int64    `json:"task_id,string"`
	Title          string    `json:"title"`
	Reason         string    `json:"reason"`
	Severity       string    `json:"severity"`
	SeverityReason string    `json:"severity_reason"`
	Report         string    `json:"report,omitempty"`
	Version        int64     `json:"version"`
	ReportVersion  int64     `json:"report_version"`
	Active         bool      `json:"active"`
	Count          int       `json:"count"`
	Critical       int       `json:"critical"`
	High           int       `json:"high"`
	Medium         int       `json:"medium"`
	Low            int       `json:"low"`
	CreatedAt      time.Time `json:"created_at"`
}

type FindingCaseEvent struct {
	ID         int64           `json:"id,string"`
	Action     string          `json:"action"`
	Actor      string          `json:"actor"`
	Reason     string          `json:"reason"`
	FindingIDs json.RawMessage `json:"finding_ids"`
	CreatedAt  time.Time       `json:"created_at"`
}

const findingCaseCols = `c.id,c.task_id,c.title,c.reason,c.severity,c.severity_reason,c.report,c.version,c.report_version,c.active,c.created_at`

func scanFindingCase(row interface{ Scan(...any) error }) (*FindingCase, error) {
	c := &FindingCase{}
	err := row.Scan(&c.ID, &c.TaskID, &c.Title, &c.Reason, &c.Severity, &c.SeverityReason,
		&c.Report, &c.Version, &c.ReportVersion, &c.Active, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (d *DB) GetFindingCase(ctx context.Context, id int64) (*FindingCase, error) {
	c, err := scanFindingCase(d.QueryRowContext(ctx, `SELECT `+findingCaseCols+` FROM finding_cases c WHERE c.id=$1`, id))
	if err != nil || c == nil {
		return c, err
	}
	err = d.QueryRowContext(ctx, `SELECT count(*),
		count(*) FILTER (WHERE severity='critical'),
		count(*) FILTER (WHERE severity='high'),
		count(*) FILTER (WHERE severity='medium'),
		count(*) FILTER (WHERE severity='low')
		FROM findings f JOIN finding_case_members m ON m.finding_id=f.id WHERE m.case_id=$1`, id).
		Scan(&c.Count, &c.Critical, &c.High, &c.Medium, &c.Low)
	return c, err
}

func (d *DB) ListFindingCases(ctx context.Context, taskID int64, page, size int) ([]FindingCase, int, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	where, args := "", []any{}
	if taskID > 0 {
		args = append(args, taskID)
		where = ` WHERE c.task_id=$1`
	}
	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM finding_cases c`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := d.QueryContext(ctx, fmt.Sprintf(`SELECT `+findingCaseCols+`
		FROM finding_cases c%s ORDER BY c.active DESC,c.created_at DESC,c.id DESC
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []FindingCase{}
	for rows.Next() {
		c, err := scanFindingCase(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range out {
		_ = d.QueryRowContext(ctx, `SELECT count(*),
			count(*) FILTER (WHERE severity='critical'),
			count(*) FILTER (WHERE severity='high'),
			count(*) FILTER (WHERE severity='medium'),
			count(*) FILTER (WHERE severity='low')
			FROM findings f JOIN finding_case_members m ON m.finding_id=f.id WHERE m.case_id=$1`, out[i].ID).
			Scan(&out[i].Count, &out[i].Critical, &out[i].High, &out[i].Medium, &out[i].Low)
	}
	return out, total, nil
}

func (d *DB) FindingCaseMembers(ctx context.Context, id int64) ([]*DBFinding, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+findingSelectCols+`
		FROM findings f
		LEFT JOIN tasks t ON t.id=f.task_id
		JOIN finding_case_members m ON m.finding_id=f.id
		WHERE m.case_id=$1 ORDER BY f.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFindings(rows)
}

func (d *DB) FindingCaseEvents(ctx context.Context, id int64) ([]FindingCaseEvent, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,action,actor,reason,finding_ids,created_at
		FROM finding_case_events WHERE case_id=$1 ORDER BY id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingCaseEvent{}
	for rows.Next() {
		var event FindingCaseEvent
		if err := rows.Scan(&event.ID, &event.Action, &event.Actor, &event.Reason,
			&event.FindingIDs, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func sortedFindingIDs(ids []int64) ([]int64, error) {
	set := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if id <= 0 {
			return nil, errors.New("无效漏洞编号")
		}
		if !set[id] {
			set[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) < 2 {
		return nil, errors.New("至少需要两条漏洞记录")
	}
	return out, nil
}

func lockFindingCaseTask(tx *sql.Tx, taskID int64) error {
	if taskID <= 0 {
		return errors.New("归并需要仍存在的同一任务")
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, int64(5100000000000000)+taskID); err != nil {
		return err
	}
	var id int64
	return tx.QueryRow(`SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, taskID).Scan(&id)
}

// MergeFindingCase groups findings from one task without copying evidence.
func (d *DB) MergeFindingCase(ctx context.Context, taskID int64, ids []int64, title, reason, actor string) (int64, error) {
	ids, err := sortedFindingIDs(ids)
	if err != nil {
		return 0, err
	}
	title, reason, actor = strings.TrimSpace(title), strings.TrimSpace(reason), strings.TrimSpace(actor)
	if title == "" || reason == "" || actor == "" {
		return 0, errors.New("名称、基于证据的归并理由与操作者必填")
	}
	var caseID int64
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := lockFindingCaseTask(tx, taskID); err != nil {
			return err
		}
		groupSet := map[int64]bool{}
		for _, id := range ids {
			var task sql.NullInt64
			var currentCase int64
			if err := tx.QueryRow(`SELECT f.task_id,COALESCE(m.case_id,0)
				FROM findings f LEFT JOIN finding_case_members m ON m.finding_id=f.id
				WHERE f.id=$1`, id).Scan(&task, &currentCase); err != nil {
				return err
			}
			if !task.Valid || task.Int64 != taskID {
				return errors.New("只能归并同一任务的漏洞")
			}
			if currentCase > 0 {
				groupSet[currentCase] = true
			}
		}
		groups := make([]int64, 0, len(groupSet))
		for id := range groupSet {
			groups = append(groups, id)
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
		if len(groups) > 0 {
			caseID = groups[0]
		} else {
			if err := tx.QueryRow(`INSERT INTO finding_cases(task_id,origin_task_id,title,reason)
				VALUES($1,$1,$2,$3) RETURNING id`, taskID, title, reason).Scan(&caseID); err != nil {
				return err
			}
		}
		raw, _ := json.Marshal(ids)
		changed := int64(0)
		for _, id := range ids {
			res, err := tx.Exec(`INSERT INTO finding_case_members(finding_id,case_id)
				VALUES($1,$2)
				ON CONFLICT(finding_id) DO UPDATE SET case_id=EXCLUDED.case_id
				WHERE finding_case_members.case_id<>EXCLUDED.case_id`, id, caseID)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			changed += n
		}
		for _, old := range groups {
			if old != caseID {
				if _, err := tx.Exec(`UPDATE finding_cases SET active=false,version=version+1 WHERE id=$1`, old); err != nil {
					return err
				}
			}
		}
		if changed > 0 {
			if _, err := tx.Exec(`UPDATE finding_cases SET title=$2,reason=$3,version=version+1 WHERE id=$1`,
				caseID, title, reason); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason,finding_ids)
				VALUES($1,'merge',$2,$3,$4)`, caseID, actor, reason, string(raw))
			return err
		}
		return nil
	})
	return caseID, err
}

func (d *DB) DetachFindingCaseMember(ctx context.Context, caseID, findingID int64, actor, reason string) error {
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if actor == "" || reason == "" {
		return errors.New("操作者与移出原因必填")
	}
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var taskID int64
		if err := tx.QueryRow(`SELECT origin_task_id FROM finding_cases WHERE id=$1 FOR UPDATE`, caseID).Scan(&taskID); err != nil {
			return err
		}
		if err := lockFindingCaseTask(tx, taskID); err != nil {
			return err
		}
		var current int64
		if err := tx.QueryRow(`SELECT case_id FROM finding_case_members WHERE finding_id=$1`, findingID).Scan(&current); err != nil {
			return err
		}
		if current != caseID {
			return ErrFindingCaseConflict
		}
		if _, err := tx.Exec(`INSERT INTO finding_case_blocks(left_id,right_id,reason)
			SELECT LEAST($1,finding_id),GREATEST($1,finding_id),$3
			FROM finding_case_members WHERE case_id=$2 AND finding_id<>$1
			ON CONFLICT DO NOTHING`, findingID, caseID, reason); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM finding_case_members WHERE case_id=$1 AND finding_id=$2`, caseID, findingID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason,finding_ids)
			VALUES($1,'detach',$2,$3,jsonb_build_array($4::bigint))`, caseID, actor, reason, findingID)
		return err
	})
}

func (d *DB) UpdateFindingCaseReport(ctx context.Context, caseID, version int64, title, report, severity, reason string) error {
	title, report = strings.TrimSpace(title), strings.TrimSpace(report)
	severity, reason = strings.TrimSpace(severity), strings.TrimSpace(reason)
	if title == "" || report == "" || !ValidSeverity(severity) || reason == "" {
		return errors.New("完整报告、名称、有效等级和评级依据必填")
	}
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		c, err := scanFindingCase(tx.QueryRow(`SELECT `+findingCaseCols+`
			FROM finding_cases c WHERE c.id=$1 FOR UPDATE`, caseID))
		if err != nil {
			return err
		}
		if c == nil || !c.Active || c.Version != version {
			return ErrFindingCaseConflict
		}
		if _, err := tx.Exec(`UPDATE finding_cases
			SET title=$2,report=$3,severity=$4,severity_reason=$5,report_version=$6
			WHERE id=$1`, caseID, title, report, severity, reason, version); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason)
			VALUES($1,'report_updated','human',$2)`, caseID, reason)
		return err
	})
}
