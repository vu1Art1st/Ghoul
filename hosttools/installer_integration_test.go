package hosttools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/shared"
)

// Opt in with GHOUL_TEST_PYTHON_RUNTIME=1. Uses an isolated temp venv only.
func TestSharedPythonRuntimeInstall(t *testing.T) {
	if os.Getenv("GHOUL_TEST_PYTHON_RUNTIME") != "1" {
		t.Skip("set GHOUL_TEST_PYTHON_RUNTIME=1 to run isolated package install")
	}
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config: %v", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer pg.Close()
	svc, err := shared.New(t.TempDir(), pg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	installer := NewInstaller(ctx, pg, svc, nil)
	job, err := installer.Start("python-package", "six==1.17.0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pg.Exec(`DELETE FROM host_install_jobs WHERE id=$1`, job.ID) }()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		job, err = installer.Get(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "queued" && job.State != "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if job.State != "done" {
		t.Fatalf("job=%+v", job)
	}
	cmd := exec.Command(svc.Python(), "-c", "import six;print(six.__version__)")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shared import failed: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(svc.PythonRoot(), "locks", "requirements-freeze.txt")); err != nil {
		t.Fatal(err)
	}
}
