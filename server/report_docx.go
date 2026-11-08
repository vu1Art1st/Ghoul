package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/report"
)

var exportExcludeStatuses = []string{
	db.FindingFalsePositive, db.FindingDuplicate, db.FindingIgnored, db.FindingRiskAccepted,
}

func reportAssetName(asset *db.Asset) string {
	if asset == nil {
		return ""
	}
	switch asset.Type {
	case "endpoint", "app", "service":
		if asset.URL != "" {
			return asset.URL
		}
		if asset.Domain != "" && asset.Port != nil {
			return fmt.Sprintf("%s:%d", asset.Domain, *asset.Port)
		}
		if asset.IP != "" && asset.Port != nil {
			return fmt.Sprintf("%s:%d", asset.IP, *asset.Port)
		}
	}
	for _, value := range []string{asset.Domain, asset.IP, asset.RootDomain, asset.URL, asset.AppName} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return fmt.Sprintf("id:%d", asset.ID)
}

func (s *Server) reportAssetNames(findings []*db.DBFinding) map[int64]string {
	seen := map[int64]bool{}
	var ids []int64
	for _, finding := range findings {
		for _, id := range finding.AssetIDs {
			if id > 0 && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	assets, err := s.m.Assets().GetByIDs(ids)
	if err != nil {
		return nil
	}
	out := make(map[int64]string, len(assets))
	for _, asset := range assets {
		out[asset.ID] = reportAssetName(asset)
	}
	return out
}

func (s *Server) reportRetests(findings []*db.DBFinding) map[int64][]*db.FindingRetest {
	out := map[int64][]*db.FindingRetest{}
	for _, finding := range findings {
		items, err := s.m.pg.ListFindingRetests(finding.ID)
		if err == nil && len(items) > 0 {
			out[finding.ID] = items
		}
	}
	return out
}

func (s *Server) taskCustomerNames(task *Task) string {
	if task == nil || len(task.CompanyIDs) == 0 {
		return ""
	}
	names := make([]string, 0, len(task.CompanyIDs))
	for _, id := range task.CompanyIDs {
		company, err := s.m.pg.Companies().GetCompany(id)
		if err == nil && company != nil {
			names = append(names, company.Name)
		}
	}
	return strings.Join(names, "、")
}

func taskTestWindow(task *Task) string {
	if task == nil || task.FirstRunAt <= 0 {
		return ""
	}
	start := time.Unix(task.FirstRunAt, 0).UTC().Format("2006-01-02 15:04 UTC")
	if task.CompletedAt > 0 {
		return start + " ~ " + time.Unix(task.CompletedAt, 0).UTC().Format("2006-01-02 15:04 UTC")
	}
	return start + " 起（数据截至报告生成时刻）"
}

// artifactImageResolver maps artifact:<finding_evidence.id> to image bytes stored
// as a data URL in finding_evidence.content. Single-finding reports only embed
// images belonging to that finding.
func (s *Server) artifactImageResolver(findingID int64) func(int64) ([]byte, string, bool) {
	return func(id int64) ([]byte, string, bool) {
		var content, title, kind string
		var owner int64
		err := s.m.pg.QueryRow(`SELECT finding_id,content,title,kind FROM finding_evidence WHERE id=$1`, id).
			Scan(&owner, &content, &title, &kind)
		if err != nil || owner <= 0 {
			return nil, "", false
		}
		if findingID > 0 && owner != findingID {
			return nil, "", false
		}
		if kind != "screenshot" && kind != "image" {
			return nil, "", false
		}
		raw := strings.TrimSpace(content)
		if comma := strings.IndexByte(raw, ','); strings.HasPrefix(raw, "data:") && comma >= 0 {
			raw = raw[comma+1:]
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(data) == 0 {
			return nil, "", false
		}
		return data, title, true
	}
}

func (s *Server) attachEvidenceArtifacts(findings []*db.DBFinding) {
	for _, finding := range findings {
		items, err := s.m.pg.ListFindingEvidence(context.Background(), finding.ID)
		if err != nil || len(items) == 0 {
			continue
		}
		var text, reportRefs strings.Builder
		for _, item := range items {
			switch item.Kind {
			case "screenshot", "image":
				if strings.HasPrefix(strings.TrimSpace(item.Content), "data:") {
					fmt.Fprintf(&reportRefs, "\n\n![%s](artifact:%d)", item.Title, item.ID)
				}
			default:
				if strings.TrimSpace(item.Content) != "" {
					fmt.Fprintf(&text, "\n\n[%s]\n%s", item.Title, item.Content)
				}
			}
		}
		if text.Len() > 0 {
			finding.Evidence += text.String()
		}
		if reportRefs.Len() > 0 {
			finding.Report += reportRefs.String()
		}
	}
}

func (s *Server) exportTaskDocx(w http.ResponseWriter, r *http.Request) {
	task := s.m.ResolveTask(r.URL.Query().Get("task"))
	if task == nil {
		writeErr(w, 404, "no active task")
		return
	}
	findings, err := s.m.pg.ListFindingsForExport(db.FindingFilter{
		TaskID: task.ID, ExcludeStatuses: exportExcludeStatuses,
	}, nil)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.attachEvidenceArtifacts(findings)
	counts := map[string]int{}
	appendix := map[string][]string{}
	for _, kind := range []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"} {
		assets, _ := s.m.Assets().QueryByType(kind, 100000, 0)
		if len(assets) == 0 {
			continue
		}
		counts[kind] = len(assets)
		names := make([]string, 0, len(assets))
		for _, asset := range assets {
			if name := reportAssetName(asset); name != "" {
				names = append(names, name)
			}
		}
		appendix[kind] = names
	}
	doc := report.BuildTaskDocx(report.TaskDocxInput{
		TaskID:         taskID(task),
		Title:          "渗透测试报告 — " + task.Description,
		Goal:           task.Goal,
		GeneratedAt:    time.Now(),
		AssetCounts:    counts,
		Findings:       findings,
		AssetNames:     s.reportAssetNames(findings),
		Retests:        s.reportRetests(findings),
		Customer:       s.taskCustomerNames(task),
		TestWindow:     taskTestWindow(task),
		AppendixAssets: appendix,
		PageHeader:     "渗透测试报告 — " + task.Description,
		ArtifactImage:  s.artifactImageResolver(0),
	})
	name := fmt.Sprintf("ghoul-report-task%s-%s.docx", task.ID, time.Now().Format("20060102"))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	_ = doc.Encode(w)
}

func (s *Server) exportFindingDocx(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	finding, err := s.m.pg.GetFinding(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if finding == nil {
		writeErr(w, 404, "finding not found")
		return
	}
	s.attachEvidenceArtifacts([]*db.DBFinding{finding})
	retests, _ := s.m.pg.ListFindingRetests(id)
	customer := ""
	if finding.TaskID != nil {
		if task := s.m.ResolveTask(strconv.FormatInt(*finding.TaskID, 10)); task != nil {
			customer = s.taskCustomerNames(task)
		}
	}
	doc := report.BuildFindingDocx(report.FindingDocxInput{
		Finding:        finding,
		GeneratedAt:    time.Now(),
		AssetNameMap:   s.reportAssetNames([]*db.DBFinding{finding}),
		Retests:        retests,
		Customer:       customer,
		Classification: "内部资料 · 仅限授权方",
		ArtifactImage:  s.artifactImageResolver(finding.ID),
	})
	name := fmt.Sprintf("ghoul-finding-%d-%s.docx", finding.ID, time.Now().Format("20060102"))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	_ = doc.Encode(w)
}

func taskID(task *Task) int64 {
	if task == nil {
		return 0
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	return id
}
