package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

type screenshotRecorder struct {
	input        db.FindingScreenshotInput
	captureInput db.FindingScreenshotCaptureInput
}

func (r *screenshotRecorder) Record(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error) {
	return nil, nil
}

func (r *screenshotRecorder) RecordFindingScreenshot(_ context.Context, in db.FindingScreenshotInput) (*db.FindingEvidence, error) {
	r.input = in
	return &db.FindingEvidence{ID: 42, FindingID: in.FindingID, Kind: "screenshot", Title: in.Title}, nil
}

func (r *screenshotRecorder) CaptureFindingScreenshot(_ context.Context, in db.FindingScreenshotCaptureInput) (*db.FindingEvidence, error) {
	r.captureInput = in
	return &db.FindingEvidence{ID: 43, FindingID: in.FindingID, Kind: "screenshot", Title: in.Title}, nil
}

func TestSaveFindingScreenshotBindsRunDirAndFinding(t *testing.T) {
	runDir := t.TempDir()
	path := filepath.Join(runDir, "shots", "idor.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("png fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &screenshotRecorder{}
	tools := NewToolSet(nil, "worker")
	tools.SetTaskID(219)
	tools.SetFindingRecorder(recorder)

	tool := tools.saveFindingScreenshot()
	res, err := tool.Call(t.Context(), json.RawMessage(`{
		"finding_id":581,
		"path":"shots/idor.png",
		"title":"Autorize Lab：John 越权读取 Bob 私信",
		"note":"John(uid=101) 将 user_id 改为 102 后返回 Bob 的 Super secret message",
		"source_url":"https://labs.cai.do/autorize.php?action=messages&user_id=102"
	}`), &actool.ToolContext{WorkingDir: runDir})
	if err != nil {
		t.Fatal(err)
	}
	if recorder.input.TaskID != 219 || recorder.input.FindingID != 581 || recorder.input.RunDir != runDir {
		t.Fatalf("unexpected screenshot binding: %+v", recorder.input)
	}
	if recorder.input.Path != "shots/idor.png" || !strings.Contains(recorder.input.Note, "Super secret message") {
		t.Fatalf("screenshot metadata lost: %+v", recorder.input)
	}
	if !strings.Contains(res.Flatten(), "artifact:42") {
		t.Fatalf("artifact reference missing: %s", res.Flatten())
	}
}

func TestCaptureFindingScreenshotUsesControlledPageFlow(t *testing.T) {
	recorder := &screenshotRecorder{}
	tools := NewToolSet(nil, "worker")
	tools.SetTaskID(220)
	tools.SetFindingRecorder(recorder)
	res, err := tools.captureFindingScreenshot().Call(t.Context(), json.RawMessage(`{
		"finding_id":582,
		"page_url":"https://labs.cai.do/autorize.php",
		"action_selector":"button:has-text('Get Messages')",
		"wait_selector":"#testResponse",
		"route_match":"**/autorize.php?action=messages*",
		"query":{"user_id":"102"},
		"prepare_js":"currentToken='Bearer token'; currentUserId=101;",
		"title":"John 越权读取 Bob 私信",
		"wait_text":"Super secret message"
	}`), &actool.ToolContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	got := recorder.captureInput
	if got.TaskID != 220 || got.FindingID != 582 || got.Query["user_id"] != "102" {
		t.Fatalf("unexpected controlled capture input: %+v", got)
	}
	if got.ActionSelector == "" || got.WaitSelector != "#testResponse" || got.RouteMatch == "" {
		t.Fatalf("controlled capture selectors lost: %+v", got)
	}
	if got.Mode != db.FindingCaptureNativeAction {
		t.Fatalf("default capture mode=%q", got.Mode)
	}
	if !strings.Contains(res.Flatten(), db.FindingCaptureNativeAction) {
		t.Fatalf("controlled provenance missing: %s", res.Flatten())
	}
}

func TestCaptureFindingScreenshotNavigationReplay(t *testing.T) {
	recorder := &screenshotRecorder{}
	tools := NewToolSet(nil, "worker")
	tools.SetTaskID(221)
	tools.SetFindingRecorder(recorder)
	_, err := tools.captureFindingScreenshot().Call(t.Context(), json.RawMessage(`{
		"finding_id":585,
		"mode":"navigation_replay",
		"page_url":"https://labs.cai.do/shiftPayload.php",
		"route_match":"**/shiftPayload.php",
		"http_method":"POST",
		"request_body":"{\"action\":\"scan\",\"action\":\"exfiltrate\"}",
		"content_type":"application/json",
		"wait_selector":"body",
		"title":"shiftPayload 重放"
	}`), &actool.ToolContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	got := recorder.captureInput
	if got.Mode != db.FindingCaptureNavigationReplay || got.HTTPMethod != "POST" ||
		got.RequestBody == "" || got.ContentType != "application/json" {
		t.Fatalf("navigation replay fields lost: %+v", got)
	}
}
