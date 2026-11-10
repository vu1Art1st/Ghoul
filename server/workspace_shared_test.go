package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedWorkspaceIsReadOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "shared", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared", "assets", "x.py"), []byte("x=1"), 0o444); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{dir: root}}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		path := "/api/workspace/write"
		if method == http.MethodDelete {
			path = "/api/workspace/delete?path=shared/assets/x.py"
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"path":"shared/assets/x.py","content":"changed"}`))
		if method == http.MethodPost {
			s.wsWrite(rec, req)
		} else {
			s.wsDelete(rec, req)
		}
		if rec.Code != 400 && rec.Code != 403 {
			t.Fatalf("%s code=%d", method, rec.Code)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "shared", "assets", "x.py"))
	if err != nil || string(data) != "x=1" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
