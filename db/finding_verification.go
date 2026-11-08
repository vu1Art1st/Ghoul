package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type FindingVerification struct {
	Verdict         string    `json:"verdict"`
	Summary         string    `json:"summary"`
	Evidence        string    `json:"evidence"`
	CounterEvidence []string  `json:"counter_evidence"`
	PivotHints      []string  `json:"pivot_hints"`
	Actor           string    `json:"actor"`
	VerifiedAt      time.Time `json:"verified_at"`
	EvidenceVersion int64     `json:"evidence_version"`
}

// SetFindingVerification stores a deterministic verifier result. The evidence
// version check prevents a stale verifier from overwriting a newer finding.
func (d *DB) SetFindingVerification(ctx context.Context, findingID, expectedVersion int64, v FindingVerification) (int64, error) {
	v.Verdict = strings.ToLower(strings.TrimSpace(v.Verdict))
	if v.Verdict != "verified" && v.Verdict != "false_positive" && v.Verdict != "inconclusive" {
		return 0, errors.New("verdict 必须为 verified / false_positive / inconclusive")
	}
	v.Summary = strings.TrimSpace(v.Summary)
	v.Evidence = strings.TrimSpace(v.Evidence)
	v.Actor = strings.TrimSpace(v.Actor)
	if v.Summary == "" || v.Evidence == "" || v.Actor == "" {
		return 0, errors.New("summary、evidence 与 actor 必填")
	}
	if len(v.CounterEvidence) == 0 {
		return 0, errors.New("必须至少记录一条实际排查的反证项")
	}
	for i := range v.CounterEvidence {
		v.CounterEvidence[i] = strings.TrimSpace(v.CounterEvidence[i])
	}
	v.VerifiedAt = time.Now().UTC()
	v.EvidenceVersion = expectedVersion
	raw, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	counter, err := json.Marshal(v.CounterEvidence)
	if err != nil {
		return 0, err
	}
	pivot, err := json.Marshal(v.PivotHints)
	if err != nil {
		return 0, err
	}
	var next int64
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := LockFindingEvidenceTx(tx, findingID, &expectedVersion); err != nil {
			return err
		}
		status := ""
		switch v.Verdict {
		case "verified":
			status = FindingConfirmed
		case "false_positive":
			status = FindingFalsePositive
		}
		if _, err := tx.ExecContext(ctx, `UPDATE findings
			SET verification=$2,
			    counter_evidence=$3,
			    pivot_hints=$4,
			    evidence_version=evidence_version+1,
			    evidence_audit=evidence_audit || jsonb_build_array(jsonb_build_object(
			      'at',now(),'event','verification','actor',$5::text,'verdict',$6::text)),
			    status=CASE WHEN $7::text='' THEN status ELSE $7::text END
			WHERE id=$1`, findingID, string(raw), string(counter), string(pivot), v.Actor, v.Verdict, status); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT evidence_version FROM findings WHERE id=$1`, findingID).Scan(&next)
	})
	return next, err
}
