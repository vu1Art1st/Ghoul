package db

import (
	"context"
	"strconv"
	"strings"
	"time"
)

type TaskRelationSuggestion struct {
	TaskID     int64     `json:"task_id"`
	Name       string    `json:"name,omitempty"`
	Status     string    `json:"status"`
	Goal       string    `json:"goal,omitempty"`
	Reason     string    `json:"reason"`
	Confidence float64   `json:"confidence"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SuggestTaskRelations is a deliberately bounded read model. It never expands
// task scope or copies context; the caller must explicitly select source IDs.
func (d *DB) SuggestTaskRelations(ctx context.Context, excludeTaskID int64, terms []string, limit int) ([]TaskRelationSuggestion, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	clean := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if len(term) < 3 || seen[term] {
			continue
		}
		seen[term] = true
		clean = append(clean, term)
		if len(clean) >= 6 {
			break
		}
	}
	if len(clean) == 0 {
		return []TaskRelationSuggestion{}, nil
	}
	conditions := make([]string, 0, len(clean))
	args := []any{excludeTaskID}
	for _, term := range clean {
		idx := len(args) + 1
		conditions = append(conditions, `(t.name ILIKE '%'||$`+strconv.Itoa(idx)+`||'%' OR t.description ILIKE '%'||$`+strconv.Itoa(idx)+`||'%' OR t.goal ILIKE '%'||$`+strconv.Itoa(idx)+`||'%')`)
		args = append(args, term)
	}
	args = append(args, limit)
	rows, err := d.QueryContext(ctx, `
SELECT t.id,COALESCE(t.name,''),t.status,COALESCE(t.description,''),COALESCE(t.goal,''),t.updated_at
FROM tasks t
WHERE t.deleted_at IS NULL AND t.id<>$1 AND (`+strings.Join(conditions, " OR ")+`)
ORDER BY t.updated_at DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskRelationSuggestion{}
	for rows.Next() {
		var s TaskRelationSuggestion
		var description string
		if err := rows.Scan(&s.TaskID, &s.Name, &s.Status, &description, &s.Goal, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Reason, s.Confidence = taskRelationMatch(s.Name, description, s.Goal, clean)
		out = append(out, s)
	}
	return out, rows.Err()
}

func taskRelationMatch(name, description, goal string, terms []string) (string, float64) {
	haystack := strings.ToLower(name + "\n" + description + "\n" + goal)
	matched := make([]string, 0, 2)
	for _, term := range terms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			matched = append(matched, term)
			if len(matched) == 2 {
				break
			}
		}
	}
	confidence := 0.55 + float64(len(matched))*0.12
	if confidence > 0.91 {
		confidence = 0.91
	}
	if len(matched) > 0 {
		return "命中关键词：" + strings.Join(matched, "、"), confidence
	}
	return "名称、描述或目标存在相近关键词", confidence
}
