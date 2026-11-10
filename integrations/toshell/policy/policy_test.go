package policy

import (
	"errors"
	"testing"
)

func validRequest(action string, level Config) Request {
	return Request{
		TaskID: 7, SessionID: "session-1", Action: action, ScopeRows: 1,
		Session: Session{ExternalID: "session-1", Status: "active", BindingID: 3, TaskID: 7, Active: true},
		Config:  level,
	}
}

func TestReadActionAllowed(t *testing.T) {
	got, err := Evaluate(validRequest("c2_recon", Config{Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovalNeeded || got.Action.Level != LevelRead {
		t.Fatalf("decision = %+v", got)
	}
}

func TestConfirmAndDangerFailClosed(t *testing.T) {
	if _, err := Evaluate(validRequest("c2_file_list", Config{Enabled: true})); !errors.Is(err, ErrConfirmDisabled) {
		t.Fatalf("confirm error = %v", err)
	}
	if _, err := Evaluate(validRequest("c2_exec", Config{Enabled: true, AllowConfirm: true})); !errors.Is(err, ErrDangerDisabled) {
		t.Fatalf("danger error = %v", err)
	}
}

func TestDangerUsesTwoApprovalStages(t *testing.T) {
	got, err := Evaluate(validRequest("c2_exec", Config{Enabled: true, AllowConfirm: true, AllowDanger: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !got.ApprovalNeeded || got.ApprovalStages != 2 || !got.Danger {
		t.Fatalf("decision = %+v", got)
	}
}

func TestTunnelRequiresExplicitEnable(t *testing.T) {
	req := validRequest("c2_tunnel", Config{Enabled: true, AllowConfirm: true})
	if _, err := Evaluate(req); !errors.Is(err, ErrTunnelDisabled) {
		t.Fatalf("tunnel error = %v", err)
	}
}

func TestBindingAndScopeAreMandatory(t *testing.T) {
	req := validRequest("c2_recon", Config{Enabled: true})
	req.ScopeRows = 0
	if _, err := Evaluate(req); !errors.Is(err, ErrScopeRequired) {
		t.Fatalf("scope error = %v", err)
	}
	req = validRequest("c2_recon", Config{Enabled: true})
	req.Session.TaskID = 8
	if _, err := Evaluate(req); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("binding error = %v", err)
	}
}

func TestTaskLevelListenerAndPayloadActions(t *testing.T) {
	base := Request{TaskID: 7, Action: "c2_listener", ScopeRows: 1, Config: Config{Enabled: true}}
	base.SubAction = "list"
	got, err := Evaluate(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action.Level != LevelRead || got.ApprovalNeeded {
		t.Fatalf("listener list decision = %+v", got)
	}

	base.SubAction = "start"
	base.Config.AllowConfirm = true
	got, err = Evaluate(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action.Level != LevelConfirm || !got.ApprovalNeeded {
		t.Fatalf("listener start decision = %+v", got)
	}

	payload := Request{
		TaskID: 7, Action: "c2_payload", SubAction: "build", ScopeRows: 1,
		Config: Config{Enabled: true, AllowConfirm: true, AllowDanger: true},
	}
	got, err = Evaluate(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action.Level != LevelDanger || got.ApprovalStages != 2 {
		t.Fatalf("payload decision = %+v", got)
	}
}
