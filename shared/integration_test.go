package shared

import (
	"context"
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
