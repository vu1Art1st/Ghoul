package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMergeFindingCaseInfersTaskAndKeepsValidSourceGroup(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("finding case merge", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	marker := fmt.Sprintf("__finding_case_merge_%d__", time.Now().UnixNano())
	var ids []int64
	for i := 0; i < 5; i++ {
		id, addErr := d.AddFinding(task.ID, 0, marker, fmt.Sprintf("finding %d", i), SeverityHigh,
			"same root cause", "shared evidence", "test", nil)
		if addErr != nil {
			t.Fatalf("AddFinding[%d]: %v", i, addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}

	ctx := context.Background()
	targetCase, err := d.MergeFindingCase(ctx, 0, ids[3:], "target group", "same root cause", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteFindingCase(ctx, targetCase)
	sourceCase, err := d.MergeFindingCase(ctx, 0, ids[:3], "source group", "same root cause", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteFindingCase(ctx, sourceCase)
	source, err := d.GetFindingCase(ctx, sourceCase)
	if err != nil {
		t.Fatal(err)
	}
	if source.TaskID == nil || *source.TaskID != task.ID {
		t.Fatalf("task was not inferred from findings: %+v", source)
	}
	if !source.Active || source.Count != 3 {
		t.Fatalf("initial source group mismatch: %+v", source)
	}

	targetCase, err = d.MergeFindingCase(ctx, 0, []int64{ids[0], ids[3]}, "target group", "same root cause", "test")
	if err != nil {
		t.Fatal(err)
	}
	source, err = d.GetFindingCase(ctx, sourceCase)
	if err != nil {
		t.Fatal(err)
	}
	if !source.Active || source.Count != 2 {
		t.Fatalf("source group with two remaining members must stay active: %+v", source)
	}
	target, err := d.GetFindingCase(ctx, targetCase)
	if err != nil {
		t.Fatal(err)
	}
	if !target.Active || target.Count != 3 {
		t.Fatalf("target group mismatch: %+v", target)
	}
}

func TestDeleteFindingCaseKeepsFindings(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("finding case delete", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	marker := fmt.Sprintf("__finding_case_delete_%d__", time.Now().UnixNano())
	var ids []int64
	for i := 0; i < 2; i++ {
		id, addErr := d.AddFinding(task.ID, 0, marker, fmt.Sprintf("finding %d", i), SeverityHigh,
			"summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatalf("AddFinding[%d]: %v", i, addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}

	ctx := context.Background()
	caseID, err := d.MergeFindingCase(ctx, task.ID, ids, "delete me", "same root cause", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteFindingCase(ctx, caseID); err != nil {
		t.Fatal(err)
	}
	if item, getErr := d.GetFindingCase(ctx, caseID); getErr != nil || item != nil {
		t.Fatalf("case should be deleted: item=%+v err=%v", item, getErr)
	}
	for _, id := range ids {
		if finding, getErr := d.GetFinding(id); getErr != nil || finding == nil {
			t.Fatalf("finding #%d must survive case deletion: finding=%+v err=%v", id, finding, getErr)
		}
	}
}

func TestSaveFindingCaseAnalysisPersistsDeliveryFieldsAtomically(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("finding case analysis", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var ids []int64
	for i := 0; i < 2; i++ {
		id, addErr := d.AddFinding(task.ID, 0, "MCP 网关无鉴权", fmt.Sprintf("finding %d", i), SeverityHigh,
			"同一 MCP 网关缺少鉴权", "同一匿名 tools/call 证据", "test", nil)
		if addErr != nil {
			t.Fatalf("AddFinding[%d]: %v", i, addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}

	ctx := context.Background()
	caseID, err := d.SaveFindingCaseAnalysis(ctx, 0, ids,
		"MCP 网关无鉴权", "两条 Finding 均来自同一匿名 tools/call 入口",
		SeverityCritical, "无需认证即可调用工具，影响全部租户",
		"## 概述\n\nMCP 网关未校验 Authorization。", "finding_merger")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteFindingCase(ctx, caseID)
	item, err := d.GetFindingCase(ctx, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if item.TaskID == nil || *item.TaskID != task.ID || item.Count != 2 ||
		item.Severity != SeverityCritical || item.SeverityReason == "" || item.Report == "" ||
		item.ReportVersion != 1 {
		t.Fatalf("analysis was not persisted atomically: %+v", item)
	}
	if err := d.UpdateFindingCaseReport(ctx, caseID, item.Version, item.Title,
		"## 概述\n\n更新后的统一报告。", item.Severity, item.SeverityReason); err != nil {
		t.Fatal(err)
	}
	updated, err := d.GetFindingCase(ctx, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != item.Version+1 || updated.ReportVersion != item.ReportVersion+1 {
		t.Fatalf("report update versions were not incremented: before=%+v after=%+v", item, updated)
	}
}

func TestFindingMergeJobLifecycle(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("finding merge job", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var ids []int64
	for i := 0; i < 2; i++ {
		id, addErr := d.AddFinding(task.ID, 0, "job", fmt.Sprintf("finding %d", i), SeverityHigh,
			"summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}
	jobID, err := d.CreateFindingMergeJob(context.Background(), ids, "cluster")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM finding_merge_jobs WHERE id=$1`, jobID)
	if err := d.SetFindingMergeJobStatus(context.Background(), jobID, "clustering"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingMergeJob(context.Background(), jobID, nil, nil, "failed", "test failure"); err != nil {
		t.Fatal(err)
	}
	job, err := d.GetFindingMergeJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.Status != "failed" || job.Error != "test failure" ||
		job.TaskID == nil || *job.TaskID != task.ID || job.Mode != "cluster" {
		t.Fatalf("job lifecycle mismatch: %+v", job)
	}
}

func TestSaveFindingCaseAnalysesCreatesMultipleGroups(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("multi finding case analysis", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var ids []int64
	for i := 0; i < 6; i++ {
		id, addErr := d.AddFinding(task.ID, 0, fmt.Sprintf("class-%d", i/2),
			fmt.Sprintf("finding %d", i), SeverityHigh, "summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}
	analyses := []FindingCaseAnalysis{
		{FindingIDs: ids[:2], Title: "MCP 网关无鉴权", Reason: "同一网关", Severity: SeverityCritical,
			SeverityReason: "无需认证", Report: "## MCP\n\n统一报告"},
		{FindingIDs: ids[2:4], Title: "订单查询 SQL 注入", Reason: "同一参数", Severity: SeverityHigh,
			SeverityReason: "可读取数据", Report: "## SQLi\n\n统一报告"},
		{FindingIDs: ids[4:], Title: "订单越权读取", Reason: "同一对象授权缺失", Severity: SeverityHigh,
			SeverityReason: "可越权读取", Report: "## IDOR\n\n统一报告"},
	}
	caseIDs, err := d.SaveFindingCaseAnalyses(context.Background(), task.ID, analyses, nil, "finding_merger")
	if err != nil {
		t.Fatal(err)
	}
	if len(caseIDs) != 3 {
		t.Fatalf("case ids=%v, want 3", caseIDs)
	}
	for i, caseID := range caseIDs {
		defer d.DeleteFindingCase(context.Background(), caseID)
		item, getErr := d.GetFindingCase(context.Background(), caseID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if item.Count != 2 || item.Title != analyses[i].Title || item.Report != analyses[i].Report {
			t.Fatalf("case %d mismatch: %+v", i, item)
		}
	}
	jobID, err := d.CreateFindingMergeJob(context.Background(), ids, "cluster")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM finding_merge_jobs WHERE id=$1`, jobID)
	if err := d.SetFindingMergeJobStatus(context.Background(), jobID, "saving"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingMergeJob(context.Background(), jobID, caseIDs, nil, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	job, err := d.GetFindingMergeJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.Status != "succeeded" || job.GroupCount != 3 ||
		fmt.Sprint(job.CaseIDs) != fmt.Sprint(caseIDs) || len(job.UngroupedFindingIDs) != 0 {
		t.Fatalf("multi-group job result mismatch: %+v", job)
	}
}

func TestAddFindingCaseMembersMarksReportStaleAndLocksCase(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("add case member", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var ids []int64
	for i := 0; i < 3; i++ {
		id, addErr := d.AddFinding(task.ID, 0, "auth", fmt.Sprintf("finding %d", i), SeverityHigh,
			"same root cause", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}

	ctx := context.Background()
	caseID, err := d.SaveFindingCaseAnalysis(ctx, task.ID, ids[:2], "auth bypass",
		"same auth entry", SeverityHigh, "anonymous access", "## report", "finding_merger")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteFindingCase(ctx, caseID)
	before, err := d.GetFindingCase(ctx, caseID)
	if err != nil {
		t.Fatal(err)
	}
	version, err := d.AddFindingCaseMembers(ctx, caseID, before.Version, ids[2:],
		"human", "new evidence confirms the same root cause")
	if err != nil {
		t.Fatal(err)
	}
	after, err := d.GetFindingCase(ctx, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if version != after.Version || after.Count != 3 || !after.Locked ||
		after.ManagedBy != "manual" || !after.ReportStale {
		t.Fatalf("member append did not invalidate/lock report: before=%+v after=%+v", before, after)
	}
}

func TestCreateAutoFindingMergeJobIsIdempotent(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("auto merge idempotency", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var ids []int64
	for i := 0; i < 2; i++ {
		id, addErr := d.AddFinding(task.ID, 0, "auth", fmt.Sprintf("finding %d", i), SeverityHigh,
			"summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}
	firstID, created, err := d.CreateAutoFindingMergeJob(context.Background(), ids, "fingerprint-1")
	if err != nil || !created {
		t.Fatalf("first auto job: id=%d created=%t err=%v", firstID, created, err)
	}
	defer d.Exec(`DELETE FROM finding_merge_jobs WHERE id=$1`, firstID)
	secondID, created, err := d.CreateAutoFindingMergeJob(context.Background(), ids, "fingerprint-1")
	if err != nil {
		t.Fatal(err)
	}
	if created || secondID != firstID {
		t.Fatalf("duplicate auto job: first=%d second=%d created=%t", firstID, secondID, created)
	}
	job, err := d.GetFindingMergeJob(context.Background(), firstID)
	if err != nil || job == nil || job.Mode != "auto" || job.InputHash != "fingerprint-1" {
		t.Fatalf("auto job metadata mismatch: job=%+v err=%v", job, err)
	}
}

func TestSaveFindingCaseAnalysesAllowsAutoEmptyResult(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	task, err := d.CreateTask("auto empty result", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var selected []int64
	for i := 0; i < 2; i++ {
		id, addErr := d.AddFinding(task.ID, 0, "auth", fmt.Sprintf("finding %d", i), SeverityHigh,
			"summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		selected = append(selected, id)
		defer d.DeleteFinding(id)
	}
	caseIDs, err := d.SaveFindingCaseAnalyses(context.Background(), task.ID, []FindingCaseAnalysis{{
		FindingIDs: selected, RootCauseKey: "same-auth", Title: "auth bypass",
		Reason: "same root", Severity: SeverityHigh, SeverityReason: "anonymous",
		Report: "## report",
	}}, nil, "finding_merger:auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(caseIDs) != 1 {
		t.Fatalf("auto case ids=%v", caseIDs)
	}
	defer d.DeleteFindingCase(context.Background(), caseIDs[0])
	ids, err := d.SaveFindingCaseAnalyses(context.Background(), task.ID, nil, selected, "finding_merger:auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("empty auto result returned cases: %v", ids)
	}
	item, err := d.GetFindingCase(context.Background(), caseIDs[0])
	if err != nil || item == nil || item.Active || item.Count != 0 {
		t.Fatalf("obsolete auto case was not dissolved: item=%+v err=%v", item, err)
	}
}

func TestAutoFindingCaseSaveDoesNotOverwriteManualLock(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	task, err := d.CreateTask("auto lock conflict", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var ids []int64
	for i := 0; i < 2; i++ {
		id, addErr := d.AddFinding(task.ID, 0, "auth", fmt.Sprintf("finding %d", i), SeverityHigh,
			"summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		defer d.DeleteFinding(id)
	}
	analysis := []FindingCaseAnalysis{{
		FindingIDs: ids, RootCauseKey: "same-auth", Title: "auth bypass",
		Reason: "same root", Severity: SeverityHigh, SeverityReason: "anonymous",
		Report: "## report",
	}}
	caseIDs, err := d.SaveFindingCaseAnalyses(context.Background(), task.ID, analysis, nil, "finding_merger:auto")
	if err != nil || len(caseIDs) != 1 {
		t.Fatalf("create auto case: ids=%v err=%v", caseIDs, err)
	}
	defer d.DeleteFindingCase(context.Background(), caseIDs[0])
	if err := d.LockFindingCaseForManual(context.Background(), caseIDs[0], "human", "manual review"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveFindingCaseAnalyses(context.Background(), task.ID, analysis, nil, "finding_merger:auto"); !errors.Is(err, ErrFindingCaseConflict) {
		t.Fatalf("auto save should lose to manual lock, got %v", err)
	}
}

func TestFindingMetaIncludesActiveCase(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("finding meta case", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	var ids []int64
	var nodeIDs []int64
	for i := 0; i < 2; i++ {
		nodeID, nodeErr := store.AddNode(KindFinding, map[string]any{
			"vulnclass": "MCP", "severity": SeverityHigh, "summary": fmt.Sprintf("finding %d", i),
		}, 5, "confirmed", "worker", nil)
		if nodeErr != nil {
			t.Fatal(nodeErr)
		}
		id, addErr := d.AddFinding(task.ID, nodeID, "MCP", fmt.Sprintf("finding %d", i), SeverityHigh,
			"summary", "evidence", "test", nil)
		if addErr != nil {
			t.Fatal(addErr)
		}
		ids = append(ids, id)
		nodeIDs = append(nodeIDs, nodeID)
		defer d.DeleteFinding(id)
	}
	ctx := context.Background()
	caseID, err := d.SaveFindingCaseAnalysis(ctx, task.ID, ids, "MCP 网关无鉴权",
		"同一匿名入口", SeverityCritical, "无需认证", "## 报告", "finding_merger")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteFindingCase(ctx, caseID)
	meta, err := d.FindingMetaByNodeID(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range nodeIDs {
		item := meta[nodeID]
		if item.CaseID != caseID || item.CaseTitle != "MCP 网关无鉴权" ||
			item.CaseSeverity != SeverityCritical || item.CaseCount != 2 {
			t.Fatalf("node %d case metadata mismatch: %+v", nodeID, item)
		}
	}
}
