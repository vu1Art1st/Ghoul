package shared

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyValidatedSingleFileUsesEntrypoint(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "bypass.py")
	if err := os.WriteFile(src, []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "published.py")
	if _, err := copyValidated(src, dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "bypass.py")); err != nil || string(data) != "print('ok')\n" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestCopyValidatedRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := copyValidated(src, filepath.Join(root, "dst")); err == nil {
		t.Fatal("expected symlink rejection")
	}
}
