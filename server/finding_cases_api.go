package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func (s *Server) listFindingCases(w http.ResponseWriter, r *http.Request) {
	taskID := int64(atoiDefault(r.URL.Query().Get("task"), 0))
	page := findingPaginationParam(r.URL.Query().Get("page"), 1, 0)
	size := findingPaginationParam(r.URL.Query().Get("limit"), 20, 100)
	items, total, err := s.m.pg.ListFindingCases(r.Context(), taskID, page, size)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) mergeFindingCase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID     int64   `json:"task_id"`
		FindingIDs []int64 `json:"finding_ids"`
		Title      string  `json:"title"`
		Reason     string  `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	caseID, err := s.m.pg.MergeFindingCase(r.Context(), req.TaskID, req.FindingIDs, req.Title, req.Reason, "human")
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	item, err := s.m.pg.GetFindingCase(r.Context(), caseID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, item)
}

type findingCaseSuggestion struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// suggestFindingCase asks the same task-scoped LLM that powers exploration to
// propose a unified title and evidence-based merge reason. It never persists a
// case; the human still reviews and submits the suggestion.
func (s *Server) suggestFindingCase(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var req struct {
		FindingIDs []int64 `json:"finding_ids"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ids, err := uniqueFindingIDs(req.FindingIDs)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	findings := make([]*db.DBFinding, 0, len(ids))
	var taskID int64
	for _, id := range ids {
		finding, getErr := s.m.pg.GetFinding(id)
		if getErr != nil {
			writeErr(w, 500, getErr.Error())
			return
		}
		if finding == nil {
			writeErr(w, 404, fmt.Sprintf("漏洞 #%d 不存在", id))
			return
		}
		if finding.TaskID == nil || *finding.TaskID <= 0 {
			writeErr(w, 409, "存在未关联任务的漏洞，无法生成统一名称")
			return
		}
		if taskID == 0 {
			taskID = *finding.TaskID
		} else if *finding.TaskID != taskID {
			writeErr(w, 409, "只能为同一任务的漏洞生成统一名称")
			return
		}
		findings = append(findings, finding)
	}
	task := s.m.ResolveTask(strconv.FormatInt(taskID, 10))
	if task == nil {
		writeErr(w, 409, "漏洞所属任务不存在或已归档")
		return
	}

	prompt := findingCaseSuggestionPrompt(findings)
	runtime := &taskLLMRuntime{s: s, taskID: task.ID, agentKey: "goals"}
	if s.engine != nil {
		s.engine.BeginLLMCall(task.ID)
		defer s.engine.EndLLMCall(task.ID)
	}
	maxTokens := runtime.maxTokens()
	if maxTokens <= 0 || maxTokens > 1200 {
		maxTokens = 1200
	}
	temperature := 0.1
	message, _, _, err := runtime.Complete(r.Context(), llm.CompletionRequest{
		System: []string{
			"你是渗透测试漏洞报告归并助手。根据用户给出的同一任务漏洞证据，提炼一个准确、具体、可交付的中文统一名称，并说明为何应归并为同一根因。",
			"只输出 JSON，不要 Markdown。格式必须是 {\"title\":\"...\",\"reason\":\"...\"}。title 不超过 40 个汉字，reason 用 1-3 句话，引用可核验的共同证据，不得编造。",
		},
		Messages:    []llm.Message{llm.UserText(prompt)},
		MaxTokens:   maxTokens,
		Temperature: &temperature,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AI 名称生成失败: "+err.Error())
		return
	}
	suggestion, err := parseFindingCaseSuggestion(message.Text())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AI 未返回有效建议: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"title":   suggestion.Title,
		"reason":  suggestion.Reason,
		"task_id": i64s(taskID),
	})
}

func uniqueFindingIDs(input []int64) ([]int64, error) {
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(input))
	for _, id := range input {
		if id <= 0 {
			return nil, fmt.Errorf("无效漏洞编号: %d", id)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 {
		return nil, fmt.Errorf("至少需要两条漏洞记录")
	}
	if len(ids) > 50 {
		return nil, fmt.Errorf("一次最多处理 50 条漏洞")
	}
	return ids, nil
}

func findingCaseSuggestionPrompt(findings []*db.DBFinding) string {
	var b strings.Builder
	b.WriteString("待归并漏洞：\n")
	for _, finding := range findings {
		fmt.Fprintf(&b, "\n- Finding #%d\n", finding.ID)
		fmt.Fprintf(&b, "  名称: %s\n", firstNonEmpty(finding.Name, finding.VulnClass, "未命名"))
		fmt.Fprintf(&b, "  类型: %s\n", finding.VulnClass)
		fmt.Fprintf(&b, "  等级: %s\n", finding.Severity)
		fmt.Fprintf(&b, "  摘要: %s\n", clipRunes(finding.Summary, 1200))
		fmt.Fprintf(&b, "  证据: %s\n", clipRunes(finding.Evidence, 1800))
	}
	return b.String()
}

func clipRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

func parseFindingCaseSuggestion(raw string) (findingCaseSuggestion, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return findingCaseSuggestion{}, fmt.Errorf("响应中没有 JSON 对象")
	}
	var suggestion findingCaseSuggestion
	if err := json.Unmarshal([]byte(raw[start:end+1]), &suggestion); err != nil {
		return findingCaseSuggestion{}, err
	}
	suggestion.Title = strings.TrimSpace(suggestion.Title)
	suggestion.Reason = strings.TrimSpace(suggestion.Reason)
	if suggestion.Title == "" || suggestion.Reason == "" {
		return findingCaseSuggestion{}, fmt.Errorf("title 或 reason 为空")
	}
	suggestion.Title = clipRunes(suggestion.Title, 80)
	suggestion.Reason = clipRunes(suggestion.Reason, 1200)
	return suggestion, nil
}

func (s *Server) getFindingCase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad case id")
		return
	}
	item, err := s.m.pg.GetFindingCase(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if item == nil {
		writeErr(w, 404, "finding case not found")
		return
	}
	members, err := s.m.pg.FindingCaseMembers(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	events, err := s.m.pg.FindingCaseEvents(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"case": item, "members": members, "events": events})
}

func (s *Server) updateFindingCase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad case id")
		return
	}
	var req struct {
		Version  int64  `json:"version"`
		Title    string `json:"title"`
		Report   string `json:"report"`
		Severity string `json:"severity"`
		Reason   string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.m.pg.UpdateFindingCaseReport(r.Context(), id, req.Version, req.Title, req.Report, req.Severity, req.Reason); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	item, _ := s.m.pg.GetFindingCase(r.Context(), id)
	writeJSON(w, 200, item)
}

func (s *Server) detachFindingCaseMember(w http.ResponseWriter, r *http.Request) {
	caseID, ok := pathInt(r, "id")
	findingID, okFinding := pathInt(r, "findingId")
	if !ok || !okFinding || caseID <= 0 || findingID <= 0 {
		writeErr(w, 400, "bad case or finding id")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.m.pg.DetachFindingCaseMember(r.Context(), caseID, findingID, "human", req.Reason); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"detached": true})
}

func (s *Server) deleteFindingCase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad case id")
		return
	}
	if err := s.m.pg.DeleteFindingCase(r.Context(), id); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}
