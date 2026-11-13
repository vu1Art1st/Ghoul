package shared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// Requires the same opt-in PostgreSQL configuration as the other integration suites.
func TestPublishAndMaterializeLifecycle(t *testing.T) {
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
	svc, err := New(root, pg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks", "4"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("tasks", "4", "captcha.py")
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(source)), []byte("print('captcha')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("captcha-test-%d", time.Now().UnixNano())
	asset, err := svc.Publish(context.Background(), PublishInput{Name: name, Kind: "script", Description: "test shared script", Language: "python", SourcePath: source, SourceTaskID: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pg.Exec(`DELETE FROM shared_assets WHERE id=$1`, asset.ID)
	}()
	if asset.Status != "published" || asset.CurrentVersion != 1 {
		t.Fatalf("asset=%+v", asset)
	}
	entry, err := svc.Materialize(context.Background(), asset.ID, 0, filepath.Join(root, "tasks", "5"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(entry)
	if err != nil || string(data) != "print('captcha')\n" {
		t.Fatalf("entry=%q data=%q err=%v", entry, data, err)
	}
	listed, err := svc.List(context.Background(), name, true)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%v err=%v", listed, err)
	}
}

func TestSharedAssetMetadataAndVersionPreviewLifecycle(t *testing.T) {
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
	svc, err := New(root, pg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks", "9"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("tasks", "9", "preview.py")
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(source)), []byte("print('preview')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("preview-test-%d", time.Now().UnixNano())
	asset, err := svc.Publish(context.Background(), PublishInput{
		Name: name, DisplayName: "预览测试工具", Kind: "script", Description: "before",
		Language: "python", SourcePath: source, SourceTaskID: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pg.Exec(`DELETE FROM shared_assets WHERE id=$1`, asset.ID) }()

	initialUpdatedAt := asset.UpdatedAt
	updated, err := svc.UpdateMeta(context.Background(), asset.ID, AssetMetaPatch{
		DisplayName: stringPtr("新的显示名称"),
		Description: stringPtr("after"),
		Tags:        &[]string{"预览", "共享"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CurrentVersion != asset.CurrentVersion || updated.DisplayName != "新的显示名称" ||
		updated.Description != "after" || len(updated.Tags) != 2 {
		t.Fatalf("unexpected metadata update: %+v", updated)
	}

	_, err = svc.UpdateMeta(context.Background(), asset.ID, AssetMetaPatch{
		Description:       stringPtr("stale"),
		ExpectedUpdatedAt: initialUpdatedAt,
	})
	if !errors.Is(err, db.ErrSharedAssetConflict) {
		t.Fatalf("expected optimistic concurrency conflict, got %v", err)
	}

	entries, path, err := svc.VersionFiles(context.Background(), asset.ID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || len(entries) != 1 || entries[0].Path != "preview.py" {
		t.Fatalf("unexpected entries path=%q entries=%+v", path, entries)
	}
	file, err := svc.ReadVersionFile(context.Background(), asset.ID, 1, "preview.py")
	if err != nil {
		t.Fatal(err)
	}
	if file.Binary || file.Content != "print('preview')\n" {
		t.Fatalf("unexpected preview: %+v", file)
	}
	if _, _, err := svc.VersionDownloadPath(context.Background(), asset.ID, 1, "../preview.py"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	versions, err := svc.Versions(context.Background(), asset.ID)
	if err != nil || len(versions) != 1 || versions[0].SourcePath != filepath.ToSlash(source) {
		t.Fatalf("unexpected versions=%+v err=%v", versions, err)
	}
	events, err := svc.Events(context.Background(), asset.ID)
	if err != nil || len(events) < 2 {
		t.Fatalf("unexpected events=%+v err=%v", events, err)
	}
}

func stringPtr(value string) *string {
	return &value
}
