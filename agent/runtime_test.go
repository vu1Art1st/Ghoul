package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRuntimePrefersSharedVenv(t *testing.T) {
	root := t.TempDir()
	python := pythonPathForVenv(filepath.Join(root, "shared", "python", "venvs", "default"))
	writeExecutable(t, python)

	got := ResolveRuntime(root)
	if got.Python != python || got.PythonSource != "shared" || got.Fallback {
		t.Fatalf("ResolveRuntime() = %+v, want shared venv %s", got, python)
	}
}

func TestResolveRuntimeFallsBackToSystemPython(t *testing.T) {
	if lookPath("python3", "python") == "" {
		t.Skip("no system Python")
	}
	got := ResolveRuntime(t.TempDir())
	if got.PythonSource != "system" || !got.Fallback || got.Python == "" {
		t.Fatalf("ResolveRuntime() = %+v, want system fallback", got)
	}
}

func TestResolveRuntimePrefersUvManagedPythonBeforeSystem(t *testing.T) {
	root := t.TempDir()
	name := "python"
	if runtime.GOOS == "windows" {
		name = "python.exe"
	}
	python := filepath.Join(root, "shared", "python", "uv", "bin", name)
	writeExecutable(t, python)

	got := ResolveRuntime(root)
	if got.Python != python || got.PythonSource != "uv" || !got.Fallback {
		t.Fatalf("ResolveRuntime() = %+v, want uv-managed Python %s", got, python)
	}
}

func TestRuntimeEnvUsesExistingPathsAndCrossPlatformSeparator(t *testing.T) {
	root := t.TempDir()
	python := pythonPathForVenv(filepath.Join(root, "shared", "python", "venvs", "default"))
	writeExecutable(t, python)
	uvBin := filepath.Join(root, "shared", "python", "uv", "bin")
	miseShims := filepath.Join(root, "shared", "mise", "shims")
	for _, dir := range []string{uvBin, miseShims} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	env := RuntimeEnv(root, "", "")
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"GHOUL_PYTHON=" + python,
		"GHOUL_RUNTIME_SOURCE=shared",
		"GHOUL_ALLOW_SYSTEM_PYTHON_INSTALL=1",
		"UV_PYTHON_INSTALL_DIR=",
		"MISE_DATA_DIR=",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("RuntimeEnv missing %q: %v", want, env)
		}
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(joined, uvBin+";") {
			t.Errorf("RuntimeEnv on Windows must use semicolon PATH separator: %v", env)
		}
	} else if !strings.Contains(joined, uvBin+":") {
		t.Errorf("RuntimeEnv on Unix must use colon PATH separator: %v", env)
	}
}
