package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

const findingMergeJobTimeout = 10 * time.Minute
const settingFindingAutoMergeEnabled = "finding_auto_merge_enabled"
const maxAutoMergeStartsPerTick = 2

type findingMergeAgentResult struct {
	Title          string `json:"title"`
	Reason         string `json:"reason"`
	Severity       string `json:"severity"`
	SeverityReason string `json:"severity_reason"`
	Report         string `json:"report"`
}

type findingMergeCluster struct {
	FindingIDs   []int64 `json:"finding_ids"`
	RootCauseKey string  `json:"root_cause_key"`
	Reason       string  `json:"reason"`
}

type findingMergeClusterPlan struct {
	Groups              []findingMergeCluster `json:"groups"`
	UngroupedFindingIDs []int64               `json:"ungrouped_finding_ids"`
}

func (s *Server) findingAutoMergeEnabled() bool {
	return s.m != nil && s.m.pg != nil && s.m.pg.GetBool(settingFindingAutoMergeEnabled, false)
}

// reconcileAutoFindingMerges is called by the scheduler. It is deliberately
// durable and idempotent: task completion only has to produce a stable input
// fingerprint, not a one-shot callback that could be lost on restart.
func (s *Server) reconcileAutoFindingMerges(ctx context.Context) {
	if !s.findingAutoMergeEnabled() {
		return
	}
	taskIDs, err := s.m.pg.ListDoneTaskIDsForAutoMerge(ctx, 50)
	if err != nil {
		return
	}
	started := 0
	for _, taskID := range taskIDs {
		if started >= maxAutoMergeStartsPerTick {
			break
		}
		ids, inputHash, ok, err := s.findingAutoMergeInput(ctx, taskID)
		if err != nil || !ok {
			continue
		}
		jobID, created, err := s.m.pg.CreateAutoFindingMergeJob(ctx, ids, inputHash)
		if err != nil {
			continue
		}
		if created {
			go s.runFindingMergeJob(jobID)
			started++
			continue
		}
		job, getErr := s.m.pg.GetFindingMergeJob(ctx, jobID)
		if getErr == nil && job != nil && job.Status == "failed" && job.Attempts < 3 &&
			time.Since(job.UpdatedAt) >= time.Minute {
			go s.runFindingMergeJob(jobID)
			started++
		}
	}
}

func (s *Server) findingAutoMergeInput(ctx context.Context, taskID int64) ([]int64, string, bool, error) {
	inputs, err := s.m.pg.ListFindingAutoMergeInputs(ctx, taskID)
	if err != nil {
		return nil, "", false, err
	}
	eligible := make([]db.FindingAutoMergeInput, 0, len(inputs))
	for _, item := range inputs {
		if item.CaseLocked || item.CaseManagedBy == "manual" {
			continue
		}
		eligible = append(eligible, item)
	}
	if len(eligible) < 2 {
		return nil, "", false, nil
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].FindingID < eligible[j].FindingID })
	pairs, err := s.m.pg.FindingCaseBlockPairsForTask(ctx, taskID)
	if err != nil {
		return nil, "", false, err
	}
	prompt, _ := s.findingMergerPrompt()
	h := sha256.New()
	fmt.Fprintf(h, "task=%d\nprompt=%s\nreport_prompt=%s\n", taskID, prompt, agent.FindingMergerReportPrompt)
	ids := make([]int64, 0, len(eligible))
	for _, item := range eligible {
		ids = append(ids, item.FindingID)
		fmt.Fprintf(h, "finding=%d evidence=%d report=%d\n",
			item.FindingID, item.EvidenceVersion, item.ReportEvidenceVersion)
	}
	for _, pair := range pairs {
		fmt.Fprintf(h, "blocked=%d-%d\n", pair.LeftID, pair.RightID)
	}
	return ids, hex.EncodeToString(h.Sum(nil)), true, nil
}

func (s *Server) createFindingMergeJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		FindingIDs []int64 `json:"finding_ids"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jobID, err := s.m.pg.CreateFindingMergeJob(r.Context(), req.FindingIDs, "cluster")
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	go s.runFindingMergeJob(jobID)
	job, _ := s.m.pg.GetFindingMergeJob(r.Context(), jobID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) regenerateFindingCase(w http.ResponseWriter, r *http.Request) {
	caseID, ok := pathInt(r, "id")
	if !ok || caseID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad case id")
		return
	}
	item, err := s.m.pg.GetFindingCase(r.Context(), caseID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil || !item.Active {
		writeErr(w, http.StatusNotFound, "active finding case not found")
		return
	}
	if err := s.m.pg.LockFindingCaseForManual(r.Context(), caseID, "human", "人工请求重新生成统一报告"); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	members, err := s.m.pg.FindingCaseMembers(r.Context(), caseID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.ID)
	}
	jobID, err := s.m.pg.CreateFindingMergeJob(r.Context(), ids, "regenerate")
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	go s.runFindingMergeJob(jobID)
	job, _ := s.m.pg.GetFindingMergeJob(r.Context(), jobID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) getFindingMergeJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad job id")
		return
	}
	job, err := s.m.pg.GetFindingMergeJob(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job == nil {
		writeErr(w, http.StatusNotFound, "finding merge job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) runFindingMergeJob(jobID int64) {
	base := s.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, findingMergeJobTimeout)
	defer cancel()

	job, err := s.m.pg.GetFindingMergeJob(ctx, jobID)
	if err != nil || job == nil {
		return
	}
	status := "clustering"
	if job.Mode == "regenerate" {
		status = "reporting"
	}
	if err := s.m.pg.BeginFindingMergeJob(ctx, jobID, status); err != nil {
		return
	}
	caseIDs, ungrouped, err := s.executeFindingMergeAgent(ctx, job)
	if err != nil {
		_ = s.m.pg.FinishFindingMergeJob(context.Background(), jobID, nil, nil, "failed", clipRunes(err.Error(), 2000))
		return
	}
	_ = s.m.pg.FinishFindingMergeJob(context.Background(), jobID, caseIDs, ungrouped, "succeeded", "")
}

func (s *Server) executeFindingMergeAgent(ctx context.Context, job *db.FindingMergeJob) ([]int64, []int64, error) {
	if job.TaskID == nil || *job.TaskID <= 0 {
		return nil, nil, errors.New("归并任务缺少所属任务")
	}
	task := s.m.ResolveTask(i64s(*job.TaskID))
	if task == nil {
		return nil, nil, errors.New("归并任务所属任务不存在或已归档")
	}
	findings := make([]*db.DBFinding, 0, len(job.FindingIDs))
	findingsByID := make(map[int64]*db.DBFinding, len(job.FindingIDs))
	for _, id := range job.FindingIDs {
		finding, err := s.m.pg.GetFinding(id)
		if err != nil {
			return nil, nil, err
		}
		if finding == nil {
			return nil, nil, fmt.Errorf("漏洞 #%d 不存在", id)
		}
		findings = append(findings, finding)
		findingsByID[id] = finding
	}

	runtime := &taskLLMRuntime{s: s, taskID: task.ID, agentKey: agent.FindingMergerAgentKey}
	if s.engine != nil {
		s.engine.BeginLLMCall(task.ID)
		defer s.engine.EndLLMCall(task.ID)
	}
	groups := []findingMergeCluster{{FindingIDs: append([]int64(nil), job.FindingIDs...), Reason: "重新生成现有漏洞组"}}
	ungrouped := []int64{}
	if job.Mode != "regenerate" {
		blocked := map[[2]int64]bool{}
		if job.Mode == "auto" {
			pairs, pairErr := s.m.pg.FindingCaseBlockPairsForTask(ctx, *job.TaskID)
			if pairErr != nil {
				return nil, nil, pairErr
			}
			for _, pair := range pairs {
				blocked[casePairKey(pair.LeftID, pair.RightID)] = true
			}
		}
		plan, err := s.clusterFindingMergeInput(ctx, runtime, task, findings, job.Mode == "auto", blocked)
		if err != nil {
			return nil, nil, err
		}
		groups = plan.Groups
		ungrouped = plan.UngroupedFindingIDs
	}

	if err := s.m.pg.SetFindingMergeJobStatus(ctx, job.ID, "reporting"); err != nil {
		return nil, nil, err
	}
	analyses := make([]db.FindingCaseAnalysis, 0, len(groups))
	for i, group := range groups {
		groupFindings := make([]*db.DBFinding, 0, len(group.FindingIDs))
		for _, id := range group.FindingIDs {
			finding := findingsByID[id]
			if finding == nil {
				return nil, nil, fmt.Errorf("漏洞组 %d 引用了未选择的 Finding #%d", i+1, id)
			}
			groupFindings = append(groupFindings, finding)
		}
		result, err := s.generateFindingMergeReport(ctx, runtime, task, group, groupFindings)
		if err != nil {
			return nil, nil, fmt.Errorf("漏洞组 %d 报告生成失败: %w", i+1, err)
		}
		analyses = append(analyses, db.FindingCaseAnalysis{
			FindingIDs: group.FindingIDs, RootCauseKey: group.RootCauseKey,
			Title: result.Title, Reason: result.Reason,
			Severity: result.Severity, SeverityReason: result.SeverityReason, Report: result.Report,
		})
	}
	if err := s.m.pg.SetFindingMergeJobStatus(ctx, job.ID, "saving"); err != nil {
		return nil, nil, err
	}
	actor := agent.FindingMergerAgentKey
	if job.Mode == "auto" {
		actor += ":auto"
	}
	caseIDs, err := s.m.pg.SaveFindingCaseAnalyses(ctx, *job.TaskID, analyses, ungrouped, actor)
	return caseIDs, ungrouped, err
}

func (s *Server) clusterFindingMergeInput(ctx context.Context, runtime *taskLLMRuntime, task *Task, findings []*db.DBFinding, allowEmpty bool, blocked map[[2]int64]bool) (findingMergeClusterPlan, error) {
	prompt, err := s.findingMergerPrompt()
	if err != nil {
		return findingMergeClusterPlan{}, err
	}
	maxTokens := runtime.maxTokens()
	if maxTokens <= 0 || maxTokens > 3200 {
		maxTokens = 3200
	}
	temperature := 0.1
	userPrompt := findingMergePrompt(task, findings)
	if len(blocked) > 0 {
		var b strings.Builder
		b.WriteString(userPrompt)
		b.WriteString("\n\n人工已确认不能归入同组的 Finding 对（不得合并）：\n")
		for pair := range blocked {
			fmt.Fprintf(&b, "- %d <-> %d\n", pair[0], pair[1])
		}
		userPrompt = b.String()
	}
	message, _, _, err := runtime.Complete(ctx, llm.CompletionRequest{
		System: []string{
			prompt,
			"当前是聚类阶段。只输出 groups 与 ungrouped_finding_ids，不生成 title、severity、severity_reason 或 report。",
		},
		Messages:    []llm.Message{llm.UserText(userPrompt)},
		MaxTokens:   maxTokens,
		Temperature: &temperature,
	})
	if err != nil {
		return findingMergeClusterPlan{}, fmt.Errorf("漏洞聚类 Agent 调用失败: %w", err)
	}
	plan, err := parseFindingMergeClusterPlan(message.Text())
	if err != nil {
		return findingMergeClusterPlan{}, err
	}
	selected := make([]int64, 0, len(findings))
	for _, finding := range findings {
		selected = append(selected, finding.ID)
	}
	if err := validateFindingMergeClusterPlanMode(selected, plan, allowEmpty, blocked); err != nil {
		return findingMergeClusterPlan{}, err
	}
	return plan, nil
}

func (s *Server) generateFindingMergeReport(ctx context.Context, runtime *taskLLMRuntime, task *Task, group findingMergeCluster, findings []*db.DBFinding) (findingMergeAgentResult, error) {
	maxTokens := runtime.maxTokens()
	if maxTokens <= 0 || maxTokens > 6000 {
		maxTokens = 6000
	}
	temperature := 0.1
	message, _, _, err := runtime.Complete(ctx, llm.CompletionRequest{
		System: []string{
			agent.FindingMergerReportPrompt,
			"当前组根因键: " + group.RootCauseKey,
			"当前组聚类理由: " + group.Reason,
		},
		Messages:    []llm.Message{llm.UserText(findingMergeReportPrompt(task, findings))},
		MaxTokens:   maxTokens,
		Temperature: &temperature,
	})
	if err != nil {
		return findingMergeAgentResult{}, err
	}
	return parseFindingMergeAgentResult(message.Text())
}

func parseFindingMergeClusterPlan(raw string) (findingMergeClusterPlan, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return findingMergeClusterPlan{}, errors.New("聚类 Agent 响应中没有 JSON 对象")
	}
	var plan findingMergeClusterPlan
	if err := json.Unmarshal([]byte(raw[start:end+1]), &plan); err != nil {
		return findingMergeClusterPlan{}, fmt.Errorf("解析聚类响应失败: %w", err)
	}
	for i := range plan.Groups {
		plan.Groups[i].RootCauseKey = strings.TrimSpace(plan.Groups[i].RootCauseKey)
		plan.Groups[i].Reason = strings.TrimSpace(plan.Groups[i].Reason)
	}
	return plan, nil
}

func validateFindingMergeClusterPlan(selected []int64, plan findingMergeClusterPlan) error {
	return validateFindingMergeClusterPlanMode(selected, plan, false, nil)
}

func casePairKey(a, b int64) [2]int64 {
	if a > b {
		a, b = b, a
	}
	return [2]int64{a, b}
}

func validateFindingMergeClusterPlanMode(selected []int64, plan findingMergeClusterPlan, allowEmpty bool, blocked map[[2]int64]bool) error {
	if len(plan.Groups) == 0 && !allowEmpty {
		return errors.New("Agent 未发现可归并的漏洞组")
	}
	selectedSet := make(map[int64]bool, len(selected))
	for _, id := range selected {
		selectedSet[id] = true
	}
	seen := map[int64]bool{}
	rootKeys := map[string]bool{}
	for i, group := range plan.Groups {
		if len(group.FindingIDs) < 2 {
			return fmt.Errorf("漏洞组 %d 少于两条 Finding", i+1)
		}
		if group.RootCauseKey == "" || group.Reason == "" {
			return fmt.Errorf("漏洞组 %d 缺少根因键或理由", i+1)
		}
		if rootKeys[group.RootCauseKey] {
			return fmt.Errorf("根因键 %q 被多个漏洞组重复使用", group.RootCauseKey)
		}
		rootKeys[group.RootCauseKey] = true
		for _, id := range group.FindingIDs {
			if !selectedSet[id] {
				return fmt.Errorf("漏洞组 %d 引用了未选择的 Finding #%d", i+1, id)
			}
			if seen[id] {
				return fmt.Errorf("Finding #%d 被重复分组", id)
			}
			seen[id] = true
		}
		for left := 0; left < len(group.FindingIDs); left++ {
			for right := left + 1; right < len(group.FindingIDs); right++ {
				if blocked[casePairKey(group.FindingIDs[left], group.FindingIDs[right])] {
					return fmt.Errorf("漏洞组 %d 含有人工禁止归并的 Finding 对 #%d/#%d",
						i+1, group.FindingIDs[left], group.FindingIDs[right])
				}
			}
		}
	}
	for _, id := range plan.UngroupedFindingIDs {
		if !selectedSet[id] {
			return fmt.Errorf("未归并列表引用了未选择的 Finding #%d", id)
		}
		if seen[id] {
			return fmt.Errorf("Finding #%d 同时出现在漏洞组和未归并列表", id)
		}
		seen[id] = true
	}
	if len(seen) != len(selectedSet) {
		missing := make([]int64, 0)
		for id := range selectedSet {
			if !seen[id] {
				missing = append(missing, id)
			}
		}
		return fmt.Errorf("聚类结果遗漏 Finding: %v", missing)
	}
	return nil
}

func (s *Server) findingMergerPrompt() (string, error) {
	row, err := s.m.pg.GetAgentByKey(agent.FindingMergerAgentKey)
	if err != nil {
		return "", err
	}
	if row != nil {
		if prompt, err := s.m.pg.CurrentPrompt(row.ID); err == nil && strings.TrimSpace(prompt) != "" {
			return prompt, nil
		}
	}
	return agent.FindingMergerDefaultPrompt, nil
}

func findingMergePrompt(task *Task, findings []*db.DBFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "任务 #%s\n", task.ID)
	fmt.Fprintf(&b, "任务描述: %s\n", task.Description)
	fmt.Fprintf(&b, "任务目标: %s\n", task.Goal)
	b.WriteString("\n待判断 Finding：\n")
	for _, finding := range findings {
		fmt.Fprintf(&b, "\n- Finding #%d\n", finding.ID)
		fmt.Fprintf(&b, "  名称: %s\n", firstNonEmpty(finding.Name, finding.VulnClass, "未命名"))
		fmt.Fprintf(&b, "  类型: %s\n", finding.VulnClass)
		fmt.Fprintf(&b, "  等级: %s\n", finding.Severity)
		fmt.Fprintf(&b, "  摘要: %s\n", clipRunes(finding.Summary, 1600))
		fmt.Fprintf(&b, "  证据: %s\n", clipRunes(finding.Evidence, 2600))
		if finding.Report != "" {
			fmt.Fprintf(&b, "  已有报告: %s\n", clipRunes(finding.Report, 3000))
		}
	}
	return b.String()
}

func findingMergeReportPrompt(task *Task, findings []*db.DBFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "任务 #%s\n", task.ID)
	fmt.Fprintf(&b, "任务描述: %s\n", task.Description)
	fmt.Fprintf(&b, "任务目标: %s\n", task.Goal)
	b.WriteString("\n本组 Finding：\n")
	for _, finding := range findings {
		fmt.Fprintf(&b, "\n- Finding #%d\n", finding.ID)
		fmt.Fprintf(&b, "  名称: %s\n", firstNonEmpty(finding.Name, finding.VulnClass, "未命名"))
		fmt.Fprintf(&b, "  类型: %s\n", finding.VulnClass)
		fmt.Fprintf(&b, "  等级: %s\n", finding.Severity)
		fmt.Fprintf(&b, "  摘要: %s\n", clipRunes(finding.Summary, 1800))
		fmt.Fprintf(&b, "  证据: %s\n", clipRunes(finding.Evidence, 3200))
		if finding.Report != "" {
			fmt.Fprintf(&b, "  已有报告: %s\n", clipRunes(finding.Report, 3200))
		}
	}
	return b.String()
}

func parseFindingMergeAgentResult(raw string) (findingMergeAgentResult, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return findingMergeAgentResult{}, errors.New("Agent 响应中没有 JSON 对象")
	}
	var result findingMergeAgentResult
	if err := json.Unmarshal([]byte(raw[start:end+1]), &result); err != nil {
		return findingMergeAgentResult{}, fmt.Errorf("解析 Agent 响应失败: %w", err)
	}
	result.Title = strings.TrimSpace(result.Title)
	result.Reason = strings.TrimSpace(result.Reason)
	result.Severity = strings.ToLower(strings.TrimSpace(result.Severity))
	result.SeverityReason = strings.TrimSpace(result.SeverityReason)
	result.Report = strings.TrimSpace(result.Report)
	switch {
	case result.Title == "":
		return findingMergeAgentResult{}, errors.New("Agent 未生成统一名称")
	case result.Reason == "":
		return findingMergeAgentResult{}, errors.New("Agent 未生成归并理由")
	case !db.ValidSeverity(result.Severity):
		return findingMergeAgentResult{}, fmt.Errorf("Agent 返回了无效等级 %q", result.Severity)
	case result.SeverityReason == "":
		return findingMergeAgentResult{}, errors.New("Agent 未生成评级依据")
	case result.Report == "":
		return findingMergeAgentResult{}, errors.New("Agent 未生成统一报告")
	}
	result.Title = clipRunes(result.Title, 80)
	result.Reason = clipRunes(result.Reason, 2000)
	result.SeverityReason = clipRunes(result.SeverityReason, 2000)
	return result, nil
}
