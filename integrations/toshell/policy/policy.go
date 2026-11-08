// Package policy defines the Ghoul-side action catalog and fail-closed checks
// for Toshell operations. Toshell remains the C2 source of truth; this package
// decides whether Ghoul may turn an agent or operator request into a Toshell
// action.
package policy

import (
	"errors"
	"fmt"
	"strings"
)

type Level string

const (
	LevelRead    Level = "read"
	LevelConfirm Level = "confirm"
	LevelDanger  Level = "danger"
)

var (
	ErrDisabled          = errors.New("C2 adapter is disabled")
	ErrConfirmDisabled   = errors.New("C2 confirm operations are disabled")
	ErrDangerDisabled    = errors.New("C2 danger operations are disabled")
	ErrTunnelDisabled    = errors.New("C2 tunnel operations are disabled")
	ErrUnknownAction     = errors.New("unknown C2 action")
	ErrTaskRequired      = errors.New("C2 operation requires a Ghoul task")
	ErrSessionRequired   = errors.New("C2 operation requires a bound session")
	ErrSessionMismatch   = errors.New("C2 session is not bound to this task")
	ErrSessionInactive   = errors.New("C2 session is not active")
	ErrScopeRequired     = errors.New("task has no authorized scope")
	ErrRateLimited       = errors.New("C2 operation rate limit exceeded")
	ErrApprovalRequired  = errors.New("C2 operation requires approval")
	ErrApprovalInvalid   = errors.New("C2 approval token is invalid")
	ErrApprovalExpired   = errors.New("C2 approval token expired")
	ErrApprovalConsumed  = errors.New("C2 approval token already consumed")
	ErrIdempotencyReused = errors.New("C2 idempotency key was reused with different parameters")
	ErrDangerRetry       = errors.New("danger operations must not be retried automatically")
)

type Config struct {
	Enabled       bool
	AllowConfirm  bool
	AllowDanger   bool
	AllowTunnel   bool
	AllowUnscoped bool
}

type Action struct {
	Name       string
	Level      Level
	SourceTool string
}

var actions = map[string]Action{
	"c2_session_list":    {Name: "c2_session_list", Level: LevelRead, SourceTool: "session_list"},
	"c2_session_context": {Name: "c2_session_context", Level: LevelRead, SourceTool: "session_context"},
	"c2_recon":           {Name: "c2_recon", Level: LevelRead, SourceTool: "exec"},
	"c2_file_list":       {Name: "c2_file_list", Level: LevelConfirm, SourceTool: "file_list"},
	"c2_process_list":    {Name: "c2_process_list", Level: LevelConfirm, SourceTool: "process_list"},
	"c2_screenshot":      {Name: "c2_screenshot", Level: LevelConfirm, SourceTool: "screenshot"},
	"c2_tunnel":          {Name: "c2_tunnel", Level: LevelConfirm, SourceTool: "tunnel"},
	"c2_exec":            {Name: "c2_exec", Level: LevelDanger, SourceTool: "exec"},
}

func Lookup(name string) (Action, bool) {
	action, ok := actions[strings.TrimSpace(name)]
	return action, ok
}

func Catalog() []Action {
	out := make([]Action, 0, len(actions))
	for _, name := range []string{
		"c2_session_list", "c2_session_context", "c2_recon", "c2_file_list",
		"c2_process_list", "c2_screenshot", "c2_tunnel", "c2_exec",
	} {
		out = append(out, actions[name])
	}
	return out
}

type Session struct {
	ExternalID string
	Status     string
	BindingID  int64
	TaskID     int64
	Active     bool
	Expired    bool
}

type Request struct {
	TaskID    int64
	SessionID string
	Action    string
	ScopeRows int
	Session   Session
	Config    Config
}

type Decision struct {
	Action          Action
	ApprovalNeeded  bool
	Danger          bool
	ApprovalStages  int
	PolicyRationale string
}

// Evaluate applies the non-rate-limit policy. Rate limiting and idempotency are
// stateful and are enforced by the service before it inserts an operation.
func Evaluate(req Request) (Decision, error) {
	if !req.Config.Enabled {
		return Decision{}, ErrDisabled
	}
	action, ok := Lookup(req.Action)
	if !ok {
		return Decision{}, fmt.Errorf("%w: %s", ErrUnknownAction, req.Action)
	}
	if req.TaskID <= 0 {
		return Decision{}, ErrTaskRequired
	}
	if strings.TrimSpace(req.SessionID) == "" || req.Session.ExternalID != req.SessionID {
		return Decision{}, ErrSessionRequired
	}
	if req.Session.TaskID != req.TaskID || req.Session.BindingID <= 0 || !req.Session.Active {
		return Decision{}, ErrSessionMismatch
	}
	if req.Session.Expired {
		return Decision{}, ErrSessionInactive
	}
	if strings.ToLower(strings.TrimSpace(req.Session.Status)) != "active" {
		return Decision{}, ErrSessionInactive
	}
	if !req.Config.AllowUnscoped && req.ScopeRows <= 0 {
		return Decision{}, ErrScopeRequired
	}

	decision := Decision{Action: action, PolicyRationale: "session binding and task scope verified"}
	if action.Name == "c2_tunnel" && !req.Config.AllowTunnel {
		return Decision{}, ErrTunnelDisabled
	}
	switch action.Level {
	case LevelRead:
		return decision, nil
	case LevelConfirm:
		if !req.Config.AllowConfirm {
			return Decision{}, ErrConfirmDisabled
		}
		decision.ApprovalNeeded = true
		decision.ApprovalStages = 1
		return decision, nil
	case LevelDanger:
		if !req.Config.AllowDanger {
			return Decision{}, ErrDangerDisabled
		}
		decision.ApprovalNeeded = true
		decision.Danger = true
		decision.ApprovalStages = 2
		decision.PolicyRationale += "; danger action requires two distinct approval stages"
		return decision, nil
	default:
		return Decision{}, fmt.Errorf("%w: unsupported level %q", ErrUnknownAction, action.Level)
	}
}

// ActionRequiresApproval reports whether the catalog action needs a human gate.
func ActionRequiresApproval(name string) bool {
	action, ok := Lookup(name)
	return ok && action.Level != LevelRead
}

func NormalizeAction(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
