package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PocRecord struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	ScopeType        string          `json:"scope_type"`
	TargetKey        string          `json:"target_key,omitempty"`
	Vendor           string          `json:"vendor,omitempty"`
	Product          string          `json:"product,omitempty"`
	Component        string          `json:"component,omitempty"`
	AffectedVersions string          `json:"affected_versions,omitempty"`
	Fingerprint      json.RawMessage `json:"fingerprint,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CVEIDs           []string        `json:"cve_ids,omitempty"`
	CWEIDs           []string        `json:"cwe_ids,omitempty"`
	ExploitMethod    string          `json:"exploit_method,omitempty"`
	Prerequisites    string          `json:"prerequisites,omitempty"`
	Impact           string          `json:"impact,omitempty"`
	ValidationStatus string          `json:"validation_status"`
	Confidence       string          `json:"confidence"`
	SourceTaskID     *int64          `json:"source_task_id,omitempty"`
	SourceIntentID   *int64          `json:"source_intent_id,omitempty"`
	FindingID        *int64          `json:"finding_id,omitempty"`
	AssetIDs         []int64         `json:"asset_ids,omitempty"`
	ArtifactAssetID  *int64          `json:"artifact_asset_id,omitempty"`
	ArtifactVersion  int             `json:"artifact_version"`
	LastVerifiedAt   *time.Time      `json:"last_verified_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

const pocRecordCols = `id,name,scope_type,target_key,vendor,product,component,affected_versions,
COALESCE(fingerprint,'{}'::jsonb),COALESCE(metadata,'{}'::jsonb),
array_to_json(cve_ids)::text,array_to_json(cwe_ids)::text,
exploit_method,prerequisites,impact,validation_status,confidence,
source_task_id,source_intent_id,finding_id,array_to_json(asset_ids)::text,
artifact_asset_id,artifact_version,last_verified_at,created_at,updated_at`

func scanPocRecord(sc interface{ Scan(...any) error }) (*PocRecord, error) {
	var r PocRecord
	var fingerprint, metadata, cves, cwes, assets string
	err := sc.Scan(
		&r.ID, &r.Name, &r.ScopeType, &r.TargetKey, &r.Vendor, &r.Product, &r.Component,
		&r.AffectedVersions, &fingerprint, &metadata, &cves, &cwes, &r.ExploitMethod, &r.Prerequisites,
		&r.Impact, &r.ValidationStatus, &r.Confidence, &r.SourceTaskID, &r.SourceIntentID,
		&r.FindingID, &assets, &r.ArtifactAssetID, &r.ArtifactVersion, &r.LastVerifiedAt,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	r.Fingerprint = json.RawMessage(fingerprint)
	r.Metadata = json.RawMessage(metadata)
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	_ = json.Unmarshal([]byte(cves), &r.CVEIDs)
	_ = json.Unmarshal([]byte(cwes), &r.CWEIDs)
	_ = json.Unmarshal([]byte(assets), &r.AssetIDs)
	return &r, nil
}

func (d *DB) CreatePocRecord(ctx context.Context, r *PocRecord) (*PocRecord, error) {
	if strings.TrimSpace(r.Name) == "" {
		return nil, fmt.Errorf("PoC 名称不能为空")
	}
	if err := normalizePocRecord(r); err != nil {
		return nil, err
	}
	cves, _ := json.Marshal(r.CVEIDs)
	cwes, _ := json.Marshal(r.CWEIDs)
	assets, _ := json.Marshal(r.AssetIDs)
	row := d.QueryRowContext(ctx, `
INSERT INTO poc_records(name,scope_type,target_key,vendor,product,component,affected_versions,
 fingerprint,metadata,cve_ids,cwe_ids,exploit_method,prerequisites,impact,validation_status,confidence,
 source_task_id,source_intent_id,finding_id,asset_ids,artifact_asset_id,artifact_version)
VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,
 ARRAY(SELECT jsonb_array_elements_text($10::jsonb)),
 ARRAY(SELECT jsonb_array_elements_text($11::jsonb)),
 $12,$13,$14,$15,$16,$17,$18,$19,
 ARRAY(SELECT value::bigint FROM jsonb_array_elements_text($20::jsonb)),
 $21,$22)
RETURNING `+pocRecordCols,
		r.Name, r.ScopeType, r.TargetKey, r.Vendor, r.Product, r.Component, r.AffectedVersions,
		string(r.Fingerprint), string(r.Metadata), string(cves), string(cwes), r.ExploitMethod, r.Prerequisites,
		r.Impact, r.ValidationStatus, r.Confidence, r.SourceTaskID, r.SourceIntentID, r.FindingID,
		string(assets), r.ArtifactAssetID, r.ArtifactVersion,
	)
	return scanPocRecord(row)
}

func normalizePocRecord(r *PocRecord) error {
	switch r.ScopeType {
	case "":
		r.ScopeType = "component"
	case "target", "product", "component", "ctf":
	default:
		return fmt.Errorf("PoC 范围需为 target/product/component/ctf")
	}
	switch r.ValidationStatus {
	case "":
		r.ValidationStatus = "draft"
	case "draft", "reproduced", "verified", "stale", "retired":
	default:
		return fmt.Errorf("PoC 验证状态非法")
	}
	if strings.TrimSpace(r.Confidence) == "" {
		r.Confidence = "observed"
	}
	if r.Fingerprint == nil {
		r.Fingerprint = json.RawMessage(`{}`)
	}
	if r.Metadata == nil {
		r.Metadata = json.RawMessage(`{}`)
	}
	if r.CVEIDs == nil {
		r.CVEIDs = []string{}
	}
	if r.CWEIDs == nil {
		r.CWEIDs = []string{}
	}
	if r.AssetIDs == nil {
		r.AssetIDs = []int64{}
	}
	if !json.Valid(r.Fingerprint) || !json.Valid(r.Metadata) {
		return fmt.Errorf("PoC fingerprint/metadata 必须是有效 JSON")
	}
	return nil
}

func (d *DB) GetPocRecord(ctx context.Context, id int64) (*PocRecord, error) {
	r, err := scanPocRecord(d.QueryRowContext(ctx, `SELECT `+pocRecordCols+` FROM poc_records WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

func (d *DB) SearchPocRecords(ctx context.Context, q string, limit int) ([]*PocRecord, error) {
	return d.SearchPocRecordsScope(ctx, q, "", limit)
}

func (d *DB) SearchPocRecordsScope(ctx context.Context, q, scope string, limit int) ([]*PocRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	q = strings.TrimSpace(q)
	scope = strings.TrimSpace(scope)
	rows, err := d.QueryContext(ctx, `
SELECT `+pocRecordCols+` FROM poc_records
WHERE ($3='' OR scope_type=$3) AND ($1='' OR name ILIKE '%'||$1||'%' OR target_key ILIKE '%'||$1||'%'
 OR vendor ILIKE '%'||$1||'%' OR product ILIKE '%'||$1||'%'
 OR component ILIKE '%'||$1||'%' OR affected_versions ILIKE '%'||$1||'%'
 OR array_to_string(cve_ids,',') ILIKE '%'||$1||'%'
 OR array_to_string(cwe_ids,',') ILIKE '%'||$1||'%'
 OR metadata::text ILIKE '%'||$1||'%')
ORDER BY CASE validation_status WHEN 'verified' THEN 0 WHEN 'reproduced' THEN 1 ELSE 2 END,
 updated_at DESC LIMIT $2`, q, limit, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PocRecord{}
	for rows.Next() {
		r, scanErr := scanPocRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) VerifyPocRecord(ctx context.Context, id int64, status, confidence string) (*PocRecord, error) {
	if status == "" {
		status = "verified"
	}
	switch status {
	case "draft", "reproduced", "verified", "stale", "retired":
	default:
		return nil, fmt.Errorf("PoC 验证状态非法")
	}
	if confidence == "" {
		confidence = "observed"
	}
	row := d.QueryRowContext(ctx, `
UPDATE poc_records SET validation_status=$2,confidence=$3,last_verified_at=now(),updated_at=now()
WHERE id=$1 RETURNING `+pocRecordCols, id, status, confidence)
	r, err := scanPocRecord(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}
