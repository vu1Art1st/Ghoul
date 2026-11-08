package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/findingverify"
)

func (s *Server) findingEvidenceTimeline(w http.ResponseWriter, r *http.Request) {
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
	generic, err := s.m.pg.ListFindingEvidence(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	traffic, err := s.m.pg.GetFindingTraffic(r.Context(), id)
	if err != nil && !errors.Is(err, db.ErrFindingNotFound) {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"finding_id":       id,
		"version":          finding.EvidenceVersion,
		"report_version":   finding.ReportEvidenceVersion,
		"generic":          generic,
		"traffic":          traffic,
		"verification":     finding.Verification,
		"counter_evidence": finding.CounterEvidence,
		"pivot_hints":      finding.PivotHints,
	})
}

func (s *Server) addFindingEvidence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	var req struct {
		Kind            string          `json:"kind"`
		Title           string          `json:"title"`
		Content         string          `json:"content"`
		StorageRef      string          `json:"storage_ref"`
		Metadata        json.RawMessage `json:"metadata"`
		ExpectedVersion *int64          `json:"expected_version"`
		Reason          string          `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	item, version, err := s.m.pg.AddFindingEvidence(r.Context(), id, req.ExpectedVersion, "human", req.Reason, db.FindingEvidence{
		Kind: req.Kind, Title: req.Title, Content: req.Content, StorageRef: req.StorageRef, MetadataJSON: req.Metadata,
	})
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"evidence": item, "version": version})
}

func (s *Server) deleteFindingEvidence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	evidenceID, okEvidence := pathInt(r, "evidenceId")
	if !ok || !okEvidence || id <= 0 || evidenceID <= 0 {
		writeErr(w, 400, "bad finding or evidence id")
		return
	}
	var req struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	version, err := s.m.pg.DeleteFindingEvidence(r.Context(), id, evidenceID, req.ExpectedVersion, "human", req.Reason)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true, "version": version})
}

func (s *Server) verifyFinding(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	var req struct {
		Verdict         string   `json:"verdict"`
		Summary         string   `json:"summary"`
		Evidence        string   `json:"evidence"`
		CounterEvidence []string `json:"counter_evidence"`
		Tags            []string `json:"tags"`
		ExpectedVersion *int64   `json:"expected_version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
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
	if req.Verdict == "verified" {
		if _, correction := agent.EvaluateFindingEvidence(finding.VulnClass, finding.Severity, req.Evidence); correction != "" {
			writeErr(w, 422, correction)
			return
		}
	}
	version := finding.EvidenceVersion
	if req.ExpectedVersion != nil {
		version = *req.ExpectedVersion
	}
	next, err := s.m.pg.SetFindingVerification(r.Context(), id, version, db.FindingVerification{
		Verdict: req.Verdict, Summary: req.Summary, Evidence: req.Evidence,
		CounterEvidence: req.CounterEvidence, PivotHints: findingverify.PivotHints(req.Tags, finding.Summary, 4),
		Actor: "human",
	})
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"verified":    true,
		"version":     next,
		"checklist":   findingverify.Checklist(finding.VulnClass),
		"pivot_hints": findingverify.PivotHints(req.Tags, finding.Summary, 4),
	})
}

func (s *Server) dispatchFinding(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	var req struct {
		AgentKey    string `json:"agent_key"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
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
	if finding.TaskID == nil || finding.NodeID == nil {
		writeErr(w, 409, "finding origin task or node is no longer available")
		return
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = "基于该 Finding 的现有证据继续验证并推进利用"
	}
	task, ok := s.m.Task(i64s(*finding.TaskID))
	if !ok || task == nil {
		writeErr(w, 409, "finding origin task is no longer available")
		return
	}
	if !s.engine.beginTaskOperation(task.ID) {
		writeErr(w, 409, "task is being deleted")
		return
	}
	defer s.engine.decInflight(task.ID)
	audit := db.Activity{Worker: "system", Kind: "text", Summary: "人工派发 Finding 继续处理", Detail: description}
	intentID, audit, err := task.Store.AddFindingFollowUpIntent(id, *finding.NodeID, description, audit)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	queued, err := s.admitTask(task, "resume")
	if err != nil {
		_ = task.Store.DiscardOpenIntent(intentID)
		writeErr(w, 500, err.Error())
		return
	}
	dispatch, err := s.m.pg.CreateFindingDispatch(r.Context(), id, req.AgentKey, description, "human")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.engine.Broadcaster().Publish(task.ID, audit)
	s.engine.touch(task.ID)
	writeJSON(w, 202, map[string]any{"dispatch": dispatch, "intent_id": i64s(intentID), "queued": queued})
}
