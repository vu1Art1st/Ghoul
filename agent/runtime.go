package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Autumn-27/artex/hosttools"
)

// RuntimeEnv composes shared-runtime variables with the existing target proxy env.
func RuntimeEnv(workDir, proxyAddr, caCert string) []string {
	root := filepath.Join(workDir, "shared")
	pythonRoot := filepath.Join(root, "python")
	python := filepath.Join(pythonRoot, "venv", "bin", "python")
	binDir := filepath.Dir(python)
	sep := ":"
	if runtime.GOOS == "windows" {
		python = filepath.Join(pythonRoot, "venv", "Scripts", "python.exe")
		binDir = filepath.Dir(python)
		sep = ";"
	}
	libDir := filepath.Join(root, "lib")
	env := []string{
		"GHOUL_SHARED_DIR=" + root,
		"GHOUL_PYTHON=" + python,
		"GHOUL_PYTHONPATH=" + libDir,
		"PATH=" + binDir + sep + os.Getenv("PATH"),
		"PYTHONPATH=" + libDir,
	}
	return append(env, proxyEnv(proxyAddr, caCert)...)
}

func HostEnvironmentNote(workDir string) string {
	python := filepath.Join(workDir, "shared", "python", "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(workDir, "shared", "python", "venv", "Scripts", "python.exe")
	}
	var b strings.Builder
	if summary := hosttools.SummaryText(python); summary != "" {
		b.WriteString("\n\n【本机可用工具】\n" + summary)
	}
	b.WriteString("\n\n【共享空间】\n- shared_search / shared_get / shared_materialize 用于检索、查看并复制已审核共享资产；优先复用，不要重复开发。\n- 共享 Python 解释器路径在 GHOUL_PYTHON，共享库路径在 GHOUL_PYTHONPATH。需要 ddddocr 等共享包时使用 GHOUL_PYTHON 执行；不要向系统 Python 安装依赖。\n- 共享资产只读复用，Agent 不可发布、升级或卸载。")
	return b.String()
}
