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

type FindingMergeJob struct {
	ID                  int64     `json:"id,string"`
	TaskID              *int64    `json:"task_id,string"`
	FindingIDs          []int64   `json:"finding_ids"`
	CaseID              *int64    `json:"case_id,string"`
	CaseIDs             []int64   `json:"case_ids"`
	UngroupedFindingIDs []int64   `json:"ungrouped_finding_ids"`
	GroupCount          int       `json:"group_count"`
	Mode                string    `json:"mode"`
	Status              string    `json:"status"`
	Error               string    `json:"error,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type FindingCaseAnalysis struct {
	FindingIDs     []int64
	Title          string
	Reason         string
	Severity       string
	SeverityReason string
	Report         string
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

func (d *DB) CreateFindingMergeJob(ctx context.Context, ids []int64, mode string) (int64, error) {
	mode = strings.TrimSpace(mode)
	if mode != "cluster" && mode != "regenerate" {
		return 0, errors.New("无效归并任务模式")
	}
	ids, err := sortedFindingIDs(ids)
	if err != nil {
		return 0, err
	}
	var taskID int64
	for _, id := range ids {
		var findingTask sql.NullInt64
		if err := d.QueryRowContext(ctx, `SELECT task_id FROM findings WHERE id=$1`, id).Scan(&findingTask); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, fmt.Errorf("漏洞 #%d 不存在", id)
			}
			return 0, err
		}
		if !findingTask.Valid {
			return 0, errors.New("存在未关联任务的漏洞，无法归并")
		}
		if taskID == 0 {
			taskID = findingTask.Int64
		} else if findingTask.Int64 != taskID {
			return 0, errors.New("只能归并同一任务的漏洞")
		}
	}
	raw, _ := json.Marshal(ids)
	var jobID int64
	err = d.QueryRowContext(ctx, `INSERT INTO finding_merge_jobs(task_id,finding_ids,mode)
		VALUES($1,$2,$3) RETURNING id`, taskID, string(raw), mode).Scan(&jobID)
	return jobID, err
}

func (d *DB) GetFindingMergeJob(ctx context.Context, id int64) (*FindingMergeJob, error) {
	var job FindingMergeJob
	var rawFindingIDs, rawCaseIDs, rawUngrouped []byte
	err := d.QueryRowContext(ctx, `SELECT id,task_id,finding_ids,case_id,case_ids,
		ungrouped_finding_ids,group_count,mode,status,error,created_at,updated_at
		FROM finding_merge_jobs WHERE id=$1`, id).
		Scan(&job.ID, &job.TaskID, &rawFindingIDs, &job.CaseID, &rawCaseIDs,
			&rawUngrouped, &job.GroupCount, &job.Mode, &job.Status, &job.Error,
			&job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(rawFindingIDs, &job.FindingIDs)
	_ = json.Unmarshal(rawCaseIDs, &job.CaseIDs)
	_ = json.Unmarshal(rawUngrouped, &job.UngroupedFindingIDs)
	return &job, nil
}

func (d *DB) SetFindingMergeJobStatus(ctx context.Context, id int64, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE finding_merge_jobs
		SET status=$2,error='',updated_at=now() WHERE id=$1`, id, status)
	return err
}

func (d *DB) FinishFindingMergeJob(ctx context.Context, id int64, caseIDs, ungrouped []int64, status, message string) error {
	var caseArg any
	if len(caseIDs) > 0 {
		caseArg = caseIDs[0]
	}
	caseRaw, _ := json.Marshal(caseIDs)
	ungroupedRaw, _ := json.Marshal(ungrouped)
	_, err := d.ExecContext(ctx, `UPDATE finding_merge_jobs
		SET status=$2,case_id=$3,case_ids=$4,ungrouped_finding_ids=$5,
		    group_count=$6,error=$7,updated_at=now() WHERE id=$1`,
		id, status, caseArg, string(caseRaw), string(ungroupedRaw), len(caseIDs), message)
	return err
}

func (d *DB) RecoverInterruptedFindingMergeJobs(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `UPDATE finding_merge_jobs
		SET status='failed',error='服务重启，归并任务中断，请重试',updated_at=now()
		WHERE status IN ('queued','running','clustering','reporting','saving')`)
	return err
}

func (d *DB) ActiveFindingCasesForTask(ctx context.Context, taskID int64) ([]FindingCase, error) {
	if taskID <= 0 {
		return []FindingCase{}, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT `+findingCaseCols+`
		FROM finding_cases c WHERE c.task_id=$1 AND c.active
		ORDER BY c.created_at DESC,c.id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingCase{}
	for rows.Next() {
		item, err := scanFindingCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := d.QueryRowContext(ctx, `SELECT count(*),
			count(*) FILTER (WHERE severity='critical'),
			count(*) FILTER (WHERE severity='high'),
			count(*) FILTER (WHERE severity='medium'),
			count(*) FILTER (WHERE severity='low')
			FROM findings f JOIN finding_case_members m ON m.finding_id=f.id WHERE m.case_id=$1`, out[i].ID).
			Scan(&out[i].Count, &out[i].Critical, &out[i].High, &out[i].Medium, &out[i].Low); err != nil {
			return nil, err
		}
	}
	return out, nil
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
// taskID may be zero: the task is then inferred from the finding rows, which is
// the normal UI path because finding IDs already carry their owning task.
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
		var err error
		caseID, err = mergeFindingCaseTx(tx, taskID, ids, title, reason, actor)
		return err
	})
	return caseID, err
}

func mergeFindingCaseTx(tx *sql.Tx, taskID int64, ids []int64, title, reason, actor string) (int64, error) {
	groupSet := map[int64]bool{}
	derivedTaskID := int64(0)
	for _, id := range ids {
		var task sql.NullInt64
		var currentCase int64
		if err := tx.QueryRow(`SELECT f.task_id,COALESCE(m.case_id,0)
			FROM findings f LEFT JOIN finding_case_members m ON m.finding_id=f.id
			WHERE f.id=$1`, id).Scan(&task, &currentCase); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, fmt.Errorf("漏洞 #%d 不存在", id)
			}
			return 0, err
		}
		if !task.Valid {
			return 0, errors.New("存在未关联任务的漏洞，无法归并")
		}
		if derivedTaskID == 0 {
			derivedTaskID = task.Int64
		} else if task.Int64 != derivedTaskID {
			return 0, errors.New("只能归并同一任务的漏洞")
		}
		if currentCase > 0 {
			groupSet[currentCase] = true
		}
	}
	if taskID > 0 && taskID != derivedTaskID {
		return 0, errors.New("所选漏洞不属于指定任务")
	}
	taskID = derivedTaskID
	if err := lockFindingCaseTask(tx, taskID); err != nil {
		return 0, err
	}
	groups := make([]int64, 0, len(groupSet))
	for id := range groupSet {
		groups = append(groups, id)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	var caseID int64
	if len(groups) > 0 {
		caseID = groups[0]
	} else {
		if err := tx.QueryRow(`INSERT INTO finding_cases(task_id,origin_task_id,title,reason)
			VALUES($1,$1,$2,$3) RETURNING id`, taskID, title, reason).Scan(&caseID); err != nil {
			return 0, err
		}
	}
	var oldTitle, oldReason string
	var active bool
	if err := tx.QueryRow(`SELECT title,reason,active FROM finding_cases WHERE id=$1 FOR UPDATE`, caseID).
		Scan(&oldTitle, &oldReason, &active); err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(ids)
	changed := int64(0)
	for _, id := range ids {
		res, err := tx.Exec(`INSERT INTO finding_case_members(finding_id,case_id)
			VALUES($1,$2)
			ON CONFLICT(finding_id) DO UPDATE SET case_id=EXCLUDED.case_id
			WHERE finding_case_members.case_id<>EXCLUDED.case_id`, id, caseID)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		changed += n
	}
	for _, old := range groups {
		if old == caseID {
			continue
		}
		var remaining int
		if err := tx.QueryRow(`SELECT count(*) FROM finding_case_members WHERE case_id=$1`, old).
			Scan(&remaining); err != nil {
			return 0, err
		}
		if remaining >= 2 {
			if _, err := tx.Exec(`UPDATE finding_cases SET version=version+1 WHERE id=$1`, old); err != nil {
				return 0, err
			}
			continue
		}
		if _, err := tx.Exec(`DELETE FROM finding_case_members WHERE case_id=$1`, old); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE finding_cases SET active=false,version=version+1 WHERE id=$1`, old); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason,finding_ids)
			VALUES($1,'dissolved','system',$2,'[]'::jsonb)`, old,
			fmt.Sprintf("归并到漏洞组 #%d 后成员不足两条", caseID)); err != nil {
			return 0, err
		}
	}
	if changed > 0 || oldTitle != title || oldReason != reason || !active {
		if _, err := tx.Exec(`UPDATE finding_cases SET title=$2,reason=$3,active=true,version=version+1 WHERE id=$1`,
			caseID, title, reason); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason,finding_ids)
			VALUES($1,'merge',$2,$3,$4)`, caseID, actor, reason, string(raw)); err != nil {
			return 0, err
		}
	}
	return caseID, nil
}

// SaveFindingCaseAnalysis is the single-group compatibility wrapper.
func (d *DB) SaveFindingCaseAnalysis(ctx context.Context, taskID int64, ids []int64, title, reason, severity, severityReason, report, actor string) (int64, error) {
	caseIDs, err := d.SaveFindingCaseAnalyses(ctx, taskID, []FindingCaseAnalysis{{
		FindingIDs: ids, Title: title, Reason: reason, Severity: severity,
		SeverityReason: severityReason, Report: report,
	}}, nil, actor)
	if err != nil {
		return 0, err
	}
	return caseIDs[0], nil
}

// SaveFindingCaseAnalyses validates and persists a complete multi-group Agent
// result in one transaction. Groups must be disjoint and each must contain at
// least two findings.
func (d *DB) SaveFindingCaseAnalyses(ctx context.Context, taskID int64, analyses []FindingCaseAnalysis, ungrouped []int64, actor string) ([]int64, error) {
	if len(analyses) == 0 {
		return nil, errors.New("Agent 未生成任何漏洞组")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, errors.New("操作者必填")
	}
	seen := map[int64]bool{}
	normalized := make([]FindingCaseAnalysis, 0, len(analyses))
	for i := range analyses {
		item := analyses[i]
		ids, err := sortedFindingIDs(item.FindingIDs)
		if err != nil {
			return nil, fmt.Errorf("漏洞组 %d: %w", i+1, err)
		}
		for _, id := range ids {
			if seen[id] {
				return nil, fmt.Errorf("漏洞 #%d 被重复分到多个组", id)
			}
			seen[id] = true
		}
		item.FindingIDs = ids
		item.Title = strings.TrimSpace(item.Title)
		item.Reason = strings.TrimSpace(item.Reason)
		item.Severity = strings.TrimSpace(item.Severity)
		item.SeverityReason = strings.TrimSpace(item.SeverityReason)
		item.Report = strings.TrimSpace(item.Report)
		if item.Title == "" || item.Reason == "" || item.SeverityReason == "" ||
			item.Report == "" || !ValidSeverity(item.Severity) {
			return nil, fmt.Errorf("漏洞组 %d 缺少名称、理由、有效等级、评级依据或统一报告", i+1)
		}
		normalized = append(normalized, item)
	}
	for _, id := range ungrouped {
		if id <= 0 {
			return nil, fmt.Errorf("无效未归并 Finding #%d", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("漏洞 #%d 同时出现在漏洞组和未归并列表", id)
		}
		seen[id] = true
	}

	caseIDs := make([]int64, 0, len(normalized))
	err := d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		for _, item := range normalized {
			caseID, err := mergeFindingCaseTx(tx, taskID, item.FindingIDs,
				item.Title, item.Reason, actor)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE finding_cases
				SET title=$2,reason=$3,severity=$4,severity_reason=$5,report=$6,
				    report_version=report_version+1,version=version+1,active=true
				WHERE id=$1`, caseID, item.Title, item.Reason, item.Severity,
				item.SeverityReason, item.Report); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason,finding_ids)
				VALUES($1,'agent_analysis_saved',$2,$3,$4)`, caseID, actor,
				item.Reason, mustJSON(item.FindingIDs)); err != nil {
				return err
			}
			caseIDs = append(caseIDs, caseID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return caseIDs, nil
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// DeleteFindingCase removes a case and its membership/event rows. Findings and
// their evidence stay intact; deleting a delivery grouping must never delete the
// underlying vulnerability records.
func (d *DB) DeleteFindingCase(ctx context.Context, caseID int64) error {
	if caseID <= 0 {
		return errors.New("无效漏洞组编号")
	}
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRow(`SELECT id FROM finding_cases WHERE id=$1 FOR UPDATE`, caseID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("漏洞组不存在")
			}
			return err
		}
		// Delete members first so the existing member trigger can finish while
		// the parent case is still present. Then remove all generated audit rows.
		if _, err := tx.Exec(`DELETE FROM finding_case_members WHERE case_id=$1`, caseID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM finding_case_events WHERE case_id=$1`, caseID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM finding_cases WHERE id=$1`, caseID)
		return err
	})
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
			SET title=$2,report=$3,severity=$4,severity_reason=$5,
			    report_version=report_version+1,version=version+1
			WHERE id=$1`, caseID, title, report, severity, reason); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason)
			VALUES($1,'report_updated','human',$2)`, caseID, reason)
		return err
	})
}
