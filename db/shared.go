package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// SharedAsset is the editable catalog record; published bytes are immutable on disk.
type SharedAsset struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	Description    string    `json:"description"`
	Tags           []string  `json:"tags"`
	Language       string    `json:"language"`
	Entrypoint     string    `json:"entrypoint"`
	Usage          string    `json:"usage"`
	Dependencies   []string  `json:"dependencies"`
	CurrentVersion int       `json:"current_version"`
	Status         string    `json:"status"`
	SourceTaskID   int64     `json:"source_task_id"`
	SourceIntentID int64     `json:"source_intent_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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

const sharedAssetCols = `id,name,kind,description,tags,language,entrypoint,usage,dependencies,current_version,status,COALESCE(source_task_id,0),COALESCE(source_intent_id,0),created_at,updated_at`

func scanSharedAsset(scan interface{ Scan(...any) error }) (*SharedAsset, error) {
	var a SharedAsset
	var tags, deps []byte
	if err := scan.Scan(&a.ID, &a.Name, &a.Kind, &a.Description, &tags, &a.Language, &a.Entrypoint, &a.Usage, &deps, &a.CurrentVersion, &a.Status, &a.SourceTaskID, &a.SourceIntentID, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(tags, &a.Tags)
	_ = json.Unmarshal(deps, &a.Dependencies)
	if a.Tags == nil {
		a.Tags = []string{}
	}
	if a.Dependencies == nil {
		a.Dependencies = []string{}
	}
	return &a, nil
}

func (d *DB) CreateSharedAsset(ctx context.Context, a *SharedAsset) error {
	tags, _ := json.Marshal(a.Tags)
	deps, _ := json.Marshal(a.Dependencies)
	return d.QueryRowContext(ctx, `INSERT INTO shared_assets(name,kind,description,tags,language,entrypoint,usage,dependencies,status,source_task_id,source_intent_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending_review',NULLIF($9,0),NULLIF($10,0)) RETURNING id,created_at,updated_at`, a.Name, a.Kind, a.Description, tags, a.Language, a.Entrypoint, a.Usage, deps, a.SourceTaskID, a.SourceIntentID).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
}

func (d *DB) ListSharedAssets(ctx context.Context, query string, publishedOnly bool) ([]*SharedAsset, error) {
	where := "status <> 'archived'"
	if publishedOnly {
		where = "status = 'published'"
	}
	rows, err := d.QueryContext(ctx, `SELECT `+sharedAssetCols+` FROM shared_assets WHERE `+where+` AND ($1='' OR lower(name||' '||description||' '||tags::text) LIKE '%'||lower($1)||'%') ORDER BY updated_at DESC LIMIT 200`, query)
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

func (d *DB) SetSharedAssetStatus(ctx context.Context, id int64, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE shared_assets SET status=$2 WHERE id=$1`, id, status)
	return err
}

func (d *DB) UpdateSharedAssetMeta(ctx context.Context, a *SharedAsset) error {
	tags, _ := json.Marshal(a.Tags)
	deps, _ := json.Marshal(a.Dependencies)
	_, err := d.ExecContext(ctx, `UPDATE shared_assets SET description=$2,tags=$3,language=$4,entrypoint=$5,usage=$6,dependencies=$7 WHERE id=$1`,
		a.ID, a.Description, tags, a.Language, a.Entrypoint, a.Usage, deps)
	return err
}

type HostInstallJob struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Target    string     `json:"target"`
	State     string     `json:"state"`
	ExitCode  int        `json:"exit_code"`
	Error     string     `json:"error,omitempty"`
	Log       string     `json:"log"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

func (d *DB) CreateHostInstallJob(ctx context.Context, j *HostInstallJob) error {
	_, err := d.ExecContext(ctx, `INSERT INTO host_install_jobs(id,kind,target,state) VALUES($1,$2,$3,'queued')`, j.ID, j.Kind, j.Target)
	return err
}

func (d *DB) UpdateHostInstallJob(ctx context.Context, j *HostInstallJob) error {
	_, err := d.ExecContext(ctx, `UPDATE host_install_jobs SET state=$2,exit_code=$3,error=$4,log=$5,started_at=$6,ended_at=$7 WHERE id=$1`, j.ID, j.State, j.ExitCode, j.Error, j.Log, j.StartedAt, j.EndedAt)
	return err
}

func (d *DB) HostInstallJob(ctx context.Context, id string) (*HostInstallJob, error) {
	j := &HostInstallJob{}
	err := d.QueryRowContext(ctx, `SELECT id,kind,target,state,exit_code,error,log,created_at,started_at,ended_at FROM host_install_jobs WHERE id=$1`, id).Scan(&j.ID, &j.Kind, &j.Target, &j.State, &j.ExitCode, &j.Error, &j.Log, &j.CreatedAt, &j.StartedAt, &j.EndedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

func (d *DB) ListHostInstallJobs(ctx context.Context) ([]*HostInstallJob, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,kind,target,state,exit_code,error,log,created_at,started_at,ended_at FROM host_install_jobs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*HostInstallJob{}
	for rows.Next() {
		j := &HostInstallJob{}
		if err := rows.Scan(&j.ID, &j.Kind, &j.Target, &j.State, &j.ExitCode, &j.Error, &j.Log, &j.CreatedAt, &j.StartedAt, &j.EndedAt); err != nil {
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
