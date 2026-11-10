package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

type fakeSharedStore struct{ materialized bool }

func (f *fakeSharedStore) List(context.Context, string, bool) ([]*db.SharedAsset, error) {
	return []*db.SharedAsset{{ID: 7, Name: "captcha", Status: "published", CurrentVersion: 2}}, nil
}
func (f *fakeSharedStore) Get(context.Context, int64) (*db.SharedAsset, error) {
	return &db.SharedAsset{ID: 7, Name: "captcha", Status: "published"}, nil
}
func (f *fakeSharedStore) Materialize(_ context.Context, id int64, version int, destination string) (string, error) {
	f.materialized = true
	return filepath.Join(destination, "shared", "captcha", "main.py"), nil
}

func TestSharedToolsUseCurrentTaskDirectory(t *testing.T) {
	store := &fakeSharedStore{}
	var materialize actool.CoreTool
	for _, tool := range SharedTools(store) {
		if tool.Name() == "shared_materialize" {
			materialize = tool
		}
	}
	workDir := t.TempDir()
	result, err := materialize.Call(context.Background(), json.RawMessage(`{"id":7}`), &actool.ToolContext{WorkingDir: workDir})
	if err != nil || result.IsError || !store.materialized {
		t.Fatalf("result=%v err=%v materialized=%v", result, err, store.materialized)
	}
}

func TestSharedToolDefaultBindings(t *testing.T) {
	bindings := map[string][]string{}
	for _, seed := range BuiltinToolSeeds() {
		bindings[seed.Key] = seed.Agents
	}
	for _, tool := range []string{"shared_search", "shared_get"} {
		if !containsString(bindings[tool], "worker") || !containsString(bindings[tool], "planner") {
			t.Errorf("%s bindings=%v", tool, bindings[tool])
		}
	}
	if containsString(bindings["shared_materialize"], "planner") || !containsString(bindings["shared_materialize"], "worker") {
		t.Errorf("materialize bindings=%v", bindings["shared_materialize"])
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
