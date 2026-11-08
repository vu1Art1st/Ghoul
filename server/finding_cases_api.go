package server

import "net/http"

func (s *Server) listFindingCases(w http.ResponseWriter, r *http.Request) {
	taskID := int64(atoiDefault(r.URL.Query().Get("task"), 0))
	page := findingPaginationParam(r.URL.Query().Get("page"), 1, 0)
	size := findingPaginationParam(r.URL.Query().Get("limit"), 20, 100)
	items, total, err := s.m.pg.ListFindingCases(r.Context(), taskID, page, size)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) mergeFindingCase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID     int64   `json:"task_id"`
		FindingIDs []int64 `json:"finding_ids"`
		Title      string  `json:"title"`
		Reason     string  `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	caseID, err := s.m.pg.MergeFindingCase(r.Context(), req.TaskID, req.FindingIDs, req.Title, req.Reason, "human")
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	item, err := s.m.pg.GetFindingCase(r.Context(), caseID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, item)
}

func (s *Server) getFindingCase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad case id")
		return
	}
	item, err := s.m.pg.GetFindingCase(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if item == nil {
		writeErr(w, 404, "finding case not found")
		return
	}
	members, err := s.m.pg.FindingCaseMembers(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	events, err := s.m.pg.FindingCaseEvents(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"case": item, "members": members, "events": events})
}

func (s *Server) updateFindingCase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad case id")
		return
	}
	var req struct {
		Version  int64  `json:"version"`
		Title    string `json:"title"`
		Report   string `json:"report"`
		Severity string `json:"severity"`
		Reason   string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.m.pg.UpdateFindingCaseReport(r.Context(), id, req.Version, req.Title, req.Report, req.Severity, req.Reason); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	item, _ := s.m.pg.GetFindingCase(r.Context(), id)
	writeJSON(w, 200, item)
}

func (s *Server) detachFindingCaseMember(w http.ResponseWriter, r *http.Request) {
	caseID, ok := pathInt(r, "id")
	findingID, okFinding := pathInt(r, "findingId")
	if !ok || !okFinding || caseID <= 0 || findingID <= 0 {
		writeErr(w, 400, "bad case or finding id")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.m.pg.DetachFindingCaseMember(r.Context(), caseID, findingID, "human", req.Reason); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"detached": true})
}
