package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// SharedAsset is the editable catalog record; published bytes are immutable on disk.
type SharedAsset struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	DisplayName    string          `json:"display_name"`
	Kind           string          `json:"kind"`
	Description    string          `json:"description"`
	Tags           []string        `json:"tags"`
	Language       string          `json:"language"`
	Entrypoint     string          `json:"entrypoint"`
	Usage          string          `json:"usage"`
	Dependencies   []string        `json:"dependencies"`
	RuntimeID      int64           `json:"runtime_id,omitempty"`
	RuntimeSpec    string          `json:"runtime_spec,omitempty"`
	Healthcheck    json.RawMessage `json:"healthcheck"`
	CurrentVersion int             `json:"current_version"`
	Status         string          `json:"status"`
	SourceTaskID   int64           `json:"source_task_id"`
	SourceIntentID int64           `json:"source_intent_id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type SharedAssetVersion struct {
	ID          int64           `json:"id"`
	AssetID     int64           `json:"asset_id"`
	Version     int             `json:"version"`
	SourcePath  string          `json:"source_path"`
	ContentHash string          `json:"content_hash"`
	Validation  json.RawMessage `json:"validation"`
	CreatedAt   time.Time       `json:"created_at"`
}

type SharedAssetEvent struct {
	ID        int64           `json:"id"`
	AssetID   int64           `json:"asset_id"`
	Action    string          `json:"action"`
	Actor     string          `json:"actor"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

var ErrSharedAssetConflict = errors.New("共享资产已被其他操作更新，请刷新后重试")

const sharedAssetCols = `id,name,display_name,kind,description,tags,language,entrypoint,usage,dependencies,
COALESCE(runtime_id,0),runtime_spec,healthcheck,current_version,status,
COALESCE(source_task_id,0),COALESCE(source_intent_id,0),created_at,updated_at`

func scanSharedAsset(scan interface{ Scan(...any) error }) (*SharedAsset, error) {
	var a SharedAsset
	var tags, deps, healthcheck []byte
	if err := scan.Scan(&a.ID, &a.Name, &a.DisplayName, &a.Kind, &a.Description, &tags, &a.Language, &a.Entrypoint, &a.Usage, &deps, &a.RuntimeID, &a.RuntimeSpec, &healthcheck, &a.CurrentVersion, &a.Status, &a.SourceTaskID, &a.SourceIntentID, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if a.DisplayName == "" {
		a.DisplayName = a.Name
	}
	_ = json.Unmarshal(tags, &a.Tags)
	_ = json.Unmarshal(deps, &a.Dependencies)
	if a.Tags == nil {
		a.Tags = []string{}
	}
	if a.Dependencies == nil {
		a.Dependencies = []string{}
	}
	a.Healthcheck = json.RawMessage(healthcheck)
	if len(a.Healthcheck) == 0 {
		a.Healthcheck = json.RawMessage(`{}`)
	}
	return &a, nil
}

func (d *DB) CreateSharedAsset(ctx context.Context, a *SharedAsset) error {
	if a.DisplayName == "" {
		a.DisplayName = a.Name
	}
	tags, _ := json.Marshal(a.Tags)
	deps, _ := json.Marshal(a.Dependencies)
	if len(a.Healthcheck) == 0 {
		a.Healthcheck = json.RawMessage(`{}`)
	}
	return d.QueryRowContext(ctx, `INSERT INTO shared_assets(name,display_name,kind,description,tags,language,entrypoint,usage,dependencies,runtime_id,runtime_spec,healthcheck,status,source_task_id,source_intent_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,0),$11,$12::jsonb,'pending_review',NULLIF($13,0),NULLIF($14,0)) RETURNING id,created_at,updated_at`, a.Name, a.DisplayName, a.Kind, a.Description, tags, a.Language, a.Entrypoint, a.Usage, deps, a.RuntimeID, a.RuntimeSpec, string(a.Healthcheck), a.SourceTaskID, a.SourceIntentID).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
}

func (d *DB) ListSharedAssets(ctx context.Context, query string, publishedOnly bool) ([]*SharedAsset, error) {
	where := "status <> 'archived'"
	if publishedOnly {
		where = "status = 'published'"
	}
	rows, err := d.QueryContext(ctx, `SELECT `+sharedAssetCols+` FROM shared_assets WHERE `+where+` AND ($1='' OR lower(name||' '||display_name||' '||description||' '||tags::text) LIKE '%'||lower($1)||'%') ORDER BY updated_at DESC LIMIT 200`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SharedAsset{}
	for rows.Next() {
		a, err := scanSharedAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (d *DB) SharedAsset(ctx context.Context, id int64) (*SharedAsset, error) {
	a, err := scanSharedAsset(d.QueryRowContext(ctx, `SELECT `+sharedAssetCols+` FROM shared_assets WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func (d *DB) AddSharedAssetVersion(ctx context.Context, v *SharedAssetVersion, commit func(version int) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT current_version FROM shared_assets WHERE id=$1 FOR UPDATE`, v.AssetID).Scan(&current); err != nil {
		return err
	}
	v.Version = current + 1
	if v.Validation == nil {
		v.Validation = json.RawMessage(`{}`)
	}
	if commit != nil {
		if err := commit(v.Version); err != nil {
			return err
		}
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO shared_asset_versions(asset_id,version,source_path,content_hash,validation) VALUES($1,$2,$3,$4,$5) RETURNING id,created_at`, v.AssetID, v.Version, v.SourcePath, v.ContentHash, v.Validation).Scan(&v.ID, &v.CreatedAt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE shared_assets SET current_version=$2,status='published',updated_at=now() WHERE id=$1`, v.AssetID, v.Version)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) SharedAssetVersion(ctx context.Context, assetID int64, version int) (*SharedAssetVersion, error) {
	v := &SharedAssetVersion{}
	err := d.QueryRowContext(ctx, `SELECT id,asset_id,version,source_path,content_hash,validation,created_at FROM shared_asset_versions WHERE asset_id=$1 AND version=$2`, assetID, version).Scan(&v.ID, &v.AssetID, &v.Version, &v.SourcePath, &v.ContentHash, &v.Validation, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return v, err
}

func (d *DB) ListSharedAssetVersions(ctx context.Context, assetID int64) ([]*SharedAssetVersion, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,asset_id,version,source_path,content_hash,validation,created_at
FROM shared_asset_versions WHERE asset_id=$1 ORDER BY version DESC`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SharedAssetVersion{}
	for rows.Next() {
		v := &SharedAssetVersion{}
		if err := rows.Scan(&v.ID, &v.AssetID, &v.Version, &v.SourcePath, &v.ContentHash, &v.Validation, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) SetSharedAssetStatus(ctx context.Context, id int64, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE shared_assets SET status=$2 WHERE id=$1`, id, status)
	return err
}

func (d *DB) UpdateSharedAssetMeta(ctx context.Context, a *SharedAsset) error {
	if a.DisplayName == "" {
		a.DisplayName = a.Name
	}
	tags, _ := json.Marshal(a.Tags)
	deps, _ := json.Marshal(a.Dependencies)
	if len(a.Healthcheck) == 0 {
		a.Healthcheck = json.RawMessage(`{}`)
	}
	_, err := d.ExecContext(ctx, `UPDATE shared_assets SET display_name=$2,description=$3,tags=$4,language=$5,entrypoint=$6,usage=$7,dependencies=$8,runtime_id=NULLIF($9,0),runtime_spec=$10,healthcheck=$11::jsonb WHERE id=$1`,
		a.ID, a.DisplayName, a.Description, tags, a.Language, a.Entrypoint, a.Usage, deps, a.RuntimeID, a.RuntimeSpec, string(a.Healthcheck))
	return err
}

func (d *DB) UpdateSharedAssetMetaIfCurrent(ctx context.Context, a *SharedAsset, expectedUpdatedAt time.Time) (*SharedAsset, error) {
	if a.DisplayName == "" {
		a.DisplayName = a.Name
	}
	tags, _ := json.Marshal(a.Tags)
	deps, _ := json.Marshal(a.Dependencies)
	if len(a.Healthcheck) == 0 {
		a.Healthcheck = json.RawMessage(`{}`)
	}
	result, err := d.ExecContext(ctx, `UPDATE shared_assets
SET display_name=$2,description=$3,tags=$4,language=$5,entrypoint=$6,usage=$7,dependencies=$8,
    runtime_id=NULLIF($9,0),runtime_spec=$10,healthcheck=$11::jsonb
WHERE id=$1 AND updated_at=$12`,
		a.ID, a.DisplayName, a.Description, tags, a.Language, a.Entrypoint, a.Usage, deps,
		a.RuntimeID, a.RuntimeSpec, string(a.Healthcheck), expectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrSharedAssetConflict
	}
	return d.SharedAsset(ctx, a.ID)
}

func (d *DB) CreateSharedAssetEvent(ctx context.Context, event *SharedAssetEvent) error {
	if len(event.Detail) == 0 {
		event.Detail = json.RawMessage(`{}`)
	}
	if event.Actor == "" {
		event.Actor = "web"
	}
	return d.QueryRowContext(ctx, `INSERT INTO shared_asset_events(asset_id,action,actor,detail)
VALUES($1,$2,$3,$4::jsonb) RETURNING id,created_at`, event.AssetID, event.Action, event.Actor, string(event.Detail)).Scan(&event.ID, &event.CreatedAt)
}

func (d *DB) ListSharedAssetEvents(ctx context.Context, assetID int64) ([]*SharedAssetEvent, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,asset_id,action,actor,detail,created_at
FROM shared_asset_events WHERE asset_id=$1 ORDER BY created_at DESC LIMIT 100`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SharedAssetEvent{}
	for rows.Next() {
		event := &SharedAssetEvent{}
		var detail []byte
		if err := rows.Scan(&event.ID, &event.AssetID, &event.Action, &event.Actor, &detail, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Detail = json.RawMessage(detail)
		if len(event.Detail) == 0 {
			event.Detail = json.RawMessage(`{}`)
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

type HostInstallJob struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Target          string     `json:"target"`
	State           string     `json:"state"`
	ExitCode        int        `json:"exit_code"`
	Error           string     `json:"error,omitempty"`
	Log             string     `json:"log"`
	RuntimeID       int64      `json:"runtime_id,omitempty"`
	Language        string     `json:"language,omitempty"`
	ResolvedVersion string     `json:"resolved_version,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
}

func (d *DB) CreateHostInstallJob(ctx context.Context, j *HostInstallJob) error {
	_, err := d.ExecContext(ctx, `INSERT INTO host_install_jobs(id,kind,target,state,runtime_id,language,resolved_version)
VALUES($1,$2,$3,'queued',NULLIF($4,0),$5,$6)`, j.ID, j.Kind, j.Target, j.RuntimeID, j.Language, j.ResolvedVersion)
	return err
}

func (d *DB) UpdateHostInstallJob(ctx context.Context, j *HostInstallJob) error {
	_, err := d.ExecContext(ctx, `UPDATE host_install_jobs SET state=$2,exit_code=$3,error=$4,log=$5,started_at=$6,ended_at=$7,
runtime_id=NULLIF($8,0),language=$9,resolved_version=$10 WHERE id=$1`,
		j.ID, j.State, j.ExitCode, j.Error, j.Log, j.StartedAt, j.EndedAt, j.RuntimeID, j.Language, j.ResolvedVersion)
	return err
}

func (d *DB) HostInstallJob(ctx context.Context, id string) (*HostInstallJob, error) {
	j := &HostInstallJob{}
	err := d.QueryRowContext(ctx, `SELECT id,kind,target,state,exit_code,error,log,COALESCE(runtime_id,0),language,resolved_version,created_at,started_at,ended_at FROM host_install_jobs WHERE id=$1`, id).Scan(&j.ID, &j.Kind, &j.Target, &j.State, &j.ExitCode, &j.Error, &j.Log, &j.RuntimeID, &j.Language, &j.ResolvedVersion, &j.CreatedAt, &j.StartedAt, &j.EndedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

func (d *DB) ListHostInstallJobs(ctx context.Context) ([]*HostInstallJob, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,kind,target,state,exit_code,error,log,COALESCE(runtime_id,0),language,resolved_version,created_at,started_at,ended_at FROM host_install_jobs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*HostInstallJob{}
	for rows.Next() {
		j := &HostInstallJob{}
		if err := rows.Scan(&j.ID, &j.Kind, &j.Target, &j.State, &j.ExitCode, &j.Error, &j.Log, &j.RuntimeID, &j.Language, &j.ResolvedVersion, &j.CreatedAt, &j.StartedAt, &j.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (d *DB) RecoverHostInstallJobs(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `UPDATE host_install_jobs SET state='interrupted',error='服务重启，任务中断',ended_at=COALESCE(ended_at,now()) WHERE state IN ('queued','running')`)
	return err
}
