package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// C2Operation is the immutable-ish audit record for one Ghoul-originated C2
// request. ParamsJSON is already redacted by the service; it is not a secret
// store.
type C2Operation struct {
	ID               int64                `json:"id,string"`
	InstanceID       int64                `json:"instance_id,string"`
	TaskID           int64                `json:"task_id,string"`
	C2SessionID      int64                `json:"c2_session_id,string"`
	BindingID        *int64               `json:"binding_id,string,omitempty"`
	Tool             string               `json:"tool"`
	Action           string               `json:"action"`
	Level            string               `json:"level"`
	Actor            string               `json:"actor"`
	Source           string               `json:"source"`
	ParamsJSON       json.RawMessage      `json:"params"`
	ParamsHash       string               `json:"params_hash"`
	IdempotencyKey   string               `json:"idempotency_key"`
	Status           string               `json:"status"`
	ApprovalRequired bool                 `json:"approval_required"`
	ApprovalStages   int                  `json:"approval_stages"`
	ScopeSnapshot    json.RawMessage      `json:"scope_snapshot"`
	ExternalTaskID   int64                `json:"external_task_id,omitempty"`
	OutputSummary    string               `json:"output_summary,omitempty"`
	OutputSHA256     string               `json:"output_sha256,omitempty"`
	Error            string               `json:"error,omitempty"`
	RequestedAt      time.Time            `json:"requested_at"`
	ApprovedAt       *time.Time           `json:"approved_at,omitempty"`
	DispatchedAt     *time.Time           `json:"dispatched_at,omitempty"`
	CompletedAt      *time.Time           `json:"completed_at,omitempty"`
	ExpiresAt        *time.Time           `json:"expires_at,omitempty"`
	UpdatedAt        time.Time            `json:"updated_at"`
	Approval         *C2OperationApproval `json:"approval,omitempty"`
}

type C2OperationInput struct {
	InstanceID       int64
	TaskID           int64
	C2SessionID      int64
	BindingID        int64
	Tool             string
	Action           string
	Level            string
	Actor            string
	Source           string
	ParamsJSON       json.RawMessage
	ParamsHash       string
	IdempotencyKey   string
	Status           string
	ApprovalRequired bool
	ApprovalStages   int
	ScopeSnapshot    json.RawMessage
	ExpiresAt        *time.Time
}

const c2OperationCols = `id,instance_id,task_id,c2_session_id,binding_id,tool,action,level,
	actor,source,params_json,params_hash,idempotency_key,status,approval_required,
	approval_stages,scope_snapshot,external_task_id,output_summary,output_sha256,error,
	requested_at,approved_at,dispatched_at,completed_at,expires_at,updated_at`

func scanC2Operation(row interface{ Scan(...any) error }) (*C2Operation, error) {
	var item C2Operation
	var params, scope []byte
	var binding sql.NullInt64
	var approved, dispatched, completed, expires sql.NullTime
	err := row.Scan(&item.ID, &item.InstanceID, &item.TaskID, &item.C2SessionID, &binding,
		&item.Tool, &item.Action, &item.Level, &item.Actor, &item.Source, &params,
		&item.ParamsHash, &item.IdempotencyKey, &item.Status, &item.ApprovalRequired,
		&item.ApprovalStages, &scope, &item.ExternalTaskID, &item.OutputSummary,
		&item.OutputSHA256, &item.Error, &item.RequestedAt, &approved, &dispatched,
		&completed, &expires, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if binding.Valid {
		item.BindingID = &binding.Int64
	}
	item.ParamsJSON, item.ScopeSnapshot = params, scope
	if approved.Valid {
		item.ApprovedAt = &approved.Time
	}
	if dispatched.Valid {
		item.DispatchedAt = &dispatched.Time
	}
	if completed.Valid {
		item.CompletedAt = &completed.Time
	}
	if expires.Valid {
		item.ExpiresAt = &expires.Time
	}
	return &item, nil
}

func (d *DB) CreateC2Operation(ctx context.Context, in C2OperationInput) (*C2Operation, error) {
	if in.InstanceID <= 0 || in.TaskID <= 0 || in.C2SessionID <= 0 || strings.TrimSpace(in.Tool) == "" {
		return nil, errors.New("instance_id, task_id, c2_session_id and tool are required")
	}
	if in.Status == "" {
		in.Status = "pending_approval"
	}
	if in.Actor == "" {
		in.Actor = "agent"
	}
	if in.Source == "" {
		in.Source = "agent"
	}
	if in.ApprovalStages <= 0 {
		in.ApprovalStages = 1
	}
	if in.ApprovalStages > 2 {
		in.ApprovalStages = 2
	}
	if len(in.ParamsJSON) == 0 {
		in.ParamsJSON = json.RawMessage(`{}`)
	}
	if len(in.ScopeSnapshot) == 0 {
		in.ScopeSnapshot = json.RawMessage(`[]`)
	}
	var binding any
	if in.BindingID > 0 {
		binding = in.BindingID
	}
	var expires any
	if in.ExpiresAt != nil {
		expires = *in.ExpiresAt
	}
	var item C2Operation
	var params, scope []byte
	var scanBinding sql.NullInt64
	var approved, dispatched, completed, expiresAt sql.NullTime
	err := d.QueryRowContext(ctx, `INSERT INTO c2_operations
		(instance_id,task_id,c2_session_id,binding_id,tool,action,level,actor,source,
		 params_json,params_hash,idempotency_key,status,approval_required,approval_stages,
		 scope_snapshot,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING `+c2OperationCols,
		in.InstanceID, in.TaskID, in.C2SessionID, binding, in.Tool, in.Action, in.Level,
		in.Actor, in.Source, string(in.ParamsJSON), in.ParamsHash, in.IdempotencyKey,
		in.Status, in.ApprovalRequired, in.ApprovalStages, string(in.ScopeSnapshot), expires).
		Scan(&item.ID, &item.InstanceID, &item.TaskID, &item.C2SessionID, &scanBinding,
			&item.Tool, &item.Action, &item.Level, &item.Actor, &item.Source, &params,
			&item.ParamsHash, &item.IdempotencyKey, &item.Status, &item.ApprovalRequired,
			&item.ApprovalStages, &scope, &item.ExternalTaskID, &item.OutputSummary,
			&item.OutputSHA256, &item.Error, &item.RequestedAt, &approved, &dispatched,
			&completed, &expiresAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if scanBinding.Valid {
		item.BindingID = &scanBinding.Int64
	}
	item.ParamsJSON, item.ScopeSnapshot = params, scope
	if approved.Valid {
		item.ApprovedAt = &approved.Time
	}
	if dispatched.Valid {
		item.DispatchedAt = &dispatched.Time
	}
	if completed.Valid {
		item.CompletedAt = &completed.Time
	}
	if expiresAt.Valid {
		item.ExpiresAt = &expiresAt.Time
	}
	return &item, nil
}

func (d *DB) GetC2Operation(ctx context.Context, id int64) (*C2Operation, error) {
	item, err := scanC2Operation(d.QueryRowContext(ctx, `SELECT `+c2OperationCols+
		` FROM c2_operations WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	if item != nil {
		item.Approval, _ = d.GetC2OperationApproval(ctx, id, 1)
	}
	return item, nil
}

func (d *DB) GetC2OperationByIdempotency(ctx context.Context, instanceID int64, key string) (*C2Operation, error) {
	item, err := scanC2Operation(d.QueryRowContext(ctx, `SELECT `+c2OperationCols+
		` FROM c2_operations WHERE instance_id=$1 AND idempotency_key=$2`, instanceID, key))
	if err != nil {
		return nil, err
	}
	if item != nil {
		item.Approval, _ = d.GetC2OperationApproval(ctx, item.ID, 1)
	}
	return item, nil
}

type C2OperationFilter struct {
	TaskID    int64
	Status    string
	SessionID string
	Limit     int
}

func (d *DB) ListC2Operations(ctx context.Context, filter C2OperationFilter) ([]C2Operation, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{}
	conds := []string{}
	if filter.TaskID > 0 {
		args = append(args, filter.TaskID)
		conds = append(conds, fmt.Sprintf("task_id=$%d", len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		conds = append(conds, fmt.Sprintf("status=$%d", len(args)))
	}
	if filter.SessionID != "" {
		args = append(args, filter.SessionID)
		conds = append(conds, fmt.Sprintf("c2_session_id IN (SELECT id FROM c2_sessions WHERE external_session_id=$%d)", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit)
	rows, err := d.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM c2_operations%s
		ORDER BY requested_at DESC,id DESC LIMIT $%d`, c2OperationCols, where, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2Operation{}
	for rows.Next() {
		item, err := scanC2Operation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Approval, _ = d.GetC2OperationApproval(ctx, out[i].ID, 1)
	}
	return out, nil
}

type C2OperationApproval struct {
	ID              int64           `json:"id,string"`
	OperationID     int64           `json:"operation_id,string"`
	Stage           int             `json:"stage"`
	TokenHash       string          `json:"-"`
	ParamsHash      string          `json:"params_hash"`
	TaskSnapshot    json.RawMessage `json:"task_snapshot"`
	SessionSnapshot json.RawMessage `json:"session_snapshot"`
	Status          string          `json:"status"`
	ApprovedBy      string          `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time      `json:"approved_at,omitempty"`
	ConsumedAt      *time.Time      `json:"consumed_at,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	ExpiresAt       time.Time       `json:"expires_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

type C2OperationApprovalInput struct {
	OperationID     int64
	Stage           int
	TokenHash       string
	ParamsHash      string
	TaskSnapshot    json.RawMessage
	SessionSnapshot json.RawMessage
	ExpiresAt       time.Time
}

const c2ApprovalCols = `id,operation_id,stage,token_hash,params_hash,task_snapshot,
	session_snapshot,status,approved_by,approved_at,consumed_at,reason,expires_at,created_at`

func scanC2Approval(row interface{ Scan(...any) error }) (*C2OperationApproval, error) {
	var item C2OperationApproval
	var task, session []byte
	var approvedAt, consumedAt sql.NullTime
	err := row.Scan(&item.ID, &item.OperationID, &item.Stage, &item.TokenHash, &item.ParamsHash,
		&task, &session, &item.Status, &item.ApprovedBy, &approvedAt, &consumedAt,
		&item.Reason, &item.ExpiresAt, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item.TaskSnapshot, item.SessionSnapshot = task, session
	if approvedAt.Valid {
		item.ApprovedAt = &approvedAt.Time
	}
	if consumedAt.Valid {
		item.ConsumedAt = &consumedAt.Time
	}
	return &item, nil
}

func (d *DB) CreateC2OperationApproval(ctx context.Context, in C2OperationApprovalInput) (*C2OperationApproval, error) {
	if in.OperationID <= 0 || in.Stage <= 0 || in.TokenHash == "" || in.ParamsHash == "" || in.ExpiresAt.IsZero() {
		return nil, errors.New("operation_id, stage, token_hash, params_hash and expires_at are required")
	}
	if len(in.TaskSnapshot) == 0 {
		in.TaskSnapshot = json.RawMessage(`{}`)
	}
	if len(in.SessionSnapshot) == 0 {
		in.SessionSnapshot = json.RawMessage(`{}`)
	}
	return scanC2Approval(d.QueryRowContext(ctx, `INSERT INTO c2_operation_approvals
		(operation_id,stage,token_hash,params_hash,task_snapshot,session_snapshot,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+c2ApprovalCols,
		in.OperationID, in.Stage, in.TokenHash, in.ParamsHash, string(in.TaskSnapshot),
		string(in.SessionSnapshot), in.ExpiresAt))
}

func (d *DB) GetC2OperationApproval(ctx context.Context, operationID int64, stage int) (*C2OperationApproval, error) {
	return scanC2Approval(d.QueryRowContext(ctx, `SELECT `+c2ApprovalCols+
		` FROM c2_operation_approvals WHERE operation_id=$1 AND stage=$2`, operationID, stage))
}

func (d *DB) ListC2OperationApprovals(ctx context.Context, operationID int64) ([]C2OperationApproval, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+c2ApprovalCols+
		` FROM c2_operation_approvals WHERE operation_id=$1 ORDER BY stage`, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2OperationApproval{}
	for rows.Next() {
		item, err := scanC2Approval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

// ApproveC2OperationApproval consumes one approval stage. The operation becomes
// approved only after every required stage is approved.
func (d *DB) ApproveC2OperationApproval(ctx context.Context, operationID int64, stage int, actor, reason string) (*C2OperationApproval, bool, error) {
	if operationID <= 0 || stage <= 0 || strings.TrimSpace(actor) == "" {
		return nil, false, errors.New("operation_id, stage and actor are required")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var status string
	var expires time.Time
	if err := tx.QueryRowContext(ctx, `SELECT status,expires_at FROM c2_operation_approvals
		WHERE operation_id=$1 AND stage=$2 FOR UPDATE`, operationID, stage).Scan(&status, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, errors.New("approval stage not found")
		}
		return nil, false, err
	}
	if status == "approved" || status == "consumed" {
		return nil, false, errors.New("approval stage already used")
	}
	if status != "pending" {
		return nil, false, fmt.Errorf("approval stage is %s", status)
	}
	if time.Now().After(expires) {
		_, _ = tx.ExecContext(ctx, `UPDATE c2_operation_approvals SET status='expired' WHERE operation_id=$1 AND stage=$2`, operationID, stage)
		return nil, false, errors.New("approval stage expired")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE c2_operation_approvals
		SET status='approved',approved_by=$3,approved_at=now(),reason=$4
		WHERE operation_id=$1 AND stage=$2`, operationID, stage, actor, reason); err != nil {
		return nil, false, err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM c2_operation_approvals
		WHERE operation_id=$1 AND status='pending'`, operationID).Scan(&pending); err != nil {
		return nil, false, err
	}
	opApproved := pending == 0
	if opApproved {
		if _, err := tx.ExecContext(ctx, `UPDATE c2_operations
			SET status='approved',approved_at=now(),error=''
			WHERE id=$1 AND status='pending_approval'`, operationID); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	item, err := d.GetC2OperationApproval(ctx, operationID, stage)
	return item, opApproved, err
}

func (d *DB) RejectC2OperationApproval(ctx context.Context, operationID int64, stage int, actor, reason string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE c2_operation_approvals
		SET status='rejected',approved_by=$3,approved_at=now(),reason=$4
		WHERE operation_id=$1 AND stage=$2 AND status='pending'`, operationID, stage, actor, reason); err != nil {
		return err
	}
	errorText := strings.TrimSpace(reason)
	if errorText == "" {
		errorText = "approval rejected"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE c2_operations
		SET status='denied',error=$2,completed_at=now()
		WHERE id=$1 AND status IN ('pending_approval','approved')`, operationID, errorText); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) MarkC2OperationDispatched(ctx context.Context, operationID, externalTaskID int64) error {
	_, err := d.ExecContext(ctx, `UPDATE c2_operations
		SET status='dispatched',external_task_id=$2,dispatched_at=now(),error=''
		WHERE id=$1 AND status IN ('approved','dispatching')`, operationID, externalTaskID)
	return err
}

func (d *DB) MarkC2OperationRunning(ctx context.Context, operationID int64) error {
	_, err := d.ExecContext(ctx, `UPDATE c2_operations SET status='running'
		WHERE id=$1 AND status IN ('approved','dispatching','dispatched')`, operationID)
	return err
}

func (d *DB) MarkC2OperationResult(ctx context.Context, operationID int64, status, outputSummary, outputSHA, errText string) error {
	if status != "completed" && status != "failed" {
		return errors.New("terminal C2 operation status must be completed or failed")
	}
	_, err := d.ExecContext(ctx, `UPDATE c2_operations
		SET status=$2,output_summary=$3,output_sha256=$4,error=$5,completed_at=now()
		WHERE id=$1 AND status NOT IN ('completed','failed','denied','expired','cancelled')`,
		operationID, status, outputSummary, outputSHA, errText)
	return err
}

func (d *DB) MarkC2OperationFailed(ctx context.Context, operationID int64, errText string) error {
	return d.MarkC2OperationResult(ctx, operationID, "failed", "", "", errText)
}

func (d *DB) TaskScopeCount(ctx context.Context, taskID int64) (int, error) {
	if taskID <= 0 {
		return 0, nil
	}
	var count int
	err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_scope WHERE task_id=$1`, taskID).Scan(&count)
	return count, err
}

func (d *DB) TaskScopeSnapshot(ctx context.Context, taskID int64) (json.RawMessage, int, error) {
	if taskID <= 0 {
		return json.RawMessage(`[]`), 0, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT kind,COALESCE(domain,''),COALESCE(net::text,''),
		COALESCE(value,''),source,COALESCE(reason,'')
		FROM task_scope WHERE task_id=$1 ORDER BY id`, taskID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var kind, domain, network, value, source, reason string
		if err := rows.Scan(&kind, &domain, &network, &value, &source, &reason); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]string{
			"kind": kind, "domain": domain, "net": network, "value": value,
			"source": source, "reason": reason,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	raw, err := json.Marshal(out)
	return raw, len(out), err
}

type C2Evidence struct {
	ID           int64           `json:"id,string"`
	OperationID  int64           `json:"operation_id,string"`
	TaskID       int64           `json:"task_id,string"`
	C2SessionID  int64           `json:"c2_session_id,string"`
	FindingID    *int64          `json:"finding_id,string,omitempty"`
	Kind         string          `json:"kind"`
	Title        string          `json:"title"`
	Mime         string          `json:"mime"`
	Content      string          `json:"content,omitempty"`
	StorageRef   string          `json:"storage_ref,omitempty"`
	SHA256       string          `json:"sha256"`
	Size         int64           `json:"size"`
	Redacted     bool            `json:"redacted"`
	MetadataJSON json.RawMessage `json:"metadata"`
	CreatedAt    time.Time       `json:"created_at"`
}

const c2EvidenceCols = `id,operation_id,task_id,c2_session_id,finding_id,kind,title,mime,
	content,storage_ref,sha256,size,redacted,metadata_json,created_at`

func scanC2Evidence(row interface{ Scan(...any) error }) (*C2Evidence, error) {
	var item C2Evidence
	var finding sql.NullInt64
	var metadata []byte
	err := row.Scan(&item.ID, &item.OperationID, &item.TaskID, &item.C2SessionID, &finding,
		&item.Kind, &item.Title, &item.Mime, &item.Content, &item.StorageRef, &item.SHA256,
		&item.Size, &item.Redacted, &metadata, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if finding.Valid {
		item.FindingID = &finding.Int64
	}
	item.MetadataJSON = metadata
	return &item, nil
}

func (d *DB) AddC2Evidence(ctx context.Context, item C2Evidence) (*C2Evidence, error) {
	if item.OperationID <= 0 || item.TaskID <= 0 || item.C2SessionID <= 0 {
		return nil, errors.New("operation_id, task_id and c2_session_id are required")
	}
	if item.Kind == "" {
		item.Kind = "output"
	}
	if item.Title == "" {
		item.Title = item.Kind
	}
	if item.Mime == "" {
		item.Mime = "text/plain"
	}
	if item.SHA256 == "" {
		sum := sha256.Sum256([]byte(item.Content))
		item.SHA256 = hex.EncodeToString(sum[:])
	}
	if item.Size == 0 {
		item.Size = int64(len(item.Content))
	}
	if len(item.MetadataJSON) == 0 {
		item.MetadataJSON = json.RawMessage(`{}`)
	}
	var finding any
	if item.FindingID != nil && *item.FindingID > 0 {
		finding = *item.FindingID
	}
	return scanC2Evidence(d.QueryRowContext(ctx, `INSERT INTO c2_evidence
		(operation_id,task_id,c2_session_id,finding_id,kind,title,mime,content,storage_ref,
		 sha256,size,redacted,metadata_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING `+c2EvidenceCols,
		item.OperationID, item.TaskID, item.C2SessionID, finding, item.Kind, item.Title,
		item.Mime, item.Content, item.StorageRef, item.SHA256, item.Size, item.Redacted,
		string(item.MetadataJSON)))
}

func (d *DB) ListC2Evidence(ctx context.Context, operationID int64) ([]C2Evidence, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+c2EvidenceCols+
		` FROM c2_evidence WHERE operation_id=$1 ORDER BY id`, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2Evidence{}
	for rows.Next() {
		item, err := scanC2Evidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

func (d *DB) GetC2Evidence(ctx context.Context, id int64) (*C2Evidence, error) {
	return scanC2Evidence(d.QueryRowContext(ctx, `SELECT `+c2EvidenceCols+
		` FROM c2_evidence WHERE id=$1`, id))
}

func (d *DB) SetC2EvidenceFinding(ctx context.Context, evidenceID, findingID int64) error {
	if evidenceID <= 0 || findingID <= 0 {
		return errors.New("evidence_id and finding_id are required")
	}
	res, err := d.ExecContext(ctx, `UPDATE c2_evidence SET finding_id=$2 WHERE id=$1`, evidenceID, findingID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("C2 evidence not found")
	}
	return nil
}
