package server

import (
	"context"
	"fmt"

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
		out = append(out, &db.DBFinding{
			ID:        -item.Case.ID,
			TaskID:    item.Case.TaskID,
			VulnClass: name,
			Name:      name,
			Severity:  item.Case.Severity,
			Summary:   item.Case.Reason,
			Evidence:  item.Case.SeverityReason,
			Report:    item.Case.Report,
			Status:    db.FindingConfirmed,
		})
	}
	return out
}
