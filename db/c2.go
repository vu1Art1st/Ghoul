package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type C2Instance struct {
	ID           int64           `json:"id,string"`
	Name         string          `json:"name"`
	BaseURL      string          `json:"base_url"`
	MCPURL       string          `json:"mcp_url,omitempty"`
	Version      string          `json:"version,omitempty"`
	Commit       string          `json:"commit,omitempty"`
	APIKeyRef    string          `json:"api_key_ref,omitempty"`
	Enabled      bool            `json:"enabled"`
	Status       string          `json:"status"`
	LastHealthAt *time.Time      `json:"last_health_at,omitempty"`
	LastSyncAt   *time.Time      `json:"last_sync_at,omitempty"`
	Health       json.RawMessage `json:"health"`
	Channels     json.RawMessage `json:"channels"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type C2Session struct {
	ID                int64             `json:"id,string"`
	InstanceID        int64             `json:"instance_id,string"`
	ExternalSessionID string            `json:"session_id"`
	Hostname          string            `json:"hostname"`
	Username          string            `json:"username"`
	OS                string            `json:"os"`
	Arch              string            `json:"arch"`
	RemoteAddr        string            `json:"remote_addr"`
	ListenerID        string            `json:"listener_id"`
	Status            string            `json:"status"`
	Capabilities      json.RawMessage   `json:"capabilities"`
	LastSeenAt        *time.Time        `json:"last_seen_at,omitempty"`
	RawHash           string            `json:"raw_hash"`
	Raw               json.RawMessage   `json:"raw"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	Binding           *C2SessionBinding `json:"binding,omitempty"`
}

type C2SessionBinding struct {
	ID            int64      `json:"id,string"`
	C2SessionID   int64      `json:"c2_session_id,string"`
	TaskID        int64      `json:"task_id,string"`
	AssetID       *int64     `json:"asset_id,string,omitempty"`
	CompanyID     *int64     `json:"company_id,string,omitempty"`
	BindingReason string     `json:"binding_reason"`
	BoundBy       string     `json:"bound_by"`
	BoundAt       time.Time  `json:"bound_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	Active        bool       `json:"active"`
}

type C2SessionFilter struct {
	TaskID    int64
	Status    string
	BoundOnly bool
	Limit     int
}

type C2Task struct {
	ID                int64           `json:"id,string"`
	InstanceID        int64           `json:"instance_id,string"`
	ExternalTaskID    uint64          `json:"task_id"`
	ExternalSessionID string          `json:"session_id"`
	TaskType          string          `json:"task_type"`
	Command           string          `json:"command"`
	Status            string          `json:"status"`
	ExitCode          int32           `json:"exit_code"`
	OutputSummary     string          `json:"output_summary"`
	OutputSHA256      string          `json:"output_sha256"`
	CreatedAtExt      *time.Time      `json:"created_at_ext,omitempty"`
	CompletedAt       *time.Time      `json:"completed_at,omitempty"`
	Raw               json.RawMessage `json:"raw"`
	SyncedAt          time.Time       `json:"synced_at"`
}

type C2Listener struct {
	ID                 int64           `json:"id,string"`
	InstanceID         int64           `json:"instance_id,string"`
	ExternalListenerID string          `json:"listener_id"`
	Name               string          `json:"name"`
	Type               string          `json:"type"`
	Protocol           string          `json:"protocol"`
	BindAddr           string          `json:"bind_addr"`
	BindPort           int             `json:"bind_port"`
	PublicAddr         string          `json:"public_addr"`
	Status             string          `json:"status"`
	Connections        int             `json:"connections"`
	Raw                json.RawMessage `json:"raw"`
	SyncedAt           time.Time       `json:"synced_at"`
}

type C2Event struct {
	ID         int64           `json:"id,string"`
	InstanceID int64           `json:"instance_id,string"`
	EventKey   string          `json:"event_key"`
	EventType  string          `json:"type"`
	EventTime  *time.Time      `json:"time,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type C2EventCursor struct {
	InstanceID   int64      `json:"instance_id,string"`
	LastEventID  string     `json:"last_event_id"`
	LastEventAt  *time.Time `json:"last_event_at,omitempty"`
	LastResyncAt *time.Time `json:"last_resync_at,omitempty"`
	Status       string     `json:"status"`
}

func (d *DB) UpsertC2Instance(ctx context.Context, in C2Instance) (int64, error) {
	if strings.TrimSpace(in.BaseURL) == "" {
		return 0, errors.New("C2 base_url is required")
	}
	if in.Name == "" {
		in.Name = "toshell"
	}
	if in.Status == "" {
		in.Status = "unknown"
	}
	if len(in.Health) == 0 {
		in.Health = json.RawMessage(`{}`)
	}
	if len(in.Channels) == 0 {
		in.Channels = json.RawMessage(`{}`)
	}
	var id int64
	err := d.QueryRowContext(ctx, `INSERT INTO c2_instances
		(name,base_url,mcp_url,version,commit,api_key_ref,enabled,status,health_json,channels_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT(base_url) DO UPDATE SET
		  name=EXCLUDED.name,mcp_url=EXCLUDED.mcp_url,version=EXCLUDED.version,
		  commit=EXCLUDED.commit,api_key_ref=EXCLUDED.api_key_ref,
		  enabled=EXCLUDED.enabled,status=EXCLUDED.status,updated_at=now()
		RETURNING id`,
		in.Name, in.BaseURL, in.MCPURL, in.Version, in.Commit, in.APIKeyRef,
		in.Enabled, in.Status, string(in.Health), string(in.Channels)).Scan(&id)
	return id, err
}

func (d *DB) UpdateC2InstanceHealth(ctx context.Context, id int64, status string, health, channels json.RawMessage) error {
	if len(health) == 0 {
		health = json.RawMessage(`{}`)
	}
	if len(channels) == 0 {
		channels = json.RawMessage(`{}`)
	}
	_, err := d.ExecContext(ctx, `UPDATE c2_instances
		SET status=$2,health_json=$3,channels_json=$4,last_health_at=now(),last_sync_at=now(),updated_at=now()
		WHERE id=$1`, id, status, string(health), string(channels))
	return err
}

func (d *DB) ListC2Instances(ctx context.Context) ([]C2Instance, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,name,base_url,mcp_url,version,commit,api_key_ref,
		enabled,status,last_health_at,last_sync_at,health_json,channels_json,created_at,updated_at
		FROM c2_instances ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2Instance{}
	for rows.Next() {
		var item C2Instance
		var health, channels []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &item.MCPURL, &item.Version,
			&item.Commit, &item.APIKeyRef, &item.Enabled, &item.Status, &item.LastHealthAt,
			&item.LastSyncAt, &health, &channels, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Health, item.Channels = health, channels
		out = append(out, item)
	}
	return out, rows.Err()
}

type C2SessionInput struct {
	ExternalSessionID string
	Hostname          string
	Username          string
	OS                string
	Arch              string
	RemoteAddr        string
	ListenerID        string
	Status            string
	Capabilities      []string
	LastSeen          time.Time
	Raw               json.RawMessage
}

func C2RawHash(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (d *DB) UpsertC2Session(ctx context.Context, instanceID int64, in C2SessionInput) (int64, error) {
	if instanceID <= 0 || strings.TrimSpace(in.ExternalSessionID) == "" {
		return 0, errors.New("instance_id and external session id are required")
	}
	capabilities, _ := json.Marshal(in.Capabilities)
	raw := in.Raw
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var lastSeen any
	if !in.LastSeen.IsZero() {
		lastSeen = in.LastSeen
	}
	var id int64
	err := d.QueryRowContext(ctx, `INSERT INTO c2_sessions
		(instance_id,external_session_id,hostname,username,os,arch,remote_addr,listener_id,
		 status,capabilities_json,last_seen_at,raw_hash,raw_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT(instance_id,external_session_id) DO UPDATE SET
		  hostname=EXCLUDED.hostname,username=EXCLUDED.username,os=EXCLUDED.os,arch=EXCLUDED.arch,
		  remote_addr=EXCLUDED.remote_addr,listener_id=EXCLUDED.listener_id,status=EXCLUDED.status,
		  capabilities_json=EXCLUDED.capabilities_json,last_seen_at=EXCLUDED.last_seen_at,
		  raw_hash=EXCLUDED.raw_hash,raw_json=EXCLUDED.raw_json,updated_at=now()
		RETURNING id`,
		instanceID, in.ExternalSessionID, in.Hostname, in.Username, in.OS, in.Arch,
		in.RemoteAddr, in.ListenerID, in.Status, string(capabilities), lastSeen,
		C2RawHash(raw), string(raw)).Scan(&id)
	return id, err
}

func (d *DB) ListC2Sessions(ctx context.Context, filter C2SessionFilter) ([]C2Session, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []any{}
	conds := []string{}
	if filter.Status != "" {
		args = append(args, filter.Status)
		conds = append(conds, fmt.Sprintf("s.status=$%d", len(args)))
	}
	join := `LEFT JOIN c2_session_bindings b ON b.c2_session_id=s.id AND b.active`
	if filter.TaskID > 0 {
		args = append(args, filter.TaskID)
		join += fmt.Sprintf(" AND b.task_id=$%d", len(args))
	}
	if filter.BoundOnly || filter.TaskID > 0 {
		conds = append(conds, "b.id IS NOT NULL")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit)
	rows, err := d.QueryContext(ctx, fmt.Sprintf(`SELECT s.id,s.instance_id,s.external_session_id,
		s.hostname,s.username,s.os,s.arch,s.remote_addr,s.listener_id,s.status,s.capabilities_json,
		s.last_seen_at,s.raw_hash,s.raw_json,s.created_at,s.updated_at,
		b.id,b.task_id,b.asset_id,b.company_id,b.binding_reason,b.bound_by,b.bound_at,b.expires_at,b.active
		FROM c2_sessions s %s%s ORDER BY s.last_seen_at DESC NULLS LAST,s.id DESC LIMIT $%d`,
		join, where, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2Session{}
	for rows.Next() {
		var item C2Session
		var caps, raw []byte
		var bid, taskID, assetID, companyID sql.NullInt64
		var reason, boundBy sql.NullString
		var boundAt, expiresAt sql.NullTime
		var active sql.NullBool
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.ExternalSessionID, &item.Hostname,
			&item.Username, &item.OS, &item.Arch, &item.RemoteAddr, &item.ListenerID, &item.Status,
			&caps, &item.LastSeenAt, &item.RawHash, &raw, &item.CreatedAt, &item.UpdatedAt,
			&bid, &taskID, &assetID, &companyID, &reason, &boundBy, &boundAt, &expiresAt, &active); err != nil {
			return nil, err
		}
		item.Capabilities, item.Raw = caps, raw
		if bid.Valid {
			binding := &C2SessionBinding{ID: bid.Int64, C2SessionID: item.ID, TaskID: taskID.Int64,
				BindingReason: reason.String, BoundBy: boundBy.String, Active: active.Bool}
			if assetID.Valid {
				v := assetID.Int64
				binding.AssetID = &v
			}
			if companyID.Valid {
				v := companyID.Int64
				binding.CompanyID = &v
			}
			if boundAt.Valid {
				binding.BoundAt = boundAt.Time
			}
			if expiresAt.Valid {
				v := expiresAt.Time
				binding.ExpiresAt = &v
			}
			item.Binding = binding
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *DB) GetC2SessionByExternalID(ctx context.Context, instanceID int64, externalID string) (*C2Session, error) {
	items, err := d.ListC2Sessions(ctx, C2SessionFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].InstanceID == instanceID && items[i].ExternalSessionID == externalID {
			return &items[i], nil
		}
	}
	return nil, nil
}

func (d *DB) UpdateC2SessionStatus(ctx context.Context, instanceID int64, externalID, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE c2_sessions SET status=$3,updated_at=now()
		WHERE instance_id=$1 AND external_session_id=$2`, instanceID, externalID, status)
	return err
}

func (d *DB) HasActiveC2Binding(ctx context.Context, taskID int64) (bool, error) {
	if taskID <= 0 {
		return false, nil
	}
	var ok bool
	err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM c2_session_bindings WHERE task_id=$1 AND active)`, taskID).Scan(&ok)
	return ok, err
}

func (d *DB) BindC2Session(ctx context.Context, instanceID int64, externalSessionID string, taskID, assetID, companyID int64, reason, actor string) (*C2SessionBinding, error) {
	if taskID <= 0 || strings.TrimSpace(reason) == "" {
		return nil, errors.New("task_id and binding reason are required")
	}
	var sessionID int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM c2_sessions WHERE instance_id=$1 AND external_session_id=$2`,
		instanceID, externalSessionID).Scan(&sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("C2 session not found")
		}
		return nil, err
	}
	var taskExists int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM tasks WHERE id=$1`, taskID).Scan(&taskExists); err != nil {
		return nil, errors.New("Ghoul task not found")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE c2_session_bindings SET active=false WHERE c2_session_id=$1 AND active`, sessionID); err != nil {
		return nil, err
	}
	var asset, company any
	if assetID > 0 {
		asset = assetID
	}
	if companyID > 0 {
		company = companyID
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO c2_session_bindings
		(c2_session_id,task_id,asset_id,company_id,binding_reason,bound_by,active)
		VALUES($1,$2,$3,$4,$5,$6,true) RETURNING id`,
		sessionID, taskID, asset, company, strings.TrimSpace(reason), actor).Scan(&id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &C2SessionBinding{ID: id, C2SessionID: sessionID, TaskID: taskID, AssetID: optionalID(assetID),
		CompanyID: optionalID(companyID), BindingReason: strings.TrimSpace(reason), BoundBy: actor, Active: true}, nil
}

func optionalID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

func (d *DB) UnbindC2Session(ctx context.Context, instanceID int64, externalSessionID, actor string) error {
	res, err := d.ExecContext(ctx, `UPDATE c2_session_bindings b SET active=false
		FROM c2_sessions s
		WHERE b.c2_session_id=s.id AND s.instance_id=$1 AND s.external_session_id=$2 AND b.active`,
		instanceID, externalSessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("active C2 session binding not found")
	}
	_, _ = d.ExecContext(ctx, `UPDATE c2_sessions SET updated_at=now() WHERE instance_id=$1 AND external_session_id=$2`, instanceID, externalSessionID)
	_ = actor
	return nil
}

type C2TaskInput struct {
	ExternalTaskID    uint64
	ExternalSessionID string
	TaskType          string
	Command           string
	Status            string
	ExitCode          int32
	Output            string
	CreatedAt         time.Time
	CompletedAt       *time.Time
	Raw               json.RawMessage
}

func (d *DB) UpsertC2Task(ctx context.Context, instanceID int64, in C2TaskInput) error {
	raw := in.Raw
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	summary := in.Output
	if len([]rune(summary)) > 2000 {
		summary = string([]rune(summary)[:2000])
	}
	sum := sha256.Sum256([]byte(in.Output))
	var created, completed any
	if !in.CreatedAt.IsZero() {
		created = in.CreatedAt
	}
	if in.CompletedAt != nil {
		completed = *in.CompletedAt
	}
	_, err := d.ExecContext(ctx, `INSERT INTO c2_tasks
		(instance_id,external_task_id,external_session_id,task_type,command,status,exit_code,
		 output_summary,output_sha256,created_at_ext,completed_at,raw_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(instance_id,external_task_id) DO UPDATE SET
		  external_session_id=EXCLUDED.external_session_id,task_type=EXCLUDED.task_type,
		  command=EXCLUDED.command,status=EXCLUDED.status,exit_code=EXCLUDED.exit_code,
		  output_summary=EXCLUDED.output_summary,output_sha256=EXCLUDED.output_sha256,
		  created_at_ext=EXCLUDED.created_at_ext,completed_at=EXCLUDED.completed_at,
		  raw_json=EXCLUDED.raw_json,synced_at=now()`,
		instanceID, in.ExternalTaskID, in.ExternalSessionID, in.TaskType, in.Command, in.Status,
		in.ExitCode, summary, hex.EncodeToString(sum[:]), created, completed, string(raw))
	return err
}

func (d *DB) ListC2Tasks(ctx context.Context, instanceID int64, sessionID string, limit int) ([]C2Task, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := d.QueryContext(ctx, `SELECT id,instance_id,external_task_id,external_session_id,
		task_type,command,status,exit_code,output_summary,output_sha256,created_at_ext,completed_at,raw_json,synced_at
		FROM c2_tasks WHERE instance_id=$1 AND ($2='' OR external_session_id=$2)
		ORDER BY created_at_ext DESC NULLS LAST,id DESC LIMIT $3`, instanceID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2Task{}
	for rows.Next() {
		var item C2Task
		var raw []byte
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.ExternalTaskID, &item.ExternalSessionID,
			&item.TaskType, &item.Command, &item.Status, &item.ExitCode, &item.OutputSummary,
			&item.OutputSHA256, &item.CreatedAtExt, &item.CompletedAt, &raw, &item.SyncedAt); err != nil {
			return nil, err
		}
		item.Raw = raw
		out = append(out, item)
	}
	return out, rows.Err()
}

type C2ListenerInput struct {
	ExternalListenerID string
	Name               string
	Type               string
	Protocol           string
	BindAddr           string
	BindPort           int
	PublicAddr         string
	Status             string
	Connections        int
	Raw                json.RawMessage
}

func (d *DB) UpsertC2Listener(ctx context.Context, instanceID int64, in C2ListenerInput) error {
	raw := in.Raw
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	_, err := d.ExecContext(ctx, `INSERT INTO c2_listeners
		(instance_id,external_listener_id,name,type,protocol,bind_addr,bind_port,public_addr,status,connections,raw_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(instance_id,external_listener_id) DO UPDATE SET
		  name=EXCLUDED.name,type=EXCLUDED.type,protocol=EXCLUDED.protocol,
		  bind_addr=EXCLUDED.bind_addr,bind_port=EXCLUDED.bind_port,public_addr=EXCLUDED.public_addr,
		  status=EXCLUDED.status,connections=EXCLUDED.connections,raw_json=EXCLUDED.raw_json,synced_at=now()`,
		instanceID, in.ExternalListenerID, in.Name, in.Type, in.Protocol, in.BindAddr,
		in.BindPort, in.PublicAddr, in.Status, in.Connections, string(raw))
	return err
}

func (d *DB) ListC2Listeners(ctx context.Context, instanceID int64) ([]C2Listener, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,instance_id,external_listener_id,name,type,protocol,
		bind_addr,bind_port,public_addr,status,connections,raw_json,synced_at
		FROM c2_listeners WHERE instance_id=$1 ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2Listener{}
	for rows.Next() {
		var item C2Listener
		var raw []byte
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.ExternalListenerID, &item.Name,
			&item.Type, &item.Protocol, &item.BindAddr, &item.BindPort, &item.PublicAddr,
			&item.Status, &item.Connections, &raw, &item.SyncedAt); err != nil {
			return nil, err
		}
		item.Raw = raw
		out = append(out, item)
	}
	return out, rows.Err()
}

func C2EventKey(eventType string, eventTime int64, payload json.RawMessage) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", eventType, eventTime, payload)))
	return hex.EncodeToString(sum[:16])
}

func (d *DB) AppendC2Event(ctx context.Context, instanceID int64, eventType string, eventTime time.Time, payload json.RawMessage) (*C2Event, error) {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	key := C2EventKey(eventType, eventTime.Unix(), payload)
	var item C2Event
	var raw []byte
	var eventAt sql.NullTime
	err := d.QueryRowContext(ctx, `INSERT INTO c2_events(instance_id,event_key,event_type,event_time,payload)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(instance_id,event_key) DO UPDATE SET payload=EXCLUDED.payload
		RETURNING id,instance_id,event_key,event_type,event_time,payload,created_at`,
		instanceID, key, eventType, eventTime, string(payload)).Scan(&item.ID, &item.InstanceID,
		&item.EventKey, &item.EventType, &eventAt, &raw, &item.CreatedAt)
	if err != nil {
		return nil, err
	}
	if eventAt.Valid {
		item.EventTime = &eventAt.Time
	}
	item.Payload = raw
	return &item, nil
}

func (d *DB) ListC2Events(ctx context.Context, instanceID int64, limit int) ([]C2Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := d.QueryContext(ctx, `SELECT id,instance_id,event_key,event_type,event_time,payload,created_at
		FROM c2_events WHERE instance_id=$1 ORDER BY event_time DESC NULLS LAST,id DESC LIMIT $2`, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []C2Event{}
	for rows.Next() {
		var item C2Event
		var eventAt sql.NullTime
		var raw []byte
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.EventKey, &item.EventType,
			&eventAt, &raw, &item.CreatedAt); err != nil {
			return nil, err
		}
		if eventAt.Valid {
			item.EventTime = &eventAt.Time
		}
		item.Payload = raw
		out = append(out, item)
	}
	return out, rows.Err()
}

func (d *DB) SetC2Cursor(ctx context.Context, instanceID int64, lastEventID string, lastEventAt time.Time, resync bool, status string) error {
	var resyncAt any
	if resync {
		resyncAt = time.Now().UTC()
	}
	var eventAt any
	if !lastEventAt.IsZero() {
		eventAt = lastEventAt
	}
	_, err := d.ExecContext(ctx, `INSERT INTO c2_event_cursor(instance_id,last_event_id,last_event_at,last_resync_at,status)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(instance_id) DO UPDATE SET
		  last_event_id=COALESCE(NULLIF(EXCLUDED.last_event_id,''),c2_event_cursor.last_event_id),
		  last_event_at=COALESCE(EXCLUDED.last_event_at,c2_event_cursor.last_event_at),
		  last_resync_at=COALESCE(EXCLUDED.last_resync_at,c2_event_cursor.last_resync_at),
		  status=EXCLUDED.status`, instanceID, lastEventID, eventAt, resyncAt, status)
	return err
}

func (d *DB) GetC2Cursor(ctx context.Context, instanceID int64) (*C2EventCursor, error) {
	var out C2EventCursor
	err := d.QueryRowContext(ctx, `SELECT instance_id,last_event_id,last_event_at,last_resync_at,status
		FROM c2_event_cursor WHERE instance_id=$1`, instanceID).
		Scan(&out.InstanceID, &out.LastEventID, &out.LastEventAt, &out.LastResyncAt, &out.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return &C2EventCursor{InstanceID: instanceID, Status: "disconnected"}, nil
	}
	return &out, err
}
