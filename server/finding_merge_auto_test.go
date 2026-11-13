package server

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/report"
	"github.com/Autumn-27/norma/llm"
)

func TestFindingEvidenceImageKindsCoverC2Screenshots(t *testing.T) {
	for _, kind := range []string{"screenshot", "image", "c2_screenshot", "c2_image"} {
		if !findingEvidenceIsImage(kind) {
			t.Fatalf("kind %q should be treated as an embedded image", kind)
		}
	}
	for _, kind := range []string{"output", "c2_output", "file"} {
		if findingEvidenceIsImage(kind) {
			t.Fatalf("kind %q must not be embedded as an image", kind)
		}
	}
}

func TestReportArtifactReferenceUsesExactID(t *testing.T) {
	if !reportReferencesArtifact("before\n![shot](artifact:12)\nafter", 12) {
		t.Fatal("exact artifact reference was not detected")
	}
	if reportReferencesArtifact("before\n![shot](artifact:120)\nafter", 12) {
		t.Fatal("artifact reference matched a different ID")
	}
}

func TestFindingCaseViewPreservesDeliveryMetadata(t *testing.T) {
	earlier := time.Date(2026, 11, 13, 1, 20, 0, 0, time.UTC)
	later := earlier.Add(5 * time.Minute)
	got := findingCaseViewsAsDBFindings([]report.CaseView{{
		Case: db.FindingCase{
			ID: 9, Severity: "high", Title: "same sink", Reason: "shared root cause", Count: 2,
		},
		Members: []*db.DBFinding{
			{ID: 101, Worker: "work#3", CreatedAt: later, AssetIDs: []int64{2, 1}, EvidenceVersion: 2},
			{ID: 102, Worker: "work#1", CreatedAt: earlier, AssetIDs: []int64{1}, EvidenceVersion: 4},
		},
	}})
	if len(got) != 1 {
		t.Fatalf("case views=%d want 1", len(got))
	}
	item := got[0]
	if item.FindingCount != 2 || item.CreatedAt != earlier || item.Worker != "work#1, work#3" {
		t.Fatalf("unexpected delivery metadata: %+v", item)
	}
	if item.EvidenceVersion != 4 || len(item.AssetIDs) != 2 || item.AssetIDs[0] != 1 || item.AssetIDs[1] != 2 {
		t.Fatalf("unexpected case evidence/assets: %+v", item)
	}
}

func TestEvidenceStorageRefStaysInsideManagedRoot(t *testing.T) {
	root := t.TempDir()
	s := &Server{m: &Manager{dir: root}}
	path := filepath.Join(root, "evidence", "shot.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("png-bytes")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.readEvidenceStorageRef("evidence/shot.png")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("storage_ref read=%q err=%v", got, err)
	}
	outside := root + "-outside-evidence-test.png"
	if err := os.WriteFile(outside, want, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	if _, err := s.readEvidenceStorageRef(outside); err == nil {
		t.Fatal("storage_ref outside managed root was accepted")
	}
}

func TestReportExportWarningsPreserveAutonomousOutcome(t *testing.T) {
	warnings := reportExportWarnings(
		[]*db.DBFinding{
			{ID: 1, Report: "## 证据", EvidenceVersion: 2, ReportEvidenceVersion: 1},
			{ID: 2, Report: ""},
		},
		[]report.CaseView{{Case: db.FindingCase{ID: 7, ReportStale: true}}},
	)
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"Finding #1", "版本落后", "Finding #2", "漏洞组 #7"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings=%q missing %q", joined, want)
		}
	}
}

func TestReportTaskTitleUsesShortTaskName(t *testing.T) {
	if got := reportTaskTitle(&Task{Name: ""}); got != "渗透测试报告" {
		t.Fatalf("empty task name title=%q", got)
	}
	long := strings.Repeat("长", 60)
	got := reportTaskTitle(&Task{Name: long})
	if !strings.HasPrefix(got, "渗透测试报告 — ") || len([]rune(got)) > len([]rune("渗透测试报告 — "))+41 {
		t.Fatalf("long task title was not bounded: %q", got)
	}
}

func TestFindingMergeClusterRejectsBlockedPair(t *testing.T) {
	plan := findingMergeClusterPlan{Groups: []findingMergeCluster{{
		FindingIDs: []int64{11, 12}, RootCauseKey: "same-auth", Reason: "same root",
	}}}
	err := validateFindingMergeClusterPlanMode([]int64{11, 12}, plan, false,
		map[[2]int64]bool{casePairKey(11, 12): true})
	if err == nil || !strings.Contains(err.Error(), "人工禁止") {
		t.Fatalf("blocked pair was accepted: %v", err)
	}
}

func TestFindingAutoMergeInputSkipsLockedCasesAndIncludesBlocks(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	task, err := m.CreateTask("auto merge selector", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	defer m.pg.DeleteTask(taskID)

	var ids []int64
	for i := 0; i < 4; i++ {
		id, addErr := m.pg.AddFinding(taskID, 0, "auth", "selector "+strconv.Itoa(i),
			db.SeverityHigh, "summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		defer m.pg.DeleteFinding(id)
	}
	manualCase, err := m.pg.MergeFindingCase(t.Context(), taskID, ids[:2],
		"manual auth", "human decision", "human")
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteFindingCase(t.Context(), manualCase)

	s := &Server{m: m}
	eligible, firstHash, ok, err := s.findingAutoMergeInput(context.Background(), taskID)
	if err != nil || !ok {
		t.Fatalf("auto input: ok=%t err=%v", ok, err)
	}
	if len(eligible) != 2 || eligible[0] != ids[2] || eligible[1] != ids[3] {
		t.Fatalf("locked case members leaked into auto input: %v", eligible)
	}
	_, err = m.pg.Exec(`INSERT INTO finding_case_blocks(left_id,right_id,reason)
		VALUES(LEAST($1::bigint,$2::bigint),GREATEST($1::bigint,$2::bigint),'manual split')`, ids[2], ids[0])
	if err != nil {
		t.Fatal(err)
	}
	_, secondHash, _, err := s.findingAutoMergeInput(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == secondHash {
		t.Fatal("finding_case_blocks change did not alter auto merge fingerprint")
	}
}

func TestReconcileAutoFindingMergeRunsCompletedTask(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	task, err := m.CreateTask("automatic merge", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	defer m.pg.DeleteTask(taskID)
	var ids []int64
	for i := 0; i < 2; i++ {
		id, addErr := m.pg.AddFinding(taskID, 0, "auth", "auto "+strconv.Itoa(i),
			db.SeverityHigh, "same auth root", "same evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		defer m.pg.DeleteFinding(id)
	}
	profileID, err := m.pg.SaveProfile(&db.LLMProfile{
		Name: "auto-finding-merge-test", Format: "openai", Model: "test-model", APIKey: "sk-test",
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
			prov: retestProvider{complete: func(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
				if strings.Contains(strings.Join(req.System, "\n"), "当前是聚类阶段") {
					body := `{"groups":[{"finding_ids":[` +
						strconv.FormatInt(ids[0], 10) + `,` + strconv.FormatInt(ids[1], 10) +
						`],"root_cause_key":"same-auth","reason":"same auth root"}],"ungrouped_finding_ids":[]}`
					return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(body)}},
						"end_turn", llm.Usage{}, nil
				}
				return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
					llm.TextBlock(`{"title":"auth bypass","reason":"same auth root","severity":"high","severity_reason":"anonymous","report":"## report"}`),
				}}, "end_turn", llm.Usage{}, nil
			}},
			cfg: agent.Config{Model: "test-model"},
		}},
	}
	s.seedFindingMergerAgent()
	if err := m.pg.SetBool(settingFindingAutoMergeEnabled, true); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTaskStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	s.reconcileAutoFindingMerges(context.Background())

	var job *db.FindingMergeJob
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, _ = m.pg.LatestAutoFindingMergeJobForTask(context.Background(), taskID)
		if job != nil && (job.Status == "succeeded" || job.Status == "failed") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job == nil || job.Status != "succeeded" || job.Mode != "auto" || len(job.CaseIDs) != 1 {
		t.Fatalf("automatic merge job mismatch: %+v", job)
	}
	defer m.pg.DeleteFindingCase(context.Background(), job.CaseIDs[0])
	item, err := m.pg.GetFindingCase(context.Background(), job.CaseIDs[0])
	if err != nil || item == nil || !item.Active || item.ManagedBy != "auto" ||
		item.Locked || item.RootCauseKey != "same-auth" || item.ReportStale {
		t.Fatalf("automatic case mismatch: item=%+v err=%v", item, err)
	}
	_, stableHash, ok, err := s.findingAutoMergeInput(context.Background(), taskID)
	if err != nil || !ok || stableHash != job.InputHash {
		t.Fatalf("report writeback changed auto fingerprint: before=%s after=%s ok=%t err=%v",
			job.InputHash, stableHash, ok, err)
	}
}

func TestTaskDocxEmbedsCaseMemberScreenshots(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	task, err := m.CreateTask("case screenshot docx", "verify member artifacts", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	defer m.pg.DeleteTask(taskID)

	var findingIDs []int64
	for i, kind := range []string{"screenshot", "c2_screenshot"} {
		id, addErr := m.pg.AddFinding(taskID, 0, "auth", "case screenshot "+strconv.Itoa(i),
			db.SeverityHigh, "same auth root", "same evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		findingIDs = append(findingIDs, id)
		defer m.pg.DeleteFinding(id)
		_, _, evidenceErr := m.pg.AddFindingEvidence(t.Context(), id, nil, "test", "screenshot fixture",
			db.FindingEvidence{Kind: kind, Title: kind, Content: testPNGDataURL()})
		if evidenceErr != nil {
			t.Fatal(evidenceErr)
		}
	}
	caseID, err := m.pg.MergeFindingCase(t.Context(), taskID, findingIDs,
		"auth bypass", "same auth root", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteFindingCase(t.Context(), caseID)

	s := &Server{m: m}
	req := httptest.NewRequest(http.MethodGet, "/api/report/docx?task="+task.ID, nil)
	rec := httptest.NewRecorder()
	s.exportTaskDocx(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("docx status=%d body=%s", rec.Code, rec.Body.String())
	}
	images := docxImageCount(t, rec.Body.Bytes())
	if images < 2 {
		t.Fatalf("case docx images=%d, want both member screenshots embedded", images)
	}

	singleReq := httptest.NewRequest(http.MethodGet,
		"/api/exploration/findings/"+strconv.FormatInt(findingIDs[0], 10)+"/docx", nil)
	singleReq.SetPathValue("id", strconv.FormatInt(findingIDs[0], 10))
	singleRec := httptest.NewRecorder()
	s.exportFindingDocx(singleRec, singleReq)
	if singleRec.Code != http.StatusOK || singleRec.Body.Len() == 0 {
		t.Fatalf("single docx status=%d body=%s", singleRec.Code, singleRec.Body.String())
	}
	if images := docxImageCount(t, singleRec.Body.Bytes()); images < 1 {
		t.Fatalf("single finding docx images=%d, want embedded screenshot", images)
	}
}

func docxImageCount(t *testing.T, raw []byte) int {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	for _, file := range zr.File {
		if strings.HasPrefix(file.Name, "word/media/") {
			rc, openErr := file.Open()
			if openErr != nil {
				t.Fatal(openErr)
			}
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
			images++
		}
	}
	return images
}

func testPNGDataURL() string {
	const raw = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2nXQAAAAASUVORK5CYII="
	return "data:image/png;base64," + raw
}
