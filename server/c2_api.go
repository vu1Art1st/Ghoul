package server

import (
	"net/http"
	"strings"

	"github.com/Autumn-27/artex/db"
)

func (s *Server) c2InstanceID(r *http.Request) int64 {
	if s.c2 != nil && s.c2.instance() > 0 {
		return s.c2.instance()
	}
	items, err := s.m.pg.ListC2Instances(r.Context())
	if err != nil || len(items) == 0 {
		return 0
	}
	return items[0].ID
}

func (s *Server) c2Status(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "status": "disabled"})
		return
	}
	instances, err := s.m.pg.ListC2Instances(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var instance *db.C2Instance
	if len(instances) > 0 {
		instance = &instances[0]
	}
	lastError, lastSync := s.c2.state()
	instanceID := s.c2InstanceID(r)
	cursor, _ := s.m.pg.GetC2Cursor(r.Context(), instanceID)
	sessions, _ := s.m.pg.ListC2Sessions(r.Context(), db.C2SessionFilter{Limit: 1000})
	listeners, _ := s.m.pg.ListC2Listeners(r.Context(), instanceID)
	tasks, _ := s.m.pg.ListC2Tasks(r.Context(), instanceID, "", 1000)
	writeJSON(w, 200, map[string]any{
		"enabled":      true,
		"status":       instanceStatus(instance),
		"instance":     instance,
		"cursor":       cursor,
		"last_error":   lastError,
		"last_sync_at": lastSync,
		"counts": map[string]int{
			"sessions": len(sessions), "listeners": len(listeners), "tasks": len(tasks),
		},
	})
}

func instanceStatus(instance *db.C2Instance) string {
	if instance == nil {
		return "unavailable"
	}
	if strings.TrimSpace(instance.Status) == "" {
		return "unknown"
	}
	return instance.Status
}

func (s *Server) c2Instances(w http.ResponseWriter, r *http.Request) {
	items, err := s.m.pg.ListC2Instances(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) c2Sessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	taskID := int64(atoiDefault(q.Get("task"), 0))
	status := strings.TrimSpace(q.Get("status"))
	bound := q.Get("bound") == "1" || q.Get("bound") == "true"
	limit := atoiDefault(q.Get("limit"), 200)
	items, err := s.m.pg.ListC2Sessions(r.Context(), db.C2SessionFilter{
		TaskID: taskID, Status: status, BoundOnly: bound, Limit: limit,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) c2BindSession(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	var req struct {
		TaskID    int64  `json:"task_id"`
		AssetID   int64  `json:"asset_id"`
		CompanyID int64  `json:"company_id"`
		Reason    string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	instanceID := s.c2InstanceID(r)
	if instanceID <= 0 {
		writeErr(w, 503, "C2 instance is not ready")
		return
	}
	externalID := strings.TrimSpace(r.PathValue("id"))
	binding, err := s.m.pg.BindC2Session(r.Context(), instanceID, externalID, req.TaskID, req.AssetID, req.CompanyID, req.Reason, "human")
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, binding)
}

func (s *Server) c2UnbindSession(w http.ResponseWriter, r *http.Request) {
	if s.c2 == nil {
		writeErr(w, 503, "C2 adapter is disabled")
		return
	}
	instanceID := s.c2InstanceID(r)
	externalID := strings.TrimSpace(r.PathValue("id"))
	if err := s.m.pg.UnbindC2Session(r.Context(), instanceID, externalID, "human"); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"unbound": true})
}

func (s *Server) c2Listeners(w http.ResponseWriter, r *http.Request) {
	instanceID := s.c2InstanceID(r)
	items, err := s.m.pg.ListC2Listeners(r.Context(), instanceID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) c2Tasks(w http.ResponseWriter, r *http.Request) {
	instanceID := s.c2InstanceID(r)
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	items, err := s.m.pg.ListC2Tasks(r.Context(), instanceID, sessionID, limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) c2Events(w http.ResponseWriter, r *http.Request) {
	instanceID := s.c2InstanceID(r)
	items, err := s.m.pg.ListC2Events(r.Context(), instanceID, atoiDefault(r.URL.Query().Get("limit"), 200))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}
