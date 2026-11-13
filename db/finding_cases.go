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
	RootCauseKey   string    `json:"root_cause_key,omitempty"`
	Severity       string    `json:"severity"`
	SeverityReason string    `json:"severity_reason"`
	Report         string    `json:"report,omitempty"`
	Version        int64     `json:"version"`
	ReportVersion  int64     `json:"report_version"`
	ReportSource   int64     `json:"report_source_version"`
	ReportStale    bool      `json:"report_stale"`
	ManagedBy      string    `json:"managed_by"`
	Locked         bool      `json:"locked"`
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
	InputHash           string    `json:"input_hash,omitempty"`
	Attempts            int       `json:"attempts"`
	Status              string    `json:"status"`
	Error               string    `json:"error,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type FindingCaseAnalysis struct {
	FindingIDs     []int64
	RootCauseKey   string
	Title          string
	Reason         string
	Severity       string
	SeverityReason string
	Report         string
}

type FindingAutoMergeInput struct {
	FindingID             int64
	EvidenceVersion       int64
	ReportEvidenceVersion int64
	CaseID                int64
	CaseVersion           int64
	CaseRootCauseKey      string
	CaseManagedBy         string
	CaseLocked            bool
}

type FindingCaseBlockPair struct {
	LeftID  int64
	RightID int64
}

const findingCaseCols = `c.id,c.task_id,c.title,c.reason,c.root_cause_key,c.severity,c.severity_reason,c.report,c.version,c.report_version,c.report_source_version,c.managed_by,c.locked,c.active,c.created_at`

func scanFindingCase(row interface{ Scan(...any) error }) (*FindingCase, error) {
	c := &FindingCase{}
	err := row.Scan(&c.ID, &c.TaskID, &c.Title, &c.Reason, &c.RootCauseKey,
		&c.Severity, &c.SeverityReason, &c.Report, &c.Version, &c.ReportVersion,
		&c.ReportSource, &c.ManagedBy, &c.Locked, &c.Active, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err == nil {
		c.ReportStale = strings.TrimSpace(c.Report) == "" || c.ReportSource != c.Version
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

// CreateAutoFindingMergeJob is idempotent for a task/input fingerprint. The
// returned created flag lets the scheduler avoid launching the same job twice
// when task-done reconciliation runs concurrently with a restart.
func (d *DB) CreateAutoFindingMergeJob(ctx context.Context, ids []int64, inputHash string) (int64, bool, error) {
	inputHash = strings.TrimSpace(inputHash)
	if inputHash == "" {
		return 0, false, errors.New("自动归并输入指纹不能为空")
	}
	ids, err := sortedFindingIDs(ids)
	if err != nil {
		return 0, false, err
	}
	var taskID int64
	for _, id := range ids {
		var findingTask sql.NullInt64
		if err := d.QueryRowContext(ctx, `SELECT task_id FROM findings WHERE id=$1`, id).Scan(&findingTask); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, false, fmt.Errorf("漏洞 #%d 不存在", id)
			}
			return 0, false, err
		}
		if !findingTask.Valid {
			return 0, false, errors.New("存在未关联任务的漏洞，无法自动归并")
		}
		if taskID == 0 {
			taskID = findingTask.Int64
		} else if taskID != findingTask.Int64 {
			return 0, false, errors.New("自动归并只能处理同一任务的漏洞")
		}
	}
	raw, _ := json.Marshal(ids)
	var jobID int64
	err = d.QueryRowContext(ctx, `INSERT INTO finding_merge_jobs(task_id,finding_ids,mode,input_hash)
		VALUES($1,$2,'auto',$3)
		ON CONFLICT (task_id,input_hash) WHERE mode='auto' AND input_hash<>'' DO NOTHING
		RETURNING id`, taskID, string(raw), inputHash).Scan(&jobID)
	if err == nil {
		return jobID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if err := d.QueryRowContext(ctx, `SELECT id FROM finding_merge_jobs
		WHERE task_id=$1 AND mode='auto' AND input_hash=$2`, taskID, inputHash).Scan(&jobID); err != nil {
		return 0, false, err
	}
	return jobID, false, nil
}

func (d *DB) GetFindingMergeJob(ctx context.Context, id int64) (*FindingMergeJob, error) {
	var job FindingMergeJob
	var rawFindingIDs, rawCaseIDs, rawUngrouped []byte
	err := d.QueryRowContext(ctx, `SELECT id,task_id,finding_ids,case_id,case_ids,
		ungrouped_finding_ids,group_count,mode,input_hash,attempts,status,error,created_at,updated_at
		FROM finding_merge_jobs WHERE id=$1`, id).
		Scan(&job.ID, &job.TaskID, &rawFindingIDs, &job.CaseID, &rawCaseIDs,
			&rawUngrouped, &job.GroupCount, &job.Mode, &job.InputHash, &job.Attempts, &job.Status, &job.Error,
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

func (d *DB) LatestAutoFindingMergeJobForTask(ctx context.Context, taskID int64) (*FindingMergeJob, error) {
	if taskID <= 0 {
		return nil, nil
	}
	var id int64
	err := d.QueryRowContext(ctx, `SELECT id FROM finding_merge_jobs
		WHERE task_id=$1 AND mode='auto' ORDER BY id DESC LIMIT 1`, taskID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d.GetFindingMergeJob(ctx, id)
}

// ListFindingAutoMergeInputs returns the task findings eligible for automatic
// clustering together with the active case state needed to compute an input
// fingerprint. Locked cases are returned so callers can exclude them explicitly.
func (d *DB) ListFindingAutoMergeInputs(ctx context.Context, taskID int64) ([]FindingAutoMergeInput, error) {
	if taskID <= 0 {
		return nil, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT f.id,f.evidence_version,COALESCE(f.report_evidence_version,0),
		COALESCE(c.id,0),COALESCE(c.version,0),COALESCE(c.root_cause_key,''),
		COALESCE(c.managed_by,''),COALESCE(c.locked,false)
		FROM findings f
		LEFT JOIN finding_case_members m ON m.finding_id=f.id
		LEFT JOIN finding_cases c ON c.id=m.case_id AND c.active
		WHERE f.task_id=$1 AND f.status NOT IN ($2,$3,$4,$5)
		ORDER BY f.id`,
		taskID, FindingFalsePositive, FindingIgnored, FindingDuplicate, FindingRiskAccepted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingAutoMergeInput{}
	for rows.Next() {
		var item FindingAutoMergeInput
		if err := rows.Scan(&item.FindingID, &item.EvidenceVersion, &item.ReportEvidenceVersion,
			&item.CaseID, &item.CaseVersion, &item.CaseRootCauseKey,
			&item.CaseManagedBy, &item.CaseLocked); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *DB) FindingCaseBlockPairsForTask(ctx context.Context, taskID int64) ([]FindingCaseBlockPair, error) {
	if taskID <= 0 {
		return nil, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT b.left_id,b.right_id
		FROM finding_case_blocks b
		JOIN findings l ON l.id=b.left_id
		JOIN findings r ON r.id=b.right_id
		WHERE l.task_id=$1 AND r.task_id=$1
		ORDER BY b.left_id,b.right_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingCaseBlockPair{}
	for rows.Next() {
		var item FindingCaseBlockPair
		if err := rows.Scan(&item.LeftID, &item.RightID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *DB) ListDoneTaskIDsForAutoMerge(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.QueryContext(ctx, `SELECT t.id
		FROM tasks t
		WHERE t.status='done' AND t.deleted_at IS NULL AND t.archived_at IS NULL
		  AND EXISTS(SELECT 1 FROM findings f WHERE f.task_id=t.id
		             AND f.status NOT IN ($2,$3,$4,$5))
		ORDER BY t.completed_at DESC NULLS LAST,t.id DESC
		LIMIT $1`, limit, FindingFalsePositive, FindingIgnored, FindingDuplicate, FindingRiskAccepted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (d *DB) SetFindingMergeJobStatus(ctx context.Context, id int64, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE finding_merge_jobs
		SET status=$2,error='',updated_at=now() WHERE id=$1`, id, status)
	return err
}

func (d *DB) BeginFindingMergeJob(ctx context.Context, id int64, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE finding_merge_jobs
		SET status=$2,error='',attempts=attempts+1,updated_at=now() WHERE id=$1`, id, status)
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
		caseID, err = mergeFindingCaseTx(tx, taskID, ids, "", title, reason, actor, "manual", true)
		return err
	})
	return caseID, err
}

func mergeFindingCaseTx(tx *sql.Tx, taskID int64, ids []int64, rootCauseKey, title, reason, actor, managedBy string, locked bool) (int64, error) {
	rootCauseKey = strings.TrimSpace(rootCauseKey)
	managedBy = strings.TrimSpace(managedBy)
	if managedBy == "" {
		managedBy = "manual"
	}
	groupSet := map[int64]bool{}
	derivedTaskID := int64(0)
	for _, id := range ids {
		var task sql.NullInt64
		var currentCase int64
		var currentLocked bool
		var currentManagedBy string
		if err := tx.QueryRow(`SELECT f.task_id,COALESCE(m.case_id,0),
			COALESCE(c.locked,false),COALESCE(c.managed_by,'')
			FROM findings f
			LEFT JOIN finding_case_members m ON m.finding_id=f.id
			LEFT JOIN finding_cases c ON c.id=m.case_id
			WHERE f.id=$1`, id).Scan(&task, &currentCase, &currentLocked, &currentManagedBy); err != nil {
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
		if managedBy == "auto" && currentCase > 0 && (currentLocked || currentManagedBy != "auto") {
			return 0, ErrFindingCaseConflict
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
	if rootCauseKey != "" {
		var matched int64
		err := tx.QueryRow(`SELECT id FROM finding_cases
			WHERE task_id=$1 AND origin_task_id=$1 AND root_cause_key=$2
			  AND managed_by='auto' AND NOT locked AND active
			ORDER BY id LIMIT 1`, taskID, rootCauseKey).Scan(&matched)
		if err == nil {
			caseID = matched
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	if caseID == 0 && len(groups) > 0 {
		caseID = groups[0]
	}
	if caseID == 0 {
		if err := tx.QueryRow(`INSERT INTO finding_cases(task_id,origin_task_id,title,reason,
				root_cause_key,managed_by,locked)
			VALUES($1,$1,$2,$3,$4,$5,$6) RETURNING id`,
			taskID, title, reason, rootCauseKey, managedBy, locked).Scan(&caseID); err != nil {
			return 0, err
		}
	}
	var oldTitle, oldReason, oldRootCause, oldManagedBy string
	var oldLocked, active bool
	if err := tx.QueryRow(`SELECT title,reason,root_cause_key,managed_by,locked,active
		FROM finding_cases WHERE id=$1 FOR UPDATE`, caseID).
		Scan(&oldTitle, &oldReason, &oldRootCause, &oldManagedBy, &oldLocked, &active); err != nil {
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
	effectiveRootCause := rootCauseKey
	if effectiveRootCause == "" {
		effectiveRootCause = oldRootCause
	}
	if changed > 0 || oldTitle != title || oldReason != reason || oldRootCause != effectiveRootCause ||
		oldManagedBy != managedBy || oldLocked != locked || !active {
		if _, err := tx.Exec(`UPDATE finding_cases
			SET title=$2,reason=$3,root_cause_key=$4,managed_by=$5,locked=$6,
			    active=true,version=version+1
			WHERE id=$1`,
			caseID, title, reason, effectiveRootCause, managedBy, locked); err != nil {
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
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, errors.New("操作者必填")
	}
	auto := strings.HasSuffix(actor, ":auto")
	managedBy, locked := "manual", true
	if auto {
		managedBy, locked = "auto", false
	}
	if len(analyses) == 0 {
		if !auto {
			return nil, errors.New("Agent 未生成任何漏洞组")
		}
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
		item.RootCauseKey = strings.TrimSpace(item.RootCauseKey)
		item.Title = strings.TrimSpace(item.Title)
		item.Reason = strings.TrimSpace(item.Reason)
		item.Severity = strings.TrimSpace(item.Severity)
		item.SeverityReason = strings.TrimSpace(item.SeverityReason)
		item.Report = strings.TrimSpace(item.Report)
		if item.Title == "" || item.Reason == "" || item.SeverityReason == "" ||
			item.Report == "" || !ValidSeverity(item.Severity) {
			return nil, fmt.Errorf("漏洞组 %d 缺少名称、理由、有效等级、评级依据或统一报告", i+1)
		}
		if auto && item.RootCauseKey == "" {
			return nil, fmt.Errorf("自动漏洞组 %d 缺少根因键", i+1)
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
				item.RootCauseKey, item.Title, item.Reason, actor, managedBy, locked)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE finding_cases
				SET title=$2,reason=$3,severity=$4,severity_reason=$5,report=$6,
				    report_version=report_version+1,report_source_version=version+1,
				    version=version+1,active=true
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
		if !auto {
			return nil
		}
		keep := make(map[int64]bool, len(caseIDs))
		for _, id := range caseIDs {
			keep[id] = true
		}
		rows, err := tx.Query(`SELECT DISTINCT c.id FROM finding_cases c
			JOIN finding_case_members m ON m.case_id=c.id
			WHERE c.task_id=$1 AND c.active AND c.managed_by='auto' AND NOT c.locked
			  AND m.finding_id=ANY($2)`, taskID, sortedSeenIDs(seen))
		if err != nil {
			return err
		}
		var obsolete []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !keep[id] {
				obsolete = append(obsolete, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		_ = rows.Close()
		for _, id := range obsolete {
			if _, err := tx.Exec(`DELETE FROM finding_case_members WHERE case_id=$1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE finding_cases
				SET active=false,version=version+1 WHERE id=$1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason)
				VALUES($1,'auto_dissolved',$2,'自动聚类结果不再包含该洞组')`, id, actor); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return caseIDs, nil
}

func sortedSeenIDs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
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
		if _, err := tx.Exec(`UPDATE finding_cases
			SET managed_by='manual',locked=true WHERE id=$1`, caseID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason,finding_ids)
			VALUES($1,'detach',$2,$3,jsonb_build_array($4::bigint))`, caseID, actor, reason, findingID)
		return err
	})
}

// AddFindingCaseMembers appends findings to an explicit case and marks the
// unified report stale. Re-adding a previously detached finding clears only the
// pairwise blocks between that finding and the current members; it never moves a
// finding from another active group.
func (d *DB) AddFindingCaseMembers(ctx context.Context, caseID, expectedVersion int64, ids []int64, actor, reason string) (int64, error) {
	if caseID <= 0 {
		return 0, errors.New("无效漏洞组编号")
	}
	if expectedVersion <= 0 {
		return 0, errors.New("漏洞组版本必填")
	}
	ids, err := uniquePositiveFindingIDs(ids)
	if err != nil {
		return 0, err
	}
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if actor == "" || reason == "" {
		return 0, errors.New("操作者和添加原因必填")
	}
	var nextVersion int64
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var taskID, version int64
		var active bool
		if err := tx.QueryRow(`SELECT origin_task_id,version,active
			FROM finding_cases WHERE id=$1 FOR UPDATE`, caseID).Scan(&taskID, &version, &active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("漏洞组不存在")
			}
			return err
		}
		if !active {
			return errors.New("已解散漏洞组不能添加成员")
		}
		if version != expectedVersion {
			return ErrFindingCaseConflict
		}
		if err := lockFindingCaseTask(tx, taskID); err != nil {
			return err
		}

		var currentRaw []byte
		if err := tx.QueryRow(`SELECT COALESCE(jsonb_agg(finding_id ORDER BY finding_id),'[]'::jsonb)
			FROM finding_case_members WHERE case_id=$1`, caseID).Scan(&currentRaw); err != nil {
			return err
		}
		var currentIDs []int64
		_ = json.Unmarshal(currentRaw, &currentIDs)
		current := make(map[int64]bool, len(currentIDs))
		for _, id := range currentIDs {
			current[id] = true
		}

		toAdd := make([]int64, 0, len(ids))
		for _, id := range ids {
			var findingTask sql.NullInt64
			var currentCase int64
			if err := tx.QueryRow(`SELECT f.task_id,COALESCE(m.case_id,0)
				FROM findings f LEFT JOIN finding_case_members m ON m.finding_id=f.id
				WHERE f.id=$1 FOR UPDATE OF f`, id).Scan(&findingTask, &currentCase); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("漏洞 #%d 不存在", id)
				}
				return err
			}
			if !findingTask.Valid || findingTask.Int64 != taskID {
				return fmt.Errorf("漏洞 #%d 不属于该任务", id)
			}
			if currentCase == caseID || current[id] {
				continue
			}
			if currentCase > 0 {
				return fmt.Errorf("漏洞 #%d 已属于其他漏洞组 #%d", id, currentCase)
			}
			toAdd = append(toAdd, id)
		}
		if len(toAdd) == 0 {
			nextVersion = version
			return nil
		}

		for _, id := range toAdd {
			if len(currentIDs) > 0 {
				if _, err := tx.Exec(`DELETE FROM finding_case_blocks b
					USING finding_case_members m
					WHERE m.case_id=$1
					  AND ((b.left_id=$2 AND b.right_id=m.finding_id)
					    OR (b.right_id=$2 AND b.left_id=m.finding_id))`, caseID, id); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(`INSERT INTO finding_case_members(finding_id,case_id)
				VALUES($1,$2)`, id, caseID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(`UPDATE finding_cases
			SET managed_by='manual',locked=true,version=version+1
			WHERE id=$1 RETURNING version`, caseID).Scan(&nextVersion); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason,finding_ids)
			VALUES($1,'members_added',$2,$3,$4)`,
			caseID, actor, reason, mustJSON(toAdd))
		return err
	})
	return nextVersion, err
}

func uniquePositiveFindingIDs(ids []int64) ([]int64, error) {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("无效漏洞编号: %d", id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) == 0 {
		return nil, errors.New("至少选择一条漏洞")
	}
	return out, nil
}

func (d *DB) LockFindingCaseForManual(ctx context.Context, caseID int64, actor, reason string) error {
	if caseID <= 0 {
		return errors.New("无效漏洞组编号")
	}
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if actor == "" || reason == "" {
		return errors.New("操作者和原因必填")
	}
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var managedBy string
		var locked, active bool
		if err := tx.QueryRow(`SELECT managed_by,locked,active FROM finding_cases WHERE id=$1 FOR UPDATE`,
			caseID).Scan(&managedBy, &locked, &active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("漏洞组不存在")
			}
			return err
		}
		if !active {
			return errors.New("已解散漏洞组不能重新生成")
		}
		if managedBy == "manual" && locked {
			return nil
		}
		if _, err := tx.Exec(`UPDATE finding_cases SET managed_by='manual',locked=true WHERE id=$1`, caseID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason)
			VALUES($1,'manual_lock',$2,$3)`, caseID, actor, reason)
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
			    report_version=report_version+1,report_source_version=version+1,
			    managed_by='manual',locked=true,version=version+1
			WHERE id=$1`, caseID, title, report, severity, reason); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO finding_case_events(case_id,action,actor,reason)
			VALUES($1,'report_updated','human',$2)`, caseID, reason)
		return err
	})
}
