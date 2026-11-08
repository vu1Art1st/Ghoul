package agent

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func auditNode(kind string, payload map[string]any) *db.Node {
	raw, _ := json.Marshal(payload)
	return &db.Node{Kind: kind, Payload: raw}
}

func TestValidateGoalEvidence(t *testing.T) {
	goal := auditNode(db.KindGoal, map[string]any{"goal_type": "outcome"})
	tests := []struct {
		name     string
		goal     *db.Node
		evidence *db.Node
		wantErr  bool
	}{
		{"observed evidence", goal, auditNode(db.KindFact, map[string]any{"confidence": "observed", "evidence": "whoami returned SYSTEM"}), false},
		{"inferred evidence", goal, auditNode(db.KindFact, map[string]any{"confidence": "inferred", "summary": "version looks vulnerable"}), true},
		{"empty evidence", goal, auditNode(db.KindFact, map[string]any{"confidence": "observed"}), true},
		{"partial checkpoint", goal, auditNode(db.KindFact, map[string]any{"summary": "partial", "checkpoint": map[string]any{"result": "partial"}}), true},
		{"pending contract", auditNode(db.KindGoal, map[string]any{"goal_contract_pending": true}), auditNode(db.KindFact, map[string]any{"summary": "saved"}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGoalEvidence(tc.goal, tc.evidence)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
