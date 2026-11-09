package db

import (
	"context"
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
