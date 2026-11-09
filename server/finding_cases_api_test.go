package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func TestParseFindingCaseSuggestion(t *testing.T) {
	raw := "模型说明：\n```json\n{\"title\":\"统一名称\",\"reason\":\"两条记录共享同一触发点和响应特征。\"}\n```"
	got, err := parseFindingCaseSuggestion(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "统一名称" || !strings.Contains(got.Reason, "同一触发点") {
		t.Fatalf("unexpected suggestion: %+v", got)
	}
}

func TestParseFindingCaseSuggestionRejectsEmptyFields(t *testing.T) {
	if _, err := parseFindingCaseSuggestion(`{"title":"","reason":"x"}`); err == nil {
		t.Fatal("empty title should be rejected")
	}
}

func TestUniqueFindingIDs(t *testing.T) {
	got, err := uniqueFindingIDs([]int64{108, 105, 108})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []int64{108, 105}) {
		t.Fatalf("uniqueFindingIDs=%v", got)
	}
	if _, err := uniqueFindingIDs([]int64{105}); err == nil {
		t.Fatal("one finding should be rejected")
	}
}

func TestFindingCaseRoutesRegister(t *testing.T) {
	s := &Server{}
	if handler := s.Handler(); handler == nil {
		t.Fatal("handler is nil")
	}
}

func TestParseFindingMergeAgentResult(t *testing.T) {
	got, err := parseFindingMergeAgentResult("```json\n" + `{"title":"MCP 网关无鉴权","reason":"同一匿名入口","severity":"critical","severity_reason":"无需认证即可调用工具","report":"## 概述\n\n证据一致。"}` + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "MCP 网关无鉴权" || got.Severity != db.SeverityCritical || got.Report == "" {
		t.Fatalf("unexpected merge result: %+v", got)
	}
	if _, err := parseFindingMergeAgentResult(`{"title":"x","reason":"x","severity":"urgent","severity_reason":"x","report":"x"}`); err == nil {
		t.Fatal("invalid severity should be rejected")
	}
}

func TestValidateFindingMergeClusterPlan(t *testing.T) {
	plan := findingMergeClusterPlan{
		Groups: []findingMergeCluster{
			{FindingIDs: []int64{105, 106}, RootCauseKey: "mcp", Reason: "same gateway"},
			{FindingIDs: []int64{107, 108}, RootCauseKey: "sqli", Reason: "same parameter"},
		},
		UngroupedFindingIDs: []int64{109},
	}
	if err := validateFindingMergeClusterPlan([]int64{105, 106, 107, 108, 109}, plan); err != nil {
		t.Fatal(err)
	}
	plan.Groups[1].FindingIDs = []int64{106, 108}
	if err := validateFindingMergeClusterPlan([]int64{105, 106, 107, 108, 109}, plan); err == nil {
		t.Fatal("duplicate finding should be rejected")
	}
}

func TestFindingCaseAPIIntegrationTask4AutoClustersMultipleGroups(t *testing.T) {
	if os.Getenv("GHOUL_FINDING_CASE_IT") != "1" {
		t.Skip("set GHOUL_FINDING_CASE_IT=1 against a dedicated database")
	}
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()

	// Guarded test for a fresh database. Task4 has three root causes across
	// findings 105-110 and must produce three independent cases.
	if _, err := m.pg.Exec(`SELECT setval(pg_get_serial_sequence('tasks','id'), 3, true)`); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("task4 finding case integration", "verify multi-group merge", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	if taskID != 4 {
		t.Fatalf("task id=%d, want 4", taskID)
	}
	defer m.pg.DeleteTask(taskID)

	if _, err := m.pg.Exec(`SELECT setval(pg_get_serial_sequence('findings','id'), 104, true)`); err != nil {
		t.Fatal(err)
	}
	seeds := []struct {
		class, name, summary, evidence string
	}{
		{"MCP 网关无鉴权", "MCP initialize 无鉴权", "匿名 initialize 返回 200", "获得 mcp-session-id"},
		{"MCP 网关无鉴权", "MCP tools/call 无鉴权", "匿名 tools/call 可执行", "工具调用返回真实结果"},
		{"SQL 注入", "订单查询 SQL 注入", "order_id 参数可注入", "布尔盲注确认"},
		{"SQL 注入", "订单导出 SQL 注入", "同一 order_id 参数可注入", "导出接口同样可盲注"},
		{"IDOR", "订单详情越权读取", "order_id 可越权读取他人订单", "返回其他用户地址"},
		{"IDOR", "订单附件越权下载", "同一对象级授权缺失", "可下载其他用户附件"},
	}
	findingIDs := make([]int64, 0, len(seeds))
	for i, seed := range seeds {
		id, addErr := m.pg.AddFinding(taskID, 0, seed.class, seed.name, db.SeverityHigh,
			seed.summary, seed.evidence, "test", nil)
		if addErr != nil {
			t.Fatalf("AddFinding[%d]: %v", i, addErr)
		}
		if want := int64(105 + i); id != want {
			t.Fatalf("finding id=%d, want %d", id, want)
		}
		findingIDs = append(findingIDs, id)
		defer m.pg.DeleteFinding(id)
	}

	profileID, err := m.pg.SaveProfile(&db.LLMProfile{
		Name: "finding-case-suggestion-test", Format: "openai", Model: "test-model", APIKey: "sk-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteProfile(profileID)
	if _, err := m.ReplaceTaskLLMProfiles(task.ID, []int64{profileID}, profileID); err != nil {
		t.Fatal(err)
	}
	reportTitles := []string{"MCP 网关无鉴权", "订单查询 SQL 注入", "订单越权读取"}
	reportCall := 0
	s := &Server{
		m: m,
		provByProfile: map[int64]*provEntry{profileID: {
			prov: retestProvider{complete: func(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
				if strings.Contains(strings.Join(req.System, "\n"), "当前是聚类阶段") {
					return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
						llm.TextBlock(`{"groups":[{"finding_ids":[105,106],"root_cause_key":"mcp-missing-auth","reason":"同一 MCP 网关缺少鉴权"},{"finding_ids":[107,108],"root_cause_key":"order-id-sqli","reason":"同一 order_id 参数存在 SQL 注入"},{"finding_ids":[109,110],"root_cause_key":"order-id-idor","reason":"同一订单对象授权缺失"}],"ungrouped_finding_ids":[]}`),
					}}, "end_turn", llm.Usage{}, nil
				}
				title := reportTitles[reportCall%len(reportTitles)]
				reportCall++
				body, _ := json.Marshal(map[string]any{
					"title": title, "reason": "同一根因", "severity": "high",
					"severity_reason": "无需高权限即可利用，影响业务数据机密性",
					"report":          "## 概述\n\n" + title + "统一报告。",
				})
				return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
					llm.TextBlock(string(body)),
				}}, "end_turn", llm.Usage{}, nil
			}},
			cfg: agent.Config{Model: "test-model"},
		}},
	}
	s.seedFindingMergerAgent()
	if agentRow, err := m.pg.GetAgentByKey(agent.FindingMergerAgentKey); err != nil || agentRow == nil {
		t.Fatalf("finding merger agent was not seeded: agent=%+v err=%v", agentRow, err)
	}

	job := &db.FindingMergeJob{TaskID: &taskID, FindingIDs: findingIDs, Mode: "cluster"}
	caseIDs, ungrouped, err := s.executeFindingMergeAgent(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	if len(caseIDs) != 3 || len(ungrouped) != 0 {
		t.Fatalf("case_ids=%v ungrouped=%v, want 3 groups and no ungrouped", caseIDs, ungrouped)
	}
	for i, caseID := range caseIDs {
		defer m.pg.DeleteFindingCase(t.Context(), caseID)
		item, getErr := m.pg.GetFindingCase(t.Context(), caseID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if item.TaskID == nil || *item.TaskID != 4 || item.Count != 2 ||
			item.Title != reportTitles[i] || item.Report == "" || item.SeverityReason == "" {
			t.Fatalf("case %d mismatch: %+v", i, item)
		}
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/exploration/finding-cases/"+strconv.FormatInt(caseIDs[0], 10), nil)
	detailReq.SetPathValue("id", strconv.FormatInt(caseIDs[0], 10))
	detailRec := httptest.NewRecorder()
	s.getFindingCase(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detailRec.Code, detailRec.Body.String())
	}
	var detail struct {
		Members []FindingDTO `json:"members"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Members) != 2 {
		t.Fatalf("members=%d, want 2", len(detail.Members))
	}
	for _, member := range detail.Members {
		if member.ID == "" || member.FindingID == "" || member.Name == "" {
			t.Fatalf("member DTO contract is incomplete: %+v", member)
		}
	}
	var rawDetail struct {
		Members []map[string]any `json:"members"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &rawDetail); err != nil {
		t.Fatal(err)
	}
	if len(rawDetail.Members) == 0 || rawDetail.Members[0]["id"] == nil || rawDetail.Members[0]["ID"] != nil {
		t.Fatalf("member JSON must expose lowercase id: %s", detailRec.Body.String())
	}

	reportReq := httptest.NewRequest(http.MethodGet, "/api/report?task=4", nil)
	reportRec := httptest.NewRecorder()
	s.getReport(reportRec, reportReq)
	if reportRec.Code != http.StatusOK {
		t.Fatalf("report status=%d body=%s", reportRec.Code, reportRec.Body.String())
	}
	reportBody := reportRec.Body.String()
	if !strings.Contains(reportBody, "确认发现：**3** 个") {
		t.Fatalf("task report did not count three groups:\n%s", reportBody)
	}
	for _, title := range reportTitles {
		if !strings.Contains(reportBody, title) {
			t.Fatalf("task report missing case %q:\n%s", title, reportBody)
		}
	}

	docxReq := httptest.NewRequest(http.MethodGet, "/api/report/docx?task=4", nil)
	docxRec := httptest.NewRecorder()
	s.exportTaskDocx(docxRec, docxReq)
	if docxRec.Code != http.StatusOK || docxRec.Body.Len() == 0 {
		t.Fatalf("docx status=%d body=%s", docxRec.Code, docxRec.Body.String())
	}

	for _, caseID := range caseIDs {
		deleteReq := httptest.NewRequest(http.MethodDelete, "/api/exploration/finding-cases/"+strconv.FormatInt(caseID, 10), nil)
		deleteReq.SetPathValue("id", strconv.FormatInt(caseID, 10))
		deleteRec := httptest.NewRecorder()
		s.deleteFindingCase(deleteRec, deleteReq)
		if deleteRec.Code != http.StatusOK {
			t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
		}
	}
	for _, id := range findingIDs {
		finding, getErr := m.pg.GetFinding(id)
		if getErr != nil || finding == nil {
			t.Fatalf("finding #%d must survive group deletion: finding=%+v err=%v", id, finding, getErr)
		}
	}
}
