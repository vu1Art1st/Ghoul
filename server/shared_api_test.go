package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/shared"
)

func TestSharedAssetMetadataAndContentAPI(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config: %v", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer pg.Close()
	root := t.TempDir()
	svc, err := shared.New(root, pg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks", "12"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", "12", "main.py"), []byte("print('api')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("api-preview-%d", time.Now().UnixNano())
	asset, err := svc.Publish(context.Background(), shared.PublishInput{
		Name: name, DisplayName: "API 预览工具", Kind: "script", Description: "before",
		Language: "python", SourcePath: "tasks/12/main.py", SourceTaskID: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pg.Exec(`DELETE FROM shared_assets WHERE id=$1`, asset.ID) }()
	s := &Server{shared: svc}

	patchBody, _ := json.Marshal(map[string]any{
		"display_name":        "更新后的名称",
		"description":         "after",
		"tags":                []string{"api", "preview"},
		"expected_updated_at": asset.UpdatedAt.Format(time.RFC3339Nano),
	})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10), bytes.NewReader(patchBody))
	patchReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	patchRec := httptest.NewRecorder()
	s.sharedPatch(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}
	var patched db.SharedAsset
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.DisplayName != "更新后的名称" || patched.CurrentVersion != 1 {
		t.Fatalf("unexpected patched asset: %+v", patched)
	}
	staleBody, _ := json.Marshal(map[string]any{
		"description":         "stale",
		"expected_updated_at": asset.UpdatedAt.Format(time.RFC3339Nano),
	})
	staleReq := httptest.NewRequest(http.MethodPatch, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10), bytes.NewReader(staleBody))
	staleReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	staleRec := httptest.NewRecorder()
	s.sharedPatch(staleRec, staleReq)
	if staleRec.Code != http.StatusConflict {
		t.Fatalf("stale patch status=%d body=%s", staleRec.Code, staleRec.Body.String())
	}

	versionsReq := httptest.NewRequest(http.MethodGet, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10)+"/versions", nil)
	versionsReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	versionsRec := httptest.NewRecorder()
	s.sharedVersions(versionsRec, versionsReq)
	if versionsRec.Code != http.StatusOK || !bytes.Contains(versionsRec.Body.Bytes(), []byte("tasks/12/main.py")) {
		t.Fatalf("versions status=%d body=%s", versionsRec.Code, versionsRec.Body.String())
	}

	filesReq := httptest.NewRequest(http.MethodGet, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10)+"/versions/1/files", nil)
	filesReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	filesReq.SetPathValue("version", "1")
	filesRec := httptest.NewRecorder()
	s.sharedFiles(filesRec, filesReq)
	if filesRec.Code != http.StatusOK || !bytes.Contains(filesRec.Body.Bytes(), []byte("main.py")) {
		t.Fatalf("files status=%d body=%s", filesRec.Code, filesRec.Body.String())
	}

	fileReq := httptest.NewRequest(http.MethodGet, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10)+"/versions/1/file?path=main.py", nil)
	fileReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	fileReq.SetPathValue("version", "1")
	fileRec := httptest.NewRecorder()
	s.sharedFile(fileRec, fileReq)
	if fileRec.Code != http.StatusOK || !bytes.Contains(fileRec.Body.Bytes(), []byte("print('api')")) {
		t.Fatalf("file status=%d body=%s", fileRec.Code, fileRec.Body.String())
	}
	traversalReq := httptest.NewRequest(http.MethodGet, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10)+"/versions/1/file?path=../main.py", nil)
	traversalReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	traversalReq.SetPathValue("version", "1")
	traversalRec := httptest.NewRecorder()
	s.sharedFile(traversalRec, traversalReq)
	if traversalRec.Code == http.StatusOK {
		t.Fatalf("traversal unexpectedly allowed: %s", traversalRec.Body.String())
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10)+"/versions/1/download?path=main.py", nil)
	downloadReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	downloadReq.SetPathValue("version", "1")
	downloadRec := httptest.NewRecorder()
	s.sharedDownload(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusOK || downloadRec.Body.String() != "print('api')\n" {
		t.Fatalf("download status=%d body=%q", downloadRec.Code, downloadRec.Body.String())
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "/api/shared/assets/"+strconv.FormatInt(asset.ID, 10)+"/events", nil)
	eventsReq.SetPathValue("id", strconv.FormatInt(asset.ID, 10))
	eventsRec := httptest.NewRecorder()
	s.sharedEvents(eventsRec, eventsReq)
	if eventsRec.Code != http.StatusOK || !bytes.Contains(eventsRec.Body.Bytes(), []byte("update_metadata")) {
		t.Fatalf("events status=%d body=%s", eventsRec.Code, eventsRec.Body.String())
	}
}
