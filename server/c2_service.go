package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/db"
	toshellclient "github.com/Autumn-27/artex/integrations/toshell/client"
	"github.com/Autumn-27/artex/integrations/toshell/sanitize"
	"github.com/gorilla/websocket"
)

type c2Service struct {
	pg     *db.DB
	cfg    config.C2Config
	client *toshellclient.Client

	instanceID  int64
	secret      []byte
	mu          sync.Mutex
	syncError   string
	streamError string
	lastSync    time.Time
	rateMu      sync.Mutex
	rate        map[int64][]time.Time
}

const c2FailureThreshold = 3

func newC2Service(pg *db.DB, secret []byte) *c2Service {
	cfg := config.Load().EffectiveC2()
	if !cfg.Enabled {
		return nil
	}
	return &c2Service{pg: pg, cfg: cfg, secret: append([]byte(nil), secret...), rate: map[int64][]time.Time{}}
}

func (s *c2Service) start(ctx context.Context) {
	if s == nil {
		return
	}
	version := strings.TrimSpace(os.Getenv("TOSHELL_VERSION"))
	if version == "" {
		version = "v1.4.3"
	}
	commit := strings.TrimSpace(os.Getenv("TOSHELL_COMMIT"))
	if commit == "" {
		commit = "8bf4643c1affbe109993123475422f6d26897903"
	}
	instanceID, err := s.pg.UpsertC2Instance(ctx, db.C2Instance{
		Name: "toshell", BaseURL: s.cfg.BaseURL, APIKeyRef: s.cfg.APIKeyRef,
		Version: version, Commit: commit,
		Enabled: true, Status: "connecting",
	})
	if err != nil {
		s.setSyncError(err)
		log.Printf("[c2] register instance: %v", err)
		return
	}
	s.instanceID = instanceID
	client, err := toshellclient.New(toshellclient.Config{
		BaseURL: s.cfg.BaseURL, APIKeyRef: s.cfg.APIKeyRef,
		AllowedHosts: s.cfg.AllowedHosts, AllowInsecureHTTP: s.cfg.AllowInsecureHTTP,
		TLSCAFile: s.cfg.TLSCAFile,
	})
	if err != nil {
		s.setSyncError(err)
		_ = s.pg.UpdateC2InstanceHealth(ctx, instanceID, "error", json.RawMessage(`{}`), json.RawMessage(`{}`))
		log.Printf("[c2] init client: %v", err)
		return
	}
	s.client = client
	go s.run(ctx)
}

func (s *c2Service) run(ctx context.Context) {
	go s.wsLoop(ctx)
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}
	syncFailures := 0
	if err := s.syncOnce(ctx); err != nil {
		syncFailures++
		s.recordSyncFailure(ctx, err, syncFailures)
		log.Printf("[c2] initial sync: %v", err)
	} else {
		syncFailures = 0
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.syncOnce(ctx); err != nil {
				syncFailures++
				s.recordSyncFailure(ctx, err, syncFailures)
				log.Printf("[c2] reconcile: %v", err)
			} else {
				syncFailures = 0
			}
		}
	}
}

func (s *c2Service) syncOnce(ctx context.Context) error {
	if s == nil || s.client == nil || s.instanceID <= 0 {
		return toshellclient.ErrDisabled
	}
	qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	health, err := s.client.Health(qctx)
	if err != nil {
		return err
	}
	channels, err := s.client.ChannelsHealth(qctx)
	if err != nil {
		return err
	}
	sessions, err := s.client.Sessions(qctx)
	if err != nil {
		return err
	}
	tasks, err := s.client.Tasks(qctx, "")
	if err != nil {
		return err
	}
	listeners, err := s.client.Listeners(qctx)
	if err != nil {
		return err
	}
	healthRaw, _ := json.Marshal(health)
	channelsRaw, _ := json.Marshal(channels)
	if err := s.pg.UpdateC2InstanceHealth(qctx, s.instanceID, "healthy", healthRaw, channelsRaw); err != nil {
		return err
	}
	for _, session := range sessions {
		raw, _ := json.Marshal(session)
		if _, err := s.pg.UpsertC2Session(qctx, s.instanceID, db.C2SessionInput{
			ExternalSessionID: session.ID, Hostname: session.Hostname, Username: session.Username,
			OS: session.OS, Arch: session.Arch, RemoteAddr: session.RemoteAddr,
			ListenerID: session.ListenerID, Status: session.Status,
			Capabilities: session.ActiveModules, LastSeen: session.LastSeen, Raw: raw,
		}); err != nil {
			return err
		}
	}
	for _, task := range tasks {
		raw, _ := json.Marshal(task)
		if err := s.pg.UpsertC2Task(qctx, s.instanceID, db.C2TaskInput{
			ExternalTaskID: task.ID, ExternalSessionID: task.SessionID, TaskType: task.TaskType,
			Command: task.Command, Status: task.Status, ExitCode: task.ExitCode, Output: sanitize.Summary(task.Output, 2000),
			CreatedAt: task.CreatedAt, CompletedAt: task.CompletedAt, Raw: raw,
		}); err != nil {
			return err
		}
	}
	for _, listener := range listeners {
		raw, _ := json.Marshal(listener)
		if err := s.pg.UpsertC2Listener(qctx, s.instanceID, db.C2ListenerInput{
			ExternalListenerID: listener.ID, Name: listener.Name, Type: listener.Type,
			Protocol: listener.Protocol, BindAddr: listener.BindAddr, BindPort: listener.BindPort,
			PublicAddr: listener.PublicAddr, Status: listener.Status,
			Connections: listener.Connections, Raw: raw,
		}); err != nil {
			return err
		}
	}
	_ = s.pg.MarkC2Resync(qctx, s.instanceID)
	s.mu.Lock()
	s.syncError = ""
	s.lastSync = time.Now().UTC()
	s.mu.Unlock()
	return nil
}

func (s *c2Service) wsLoop(ctx context.Context) {
	backoff := 2 * time.Second
	failures := 0
	for {
		if ctx.Err() != nil || s.client == nil {
			return
		}
		connected, err := s.wsOnce(ctx)
		if connected {
			failures = 0
			backoff = 2 * time.Second
		}
		if err != nil {
			failures++
			s.recordStreamFailure(ctx, err, failures)
			log.Printf("[c2] event stream: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (s *c2Service) wsOnce(ctx context.Context) (bool, error) {
	wsURL, err := s.client.EventsURL()
	if err != nil {
		return false, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, s.client.AuthHeaders())
	if err != nil {
		return false, err
	}
	defer conn.Close()
	s.clearStreamError()
	_ = s.pg.SetC2Cursor(ctx, s.instanceID, "", time.Time{}, "connected")
	for {
		var event toshellclient.Event
		if err := conn.ReadJSON(&event); err != nil {
			return true, err
		}
		eventAt := time.Unix(event.Time, 0).UTC()
		if event.Time == 0 {
			eventAt = time.Now().UTC()
		}
		item, err := s.pg.AppendC2Event(ctx, s.instanceID, event.Type, eventAt, event.Payload)
		if err != nil {
			return true, err
		}
		s.applyEvent(ctx, event)
		_ = s.pg.SetC2Cursor(ctx, s.instanceID, item.EventKey, eventAt, "connected")
	}
}

func (s *c2Service) applyEvent(ctx context.Context, event toshellclient.Event) {
	switch event.Type {
	case "session_online":
		var session toshellclient.Session
		if json.Unmarshal(event.Payload, &session) == nil && session.ID != "" {
			raw, _ := json.Marshal(session)
			_, _ = s.pg.UpsertC2Session(ctx, s.instanceID, db.C2SessionInput{
				ExternalSessionID: session.ID, Hostname: session.Hostname, Username: session.Username,
				OS: session.OS, Arch: session.Arch, RemoteAddr: session.RemoteAddr,
				ListenerID: session.ListenerID, Status: session.Status,
				Capabilities: session.ActiveModules, LastSeen: session.LastSeen, Raw: raw,
			})
		}
	case "session_offline":
		var payload struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.ID != "" {
			_ = s.pg.UpdateC2SessionStatus(ctx, s.instanceID, payload.ID, "dead")
		}
	case "task_completed", "task_failed":
		var payload struct {
			TaskID    uint64 `json:"task_id"`
			SessionID string `json:"session_id"`
			TaskType  string `json:"task_type"`
			ExitCode  int32  `json:"exit_code"`
			Output    string `json:"output"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.TaskID > 0 {
			status := "completed"
			if event.Type == "task_failed" {
				status = "failed"
			}
			_ = s.pg.UpsertC2Task(ctx, s.instanceID, db.C2TaskInput{
				ExternalTaskID: payload.TaskID, ExternalSessionID: payload.SessionID,
				TaskType: payload.TaskType, Status: status, ExitCode: payload.ExitCode,
				Output: sanitize.Summary(firstNonEmpty(payload.Output, payload.Error), 2000), CreatedAt: time.Now().UTC(),
				Raw: event.Payload,
			})
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *c2Service) setSyncError(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.syncError = err.Error()
	s.mu.Unlock()
}

func (s *c2Service) setStreamError(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.streamError = err.Error()
	s.mu.Unlock()
}

func (s *c2Service) clearStreamError() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.streamError = ""
	s.mu.Unlock()
}

func (s *c2Service) recordSyncFailure(ctx context.Context, err error, failures int) {
	status := "reconnecting"
	if failures >= c2FailureThreshold {
		status = "error"
		s.setSyncError(err)
	} else {
		s.mu.Lock()
		s.syncError = ""
		s.mu.Unlock()
	}
	_ = s.pg.UpdateC2InstanceStatus(ctx, s.instanceID, status)
}

func (s *c2Service) recordStreamFailure(ctx context.Context, err error, failures int) {
	status := "reconnecting"
	if failures >= c2FailureThreshold {
		status = "error"
		s.setStreamError(err)
	} else {
		s.clearStreamError()
	}
	_ = s.pg.SetC2Cursor(context.Background(), s.instanceID, "", time.Time{}, status)
}

func (s *c2Service) state() (lastError string, lastSync time.Time) {
	if s == nil {
		return "", time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncError != "" {
		return s.syncError, s.lastSync
	}
	return s.streamError, s.lastSync
}

func (s *c2Service) enabled() bool { return s != nil && s.client != nil }

func (s *c2Service) instance() int64 { return s.instanceID }
