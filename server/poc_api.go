package server

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

func (s *Server) pocSearch(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.m.pg.SearchPocRecords(r.Context(), r.URL.Query().Get("q"), limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) pocCreate(w http.ResponseWriter, r *http.Request) {
	var in db.PocRecord
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	out, err := s.m.pg.CreatePocRecord(r.Context(), &in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, out)
}

func (s *Server) pocVerify(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Status     string `json:"status"`
		Confidence string `json:"confidence"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	out, err := s.m.pg.VerifyPocRecord(r.Context(), id, in.Status, in.Confidence)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if out == nil {
		writeErr(w, 404, "PoC 不存在")
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) pocMaterialize(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		TaskID  int64 `json:"task_id"`
		Version int   `json:"version"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.TaskID <= 0 {
		writeErr(w, 400, "缺少有效的任务 ID")
		return
	}
	if _, ok := s.m.Task(strconv.FormatInt(in.TaskID, 10)); !ok {
		writeErr(w, 404, "任务不存在")
		return
	}
	poc, err := s.m.pg.GetPocRecord(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if poc == nil {
		writeErr(w, 404, "PoC 不存在")
		return
	}
	if poc.ArtifactAssetID == nil {
		writeErr(w, 400, "该 PoC 没有关联共享资产")
		return
	}
	if in.Version <= 0 {
		in.Version = poc.ArtifactVersion
	}
	taskDir := filepath.Join(s.m.dir, "tasks", strconv.FormatInt(in.TaskID, 10))
	entry, err := s.shared.Materialize(r.Context(), *poc.ArtifactAssetID, in.Version, taskDir)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"entrypoint": entry})
}

func (s *Server) taskRelatedSuggestions(w http.ResponseWriter, r *http.Request) {
	taskID, _ := strconv.ParseInt(r.URL.Query().Get("task_id"), 10, 64)
	terms := []string{}
	for _, value := range []string{
		r.URL.Query().Get("q"),
		r.URL.Query().Get("target"),
		r.URL.Query().Get("product"),
		r.URL.Query().Get("component"),
	} {
		terms = append(terms, strings.Fields(value)...)
	}
	items, err := s.m.pg.SuggestTaskRelations(r.Context(), taskID, terms, 8)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
