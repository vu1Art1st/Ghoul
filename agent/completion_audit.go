package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// validateGoalEvidence is the code-owned completion audit. It rejects evidence
// that is explicitly inferred, empty, or carries an incomplete checkpoint.
// Semantic sufficiency is still the planner's responsibility.
func validateGoalEvidence(goal, evidence *db.Node) error {
	if goal == nil || evidence == nil {
		return fmt.Errorf("缺少目标或证据节点")
	}
	var goalPayload struct {
		GoalType string `json:"goal_type"`
		Pending  bool   `json:"goal_contract_pending"`
	}
	if len(goal.Payload) > 0 {
		if err := json.Unmarshal(goal.Payload, &goalPayload); err != nil {
			return fmt.Errorf("目标 payload 无法解析: %w", err)
		}
	}
	if goalPayload.Pending {
		return fmt.Errorf("目标契约尚未确定，不能提前标记达成")
	}
	var payload struct {
		Confidence string `json:"confidence"`
		Evidence   string `json:"evidence"`
		Summary    string `json:"summary"`
		Detail     string `json:"detail"`
		Checkpoint *struct {
			Result string `json:"result"`
		} `json:"checkpoint"`
	}
	if len(evidence.Payload) > 0 {
		if err := json.Unmarshal(evidence.Payload, &payload); err != nil {
			return fmt.Errorf("证据 payload 无法解析: %w", err)
		}
	}
	if strings.EqualFold(strings.TrimSpace(payload.Confidence), "inferred") {
		return fmt.Errorf("推断事实不能独自证明目标达成")
	}
	if payload.Checkpoint != nil {
		switch strings.ToLower(strings.TrimSpace(payload.Checkpoint.Result)) {
		case "partial", "blocked":
			return fmt.Errorf("证据检查点仍为 %s，不能证明目标完成", payload.Checkpoint.Result)
		}
	}
	hasContent := strings.TrimSpace(payload.Evidence) != "" ||
		strings.TrimSpace(payload.Summary) != "" ||
		strings.TrimSpace(payload.Detail) != ""
	if !hasContent {
		return fmt.Errorf("证据缺少可核查内容，不能证明目标达成")
	}
	return nil
}
