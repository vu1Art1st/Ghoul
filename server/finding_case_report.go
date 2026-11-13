package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/report"
)

func (s *Server) activeFindingCaseViews(ctx context.Context, taskID int64) ([]report.CaseView, map[int64]bool, error) {
	cases, err := s.m.pg.ActiveFindingCasesForTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	views := make([]report.CaseView, 0, len(cases))
	memberIDs := map[int64]bool{}
	for _, item := range cases {
		members, err := s.m.pg.FindingCaseMembers(ctx, item.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, member := range members {
			memberIDs[member.ID] = true
		}
		views = append(views, report.CaseView{Case: item, Members: members})
	}
	return views, memberIDs, nil
}

func findingCaseViewsAsDBFindings(views []report.CaseView) []*db.DBFinding {
	out := make([]*db.DBFinding, 0, len(views))
	for _, item := range views {
		name := item.Case.Title
		if name == "" {
			name = fmt.Sprintf("漏洞组 #%d", item.Case.ID)
		}
		assetSet := map[int64]bool{}
		workerSet := map[string]bool{}
		var evidenceVersion int64
		var reportEvidenceVersion int64
		var createdAt time.Time
		for _, member := range item.Members {
			for _, id := range member.AssetIDs {
				if id > 0 {
					assetSet[id] = true
				}
			}
			if worker := strings.TrimSpace(member.Worker); worker != "" {
				workerSet[worker] = true
			}
			if member.EvidenceVersion > evidenceVersion {
				evidenceVersion = member.EvidenceVersion
			}
			if member.ReportEvidenceVersion > reportEvidenceVersion {
				reportEvidenceVersion = member.ReportEvidenceVersion
			}
			if !member.CreatedAt.IsZero() && (createdAt.IsZero() || member.CreatedAt.Before(createdAt)) {
				createdAt = member.CreatedAt
			}
		}
		assetIDs := make([]int64, 0, len(assetSet))
		for id := range assetSet {
			assetIDs = append(assetIDs, id)
		}
		sort.Slice(assetIDs, func(i, j int) bool { return assetIDs[i] < assetIDs[j] })
		workers := make([]string, 0, len(workerSet))
		for worker := range workerSet {
			workers = append(workers, worker)
		}
		sort.Strings(workers)
		evidence := item.Case.SeverityReason
		if strings.TrimSpace(item.Evidence) != "" {
			if strings.TrimSpace(evidence) != "" {
				evidence += "\n\n"
			}
			evidence += "成员证据汇总：\n" + item.Evidence
		}
		reportText := item.Case.Report
		if item.Case.ReportStale {
			reportText = "> 注意：漏洞组成员已变更，统一报告尚未重新生成。以下为上一版报告。\n\n" + reportText
		}
		out = append(out, &db.DBFinding{
			ID:                    -item.Case.ID,
			TaskID:                item.Case.TaskID,
			VulnClass:             name,
			Name:                  name,
			Severity:              item.Case.Severity,
			Summary:               item.Case.Reason,
			Evidence:              evidence,
			Report:                reportText,
			Worker:                strings.Join(workers, ", "),
			AssetIDs:              assetIDs,
			EvidenceVersion:       evidenceVersion,
			ReportEvidenceVersion: reportEvidenceVersion,
			FindingCount:          max(item.Case.Count, len(item.Members)),
			CreatedAt:             createdAt,
			Status:                db.FindingConfirmed,
		})
	}
	return out
}
