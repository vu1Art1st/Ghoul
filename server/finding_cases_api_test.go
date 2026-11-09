package server

import (
	"bytes"
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

func TestFindingCaseAPIIntegrationTask4Findings105And108(t *testing.T) {
	if os.Getenv("GHOUL_FINDING_CASE_IT") != "1" {
		t.Skip("set GHOUL_FINDING_CASE_IT=1 against a dedicated database")
	}
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()

	// This test is intentionally guarded and expected to run against a fresh
	// database. Shape the rows exactly like the reported task4 / 105 / 108 case.
	if _, err := m.pg.Exec(`SELECT setval(pg_get_serial_sequence('tasks','id'), 3, true)`); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("task4 finding case integration", "verify merge", nil, 0, 0)
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
	finding105, err := m.pg.AddFinding(taskID, 0, "SQL 注入", "订单查询 SQL 注入", db.SeverityHigh,
		"order_id 参数可注入", "sqlmap 确认 order_id 可布尔盲注", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if finding105 != 105 {
		t.Fatalf("first finding id=%d, want 105", finding105)
	}
	if _, err := m.pg.Exec(`SELECT setval(pg_get_serial_sequence('findings','id'), 107, true)`); err != nil {
		t.Fatal(err)
	}
	finding108, err := m.pg.AddFinding(taskID, 0, "订单接口 SQL 注入", "订单查询 SQL 注入", db.SeverityHigh,
		"order_id 参数可注入", "同一参数与响应差异确认可布尔盲注", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if finding108 != 108 {
		t.Fatalf("second finding id=%d, want 108", finding108)
	}
	defer m.pg.DeleteFinding(finding105)
	defer m.pg.DeleteFinding(finding108)

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
	s := &Server{
		m: m,
		provByProfile: map[int64]*provEntry{profileID: {
			prov: retestProvider{complete: func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
				return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
					llm.TextBlock(`{"title":"订单查询 SQL 注入","reason":"两条记录使用同一 order_id 参数并命中相同 SQL 注入根因"}`),
				}}, "end_turn", llm.Usage{}, nil
			}},
			cfg: agent.Config{Model: "test-model"},
		}},
	}
	suggestReq := httptest.NewRequest(http.MethodPost, "/api/exploration/finding-cases/suggest",
		bytes.NewBufferString(`{"finding_ids":[105,108]}`))
	suggestRec := httptest.NewRecorder()
	s.suggestFindingCase(suggestRec, suggestReq)
	if suggestRec.Code != http.StatusOK {
		t.Fatalf("suggest status=%d body=%s", suggestRec.Code, suggestRec.Body.String())
	}
	var suggestion findingCaseSuggestion
	if err := json.Unmarshal(suggestRec.Body.Bytes(), &suggestion); err != nil {
		t.Fatal(err)
	}
	if suggestion.Title != "订单查询 SQL 注入" || suggestion.Reason == "" {
		t.Fatalf("unexpected AI suggestion: %+v", suggestion)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/exploration/finding-cases",
		bytes.NewBufferString(`{"finding_ids":[105,108],"title":"订单查询 SQL 注入","reason":"两条记录使用同一 order_id 参数并命中相同 SQL 注入根因"}`))
	rec := httptest.NewRecorder()
	s.mergeFindingCase(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("merge status=%d body=%s", rec.Code, rec.Body.String())
	}
	var item db.FindingCase
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.TaskID == nil || *item.TaskID != 4 || item.Count != 2 {
		t.Fatalf("merge did not infer task4: %+v", item)
	}
	defer m.pg.DeleteFindingCase(t.Context(), item.ID)

	detailReq := httptest.NewRequest(http.MethodGet, "/api/exploration/finding-cases/"+strconv.FormatInt(item.ID, 10), nil)
	detailReq.SetPathValue("id", strconv.FormatInt(item.ID, 10))
	detailRec := httptest.NewRecorder()
	s.getFindingCase(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detailRec.Code, detailRec.Body.String())
	}
	var detail struct {
		Members []db.DBFinding `json:"members"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Members) != 2 {
		t.Fatalf("members=%d, want 2", len(detail.Members))
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/exploration/finding-cases/"+strconv.FormatInt(item.ID, 10), nil)
	deleteReq.SetPathValue("id", strconv.FormatInt(item.ID, 10))
	deleteRec := httptest.NewRecorder()
	s.deleteFindingCase(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	for _, id := range []int64{105, 108} {
		finding, getErr := m.pg.GetFinding(id)
		if getErr != nil || finding == nil {
			t.Fatalf("finding #%d must survive group deletion: finding=%+v err=%v", id, finding, getErr)
		}
	}
}
