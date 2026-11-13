package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

func (s *Server) taskReportAssets(taskID int64) (map[string]int, map[string][]string, map[int64]string, error) {
	ids, err := s.m.Assets().TaskAssetIDs(taskID)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(ids) == 0 {
		return map[string]int{}, map[string][]string{}, map[int64]string{}, nil
	}
	assets, err := s.m.Assets().GetByIDs(ids)
	if err != nil {
		return nil, nil, nil, err
	}
	sort.SliceStable(assets, func(i, j int) bool {
		if assets[i].Type != assets[j].Type {
			return assets[i].Type < assets[j].Type
		}
		return assets[i].ID < assets[j].ID
	})
	counts := map[string]int{}
	appendix := map[string][]string{}
	names := make(map[int64]string, len(assets))
	seenNames := map[string]map[string]bool{}
	for _, asset := range assets {
		name := reportAssetName(asset)
		if name == "" {
			continue
		}
		names[asset.ID] = name
		if seenNames[asset.Type] == nil {
			seenNames[asset.Type] = map[string]bool{}
		}
		if seenNames[asset.Type][name] {
			continue
		}
		seenNames[asset.Type][name] = true
		appendix[asset.Type] = append(appendix[asset.Type], name)
		counts[asset.Type]++
	}
	for typ := range appendix {
		sort.Strings(appendix[typ])
	}
	return counts, appendix, names, nil
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
// either inline as a data URL or in a managed storage_ref. A non-nil owner set
// prevents model-authored artifact references from escaping the findings
// represented by the current document.
func (s *Server) artifactImageResolver(allowedOwners map[int64]bool) func(int64) ([]byte, string, bool) {
	return func(id int64) ([]byte, string, bool) {
		var content, storageRef, title, kind string
		var owner int64
		err := s.m.pg.QueryRow(`SELECT finding_id,content,storage_ref,title,kind FROM finding_evidence WHERE id=$1`, id).
			Scan(&owner, &content, &storageRef, &title, &kind)
		if err != nil || owner <= 0 {
			return nil, "", false
		}
		if allowedOwners != nil && !allowedOwners[owner] {
			return nil, "", false
		}
		if !findingEvidenceIsImage(kind) {
			return nil, "", false
		}
		data, err := s.evidenceImageData(content, storageRef)
		if err != nil || len(data) == 0 {
			return nil, "", false
		}
		return data, title, true
	}
}

func (s *Server) evidenceImageData(content, storageRef string) ([]byte, error) {
	raw := strings.TrimSpace(content)
	if raw != "" {
		if comma := strings.IndexByte(raw, ','); strings.HasPrefix(raw, "data:") && comma >= 0 {
			raw = raw[comma+1:]
		}
		if data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw)); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	return s.readEvidenceStorageRef(storageRef)
}

func (s *Server) readEvidenceStorageRef(storageRef string) ([]byte, error) {
	ref := strings.TrimSpace(storageRef)
	if ref == "" {
		return nil, fmt.Errorf("empty storage_ref")
	}
	if strings.Contains(ref, "://") {
		return nil, fmt.Errorf("unsupported storage_ref scheme")
	}
	root, err := filepath.Abs(s.m.dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Clean(ref)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("storage_ref escapes evidence root")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > 32<<20 {
		return nil, fmt.Errorf("invalid evidence image size")
	}
	// Re-check after symlink resolution when the host permits it. Some managed
	// Windows temp/data roots deny EvalSymlinks even though the file itself is
	// readable, so fall back to the lexical boundary check above.
	if realRoot, rootErr := filepath.EvalSymlinks(root); rootErr == nil {
		if realPath, pathErr := filepath.EvalSymlinks(path); pathErr == nil {
			rel, relErr := filepath.Rel(realRoot, realPath)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("storage_ref resolves outside evidence root")
			}
			path = realPath
		}
	}
	return os.ReadFile(path)
}

func findingEvidenceIsImage(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.TrimPrefix(kind, "c2_")
	return kind == "screenshot" || kind == "image"
}

func reportReferencesArtifact(markdown string, id int64) bool {
	return strings.Contains(markdown, fmt.Sprintf("](artifact:%d)", id))
}

func (s *Server) attachEvidenceArtifacts(findings []*db.DBFinding) {
	for _, finding := range findings {
		items, err := s.m.pg.ListFindingEvidence(context.Background(), finding.ID)
		if err != nil || len(items) == 0 {
			continue
		}
		var text, reportRefs strings.Builder
		for _, item := range items {
			if findingEvidenceIsImage(item.Kind) {
				if (strings.TrimSpace(item.Content) != "" || strings.TrimSpace(item.StorageRef) != "") &&
					!reportReferencesArtifact(finding.Report, item.ID) {
					fmt.Fprintf(&reportRefs, "\n\n![%s](artifact:%d)", item.Title, item.ID)
				}
			} else {
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

func findingOwnerSet(findings []*db.DBFinding, cases []report.CaseView) map[int64]bool {
	owners := map[int64]bool{}
	for _, finding := range findings {
		if finding != nil && finding.ID > 0 {
			owners[finding.ID] = true
		}
	}
	for _, item := range cases {
		for _, member := range item.Members {
			if member != nil && member.ID > 0 {
				owners[member.ID] = true
			}
		}
	}
	return owners
}

func (s *Server) attachCaseEvidenceArtifacts(cases []report.CaseView) map[int64][]*db.FindingRetest {
	retests := map[int64][]*db.FindingRetest{}
	for i := range cases {
		var text, reportRefs strings.Builder
		for _, member := range cases[i].Members {
			items, err := s.m.pg.ListFindingEvidence(context.Background(), member.ID)
			if err == nil {
				for _, item := range items {
					if findingEvidenceIsImage(item.Kind) {
						if (strings.TrimSpace(item.Content) != "" || strings.TrimSpace(item.StorageRef) != "") &&
							!reportReferencesArtifact(cases[i].Case.Report, item.ID) {
							fmt.Fprintf(&reportRefs, "\n\n![%s](artifact:%d)", item.Title, item.ID)
						}
					} else if strings.TrimSpace(item.Content) != "" {
						fmt.Fprintf(&text, "\n\n[%s · Finding #%d]\n%s", item.Title, member.ID, item.Content)
					}
				}
			}
			if items, err := s.m.pg.ListFindingRetests(member.ID); err == nil && len(items) > 0 {
				retests[-cases[i].Case.ID] = append(retests[-cases[i].Case.ID], items...)
			}
		}
		if text.Len() > 0 {
			cases[i].Evidence += text.String()
		}
		if reportRefs.Len() > 0 {
			cases[i].Case.Report += reportRefs.String()
		}
	}
	return retests
}

func reportTaskTitle(task *Task) string {
	if task == nil {
		return "渗透测试报告"
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		return "渗透测试报告"
	}
	runes := []rune(name)
	if len(runes) > 40 {
		name = string(runes[:40]) + "…"
	}
	return "渗透测试报告 — " + name
}

func reportFindingSeverityCounts(findings []*db.DBFinding) map[string]int {
	counts := map[string]int{}
	for _, finding := range findings {
		counts[strings.ToLower(strings.TrimSpace(finding.Severity))]++
	}
	return counts
}

func reportExportWarnings(findings []*db.DBFinding, cases []report.CaseView) []string {
	seen := map[string]bool{}
	warnings := []string{}
	add := func(message string) {
		if message == "" || seen[message] {
			return
		}
		seen[message] = true
		warnings = append(warnings, message)
	}
	for _, finding := range findings {
		if finding == nil {
			continue
		}
		if strings.TrimSpace(finding.Report) == "" {
			add(fmt.Sprintf("Finding #%d 缺少 Markdown 报告。", finding.ID))
			continue
		}
		if finding.ReportEvidenceVersion != finding.EvidenceVersion {
			add(fmt.Sprintf("Finding #%d 的报告证据版本落后（报告 %d，当前 %d）。",
				finding.ID, finding.ReportEvidenceVersion, finding.EvidenceVersion))
		}
	}
	for _, item := range cases {
		if item.Case.ReportStale {
			add(fmt.Sprintf("漏洞组 #%d 的成员已变更，统一报告待重新生成。", item.Case.ID))
		}
		if strings.TrimSpace(item.Case.Report) == "" {
			add(fmt.Sprintf("漏洞组 #%d 缺少统一 Markdown 报告。", item.Case.ID))
		}
	}
	return warnings
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
	rawFindings := append([]*db.DBFinding(nil), findings...)
	taskNum, _ := strconv.ParseInt(task.ID, 10, 64)
	caseViews, caseMemberIDs, err := s.activeFindingCaseViews(r.Context(), taskNum)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(caseMemberIDs) > 0 {
		filtered := findings[:0]
		for _, finding := range findings {
			if !caseMemberIDs[finding.ID] {
				filtered = append(filtered, finding)
			}
		}
		findings = filtered
	}
	caseRetests := s.attachCaseEvidenceArtifacts(caseViews)
	owners := findingOwnerSet(findings, caseViews)
	findings = append(findings, findingCaseViewsAsDBFindings(caseViews)...)
	s.attachEvidenceArtifacts(findings)
	retests := s.reportRetests(findings)
	for findingID, items := range caseRetests {
		retests[findingID] = items
	}
	counts, appendix, assetNames, err := s.taskReportAssets(taskNum)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	title := reportTaskTitle(task)
	doc := report.BuildTaskDocx(report.TaskDocxInput{
		TaskID:         taskID(task),
		Title:          title,
		Goal:           task.Goal,
		GeneratedAt:    time.Now(),
		AssetCounts:    counts,
		Findings:       findings,
		FindingCount:   len(rawFindings),
		SeverityCounts: reportFindingSeverityCounts(rawFindings),
		CaseCount:      len(caseViews),
		AssetNames:     assetNames,
		Retests:        retests,
		Warnings:       reportExportWarnings(rawFindings, caseViews),
		Customer:       s.taskCustomerNames(task),
		TestWindow:     taskTestWindow(task),
		AppendixAssets: appendix,
		PageHeader:     title,
		ArtifactImage:  s.artifactImageResolver(owners),
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
		ArtifactImage:  s.artifactImageResolver(map[int64]bool{finding.ID: true}),
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
