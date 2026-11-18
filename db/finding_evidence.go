package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// FindingEvidence is a non-traffic evidence artifact attached to a finding.
// Traffic snapshots stay in finding_traffic_bindings; both are projected by the
// server into one evidence timeline.
type FindingEvidence struct {
	ID           int64           `json:"id,string"`
	FindingID    int64           `json:"finding_id,string"`
	Kind         string          `json:"kind"`
	Title        string          `json:"title"`
	Content      string          `json:"content,omitempty"`
	StorageRef   string          `json:"storage_ref,omitempty"`
	SHA256       string          `json:"sha256,omitempty"`
	Size         int64           `json:"size"`
	Redacted     bool            `json:"redacted"`
	MetadataJSON json.RawMessage `json:"metadata"`
	CreatedAt    time.Time       `json:"created_at"`
}

// FindingScreenshotInput describes one real browser screenshot captured in an
// intent workspace and attached to an existing Finding. The evidence store owns
// reading, validation and encoding; callers only identify the file.
type FindingScreenshotInput struct {
	TaskID    int64
	FindingID int64
	RunDir    string
	Path      string
	Title     string
	SourceURL string
	Note      string
}

const (
	FindingCaptureNativeAction     = "native_action"
	FindingCaptureNavigationReplay = "navigation_replay"
	FindingCaptureRawHTTPExchange  = "raw_http_exchange"
)

// FindingScreenshotCaptureInput asks the host to drive the real browser itself:
// open the original page, intercept the application's request, rewrite only the
// scoped parameter/header, let the page's own JavaScript render the response, and
// capture the resulting DOM. This avoids agent-authored full-page replacements.
type FindingScreenshotCaptureInput struct {
	TaskID               int64
	FindingID            int64
	RunDir               string
	Mode                 string
	PageURL              string
	PreconditionSelector string
	ActionSelector       string
	WaitSelector         string
	WaitText             string
	PrepareJS            string
	RouteMatch           string
	HTTPMethod           string
	RequestBody          string
	ContentType          string
	Query                map[string]string
	Headers              map[string]string
	TrafficID            string
	Title                string
	Note                 string
	SourceURL            string
	FullPage             bool
}

const findingEvidenceCols = `id,finding_id,kind,title,content,storage_ref,sha256,size,redacted,metadata_json,created_at`

func scanFindingEvidence(row interface{ Scan(...any) error }) (*FindingEvidence, error) {
	var item FindingEvidence
	err := row.Scan(&item.ID, &item.FindingID, &item.Kind, &item.Title, &item.Content,
		&item.StorageRef, &item.SHA256, &item.Size, &item.Redacted, &item.MetadataJSON, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &item, err
}

// ListFindingEvidence returns all generic evidence for a finding, newest first.
func (d *DB) ListFindingEvidence(ctx context.Context, findingID int64) ([]FindingEvidence, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+findingEvidenceCols+`
		FROM finding_evidence WHERE finding_id=$1 ORDER BY id DESC`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingEvidence{}
	for rows.Next() {
		item, err := scanFindingEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

// GetFindingEvidence returns one evidence row only when it belongs to findingID.
func (d *DB) GetFindingEvidence(ctx context.Context, findingID, evidenceID int64) (*FindingEvidence, error) {
	if findingID <= 0 || evidenceID <= 0 {
		return nil, ErrEvidenceNotFound
	}
	item, err := scanFindingEvidence(d.QueryRowContext(ctx, `SELECT `+findingEvidenceCols+`
		FROM finding_evidence WHERE id=$1 AND finding_id=$2`, evidenceID, findingID))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && item == nil) {
		return nil, ErrEvidenceNotFound
	}
	return item, err
}

// AddFindingEvidence performs optimistic version checking and evidence append
// atomically. expectedVersion nil means the caller intentionally skips CAS.
func (d *DB) AddFindingEvidence(ctx context.Context, findingID int64, expectedVersion *int64, actor, reason string, item FindingEvidence) (*FindingEvidence, int64, error) {
	if findingID <= 0 || strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return nil, 0, errors.New("finding_id、actor 与 reason 必填")
	}
	item.Kind = strings.TrimSpace(item.Kind)
	if item.Kind == "" {
		item.Kind = "manual"
	}
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		item.Title = item.Kind
	}
	if strings.TrimSpace(item.Content) == "" && strings.TrimSpace(item.StorageRef) == "" {
		return nil, 0, errors.New("证据 content 或 storage_ref 至少提供一项")
	}
	if strings.TrimSpace(item.Content) != "" {
		item.Size = int64(len(item.Content))
		sum := sha256.Sum256([]byte(item.Content))
		item.SHA256 = hex.EncodeToString(sum[:])
	} else if item.Size < 0 {
		return nil, 0, errors.New("证据 size 不能为负")
	}
	if len(item.MetadataJSON) == 0 {
		item.MetadataJSON = json.RawMessage(`{}`)
	}
	var nextVersion int64
	err := d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := LockFindingEvidenceTx(tx, findingID, expectedVersion); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO finding_evidence
			(finding_id,kind,title,content,storage_ref,sha256,size,redacted,metadata_json)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			RETURNING `+findingEvidenceCols,
			findingID, item.Kind, item.Title, item.Content, item.StorageRef, item.SHA256,
			item.Size, item.Redacted, string(item.MetadataJSON)).Scan(
			&item.ID, &item.FindingID, &item.Kind, &item.Title, &item.Content,
			&item.StorageRef, &item.SHA256, &item.Size, &item.Redacted,
			&item.MetadataJSON, &item.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE findings
			SET evidence_version=evidence_version+1,
			    evidence_audit=evidence_audit || jsonb_build_array(jsonb_build_object(
			      'at',now(),'event','evidence_added','actor',$2::text,'reason',$3::text,
			      'kind',$4::text,'sha256',$5::text))
			WHERE id=$1`, findingID, actor, reason, item.Kind, item.SHA256); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT evidence_version FROM findings WHERE id=$1`, findingID).Scan(&nextVersion)
	})
	return &item, nextVersion, err
}

// DeleteFindingEvidence removes one generic evidence row with CAS protection.
func (d *DB) DeleteFindingEvidence(ctx context.Context, findingID, evidenceID int64, expectedVersion int64, actor, reason string) (int64, error) {
	if findingID <= 0 || evidenceID <= 0 || strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return 0, errors.New("finding_id、evidence_id、actor 与 reason 必填")
	}
	var nextVersion int64
	err := d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := LockFindingEvidenceTx(tx, findingID, &expectedVersion); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM finding_evidence WHERE id=$1 AND finding_id=$2`, evidenceID, findingID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrEvidenceNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE findings
			SET evidence_version=evidence_version+1,
			    evidence_audit=evidence_audit || jsonb_build_array(jsonb_build_object(
			      'at',now(),'event','evidence_deleted','actor',$2::text,'reason',$3::text,
			      'evidence_id',$4::bigint))
			WHERE id=$1`, findingID, actor, reason, evidenceID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT evidence_version FROM findings WHERE id=$1`, findingID).Scan(&nextVersion)
	})
	return nextVersion, err
}
