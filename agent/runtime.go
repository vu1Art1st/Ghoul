package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Autumn-27/artex/hosttools"
)

// RuntimeResolution is the executable/runtime set injected into Bash. Shared
// uv environments are preferred, but a healthy system runtime keeps an agent
// productive when the shared volume is absent, stale, or broken.
type RuntimeResolution struct {
	SharedDir    string `json:"shared_dir"`
	VenvPython   string `json:"venv_python,omitempty"`
	Python       string `json:"python,omitempty"`
	PythonSource string `json:"python_source"` // shared | uv | system | missing
	Uv           string `json:"uv,omitempty"`
	Mise         string `json:"mise,omitempty"`
	Java         string `json:"java,omitempty"`
	JavaHome     string `json:"java_home,omitempty"`
	Node         string `json:"node,omitempty"`
	Fallback     bool   `json:"fallback"`
	Warning      string `json:"warning,omitempty"`
}

func pythonPathForVenv(root string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "Scripts", "python.exe")
	}
	return filepath.Join(root, "bin", "python")
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func findInDirs(names []string, dirs []string) string {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, name := range names {
			if runtime.GOOS == "windows" {
				if p := firstExisting(filepath.Join(dir, name+".exe")); p != "" {
					return p
				}
			}
			if p := firstExisting(filepath.Join(dir, name)); p != "" {
				return p
			}
		}
	}
	return ""
}

func lookPath(names ...string) string {
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ResolveRuntime computes the effective runtime without mutating the filesystem.
func ResolveRuntime(workDir string) RuntimeResolution {
	root := filepath.Join(workDir, "shared")
	pythonRoot := filepath.Join(root, "python")
	r := RuntimeResolution{SharedDir: root, PythonSource: "missing"}
	r.VenvPython = firstExisting(
		pythonPathForVenv(filepath.Join(pythonRoot, "venvs", "default")),
		pythonPathForVenv(filepath.Join(pythonRoot, "venv")),
	)
	r.Python = r.VenvPython
	if r.Python != "" {
		r.PythonSource = "shared"
	} else {
		r.Python = findInDirs([]string{"python3", "python"}, []string{
			filepath.Join(pythonRoot, "uv", "bin"),
			filepath.Join(root, "mise", "shims"),
		})
		if r.Python != "" {
			r.PythonSource = "uv"
			r.Fallback = true
			r.Warning = "shared Python venv is unavailable; using uv-managed Python"
		} else {
			r.Python = lookPath("python3", "python")
			if r.Python != "" {
				r.PythonSource = "system"
				r.Fallback = true
				r.Warning = "shared Python venv is unavailable; using system Python"
			}
		}
	}
	sharedToolDirs := []string{
		filepath.Join(pythonRoot, "uv", "bin"),
		filepath.Join(root, "mise", "shims"),
	}
	r.Uv = findInDirs([]string{"uv"}, sharedToolDirs)
	if r.Uv == "" {
		r.Uv = lookPath("uv")
	}
	r.Mise = findInDirs([]string{"mise"}, sharedToolDirs)
	if r.Mise == "" {
		r.Mise = lookPath("mise")
	}
	r.Java = findInDirs([]string{"java"}, sharedToolDirs)
	if r.Java == "" {
		r.Java = lookPath("java")
	}
	r.JavaHome = os.Getenv("JAVA_HOME")
	r.Node = findInDirs([]string{"node"}, sharedToolDirs)
	if r.Node == "" {
		r.Node = lookPath("node")
	}
	return r
}

// RuntimeEnv composes the resolved shared/system runtime with the target proxy env.
func RuntimeEnv(workDir, proxyAddr, caCert string) []string {
	r := ResolveRuntime(workDir)
	root := r.SharedDir
	libDir := filepath.Join(root, "lib")
	uvRoot := filepath.Join(root, "python", "uv")
	miseRoot := filepath.Join(root, "mise")
	defaultVenv := filepath.Join(root, "python", "venvs", "default")

	env := []string{
		"GHOUL_SHARED_DIR=" + root,
		"GHOUL_PYTHONPATH=" + libDir,
		"GHOUL_RUNTIME_MODE=prefer-shared",
		"GHOUL_RUNTIME_SOURCE=" + r.PythonSource,
		"GHOUL_ALLOW_SYSTEM_PYTHON_INSTALL=1",
		"UV_PYTHON_INSTALL_DIR=" + filepath.Join(uvRoot, "python"),
		"UV_PYTHON_BIN_DIR=" + filepath.Join(uvRoot, "bin"),
		"UV_CACHE_DIR=" + filepath.Join(uvRoot, "cache"),
		"UV_PROJECT_ENVIRONMENT=" + defaultVenv,
		"MISE_DATA_DIR=" + filepath.Join(miseRoot, "data"),
		"MISE_CONFIG_DIR=" + filepath.Join(miseRoot, "config"),
		"MISE_CACHE_DIR=" + filepath.Join(miseRoot, "cache"),
		"MISE_STATE_DIR=" + filepath.Join(miseRoot, "state"),
		"PYTHONPATH=" + libDir,
	}
	if r.Python != "" {
		env = append(env, "GHOUL_PYTHON="+r.Python)
	} else {
		env = append(env, "GHOUL_PYTHON=")
	}
	if r.Uv != "" {
		env = append(env, "GHOUL_UV="+r.Uv)
	} else {
		env = append(env, "GHOUL_UV=")
	}
	if r.Mise != "" {
		env = append(env, "GHOUL_MISE="+r.Mise)
	} else {
		env = append(env, "GHOUL_MISE=")
	}
	if r.Java != "" {
		env = append(env, "GHOUL_JAVA="+r.Java)
	} else {
		env = append(env, "GHOUL_JAVA=")
	}
	if r.JavaHome != "" {
		env = append(env, "GHOUL_JAVA_HOME="+r.JavaHome)
	}
	if r.Node != "" {
		env = append(env, "GHOUL_NODE="+r.Node)
	} else {
		env = append(env, "GHOUL_NODE=")
	}
	if systemPython := lookPath("python3", "python"); systemPython != "" {
		env = append(env, "GHOUL_SYSTEM_PYTHON="+systemPython)
	}
	if r.Warning != "" {
		env = append(env, "GHOUL_RUNTIME_WARNING="+r.Warning)
	}

	sep := string(os.PathListSeparator)
	pathParts := []string{}
	for _, p := range []string{
		filepath.Dir(r.VenvPython),
		filepath.Join(uvRoot, "bin"),
		filepath.Join(miseRoot, "shims"),
	} {
		if p != "." && p != "" {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				pathParts = append(pathParts, p)
			}
		}
	}
	pathParts = append(pathParts, os.Getenv("PATH"))
	env = append(env, "PATH="+strings.Join(pathParts, sep))
	return append(env, proxyEnv(proxyAddr, caCert)...)
}

func HostEnvironmentNote(workDir string) string {
	r := ResolveRuntime(workDir)
	var b strings.Builder
	if summary := hosttools.SummaryText(r.Python); summary != "" {
		b.WriteString("\n\n【本机可用工具】\n" + summary)
	}
	b.WriteString("\n\n【共享空间与运行时】\n- shared_search / shared_get / shared_materialize 用于检索、查看并复制已审核共享资产；优先复用，不要重复开发。\n- Python 解释器、venv 和 Python 包优先使用 uv；共享库路径在 GHOUL_PYTHONPATH。\n- 共享运行时不可用时允许调用系统 Python、Java、Node.js 继续执行。\n- 缺少 Python 包时先调用 runtime_ensure 安装到共享 uv venv；共享安装失败时允许安装到系统 Python，并必须记录为易失环境。\n- 不要自行重写 WebSocket、HTTP、加密、解析器等成熟基础库。")
	if r.Warning != "" {
		b.WriteString("\n- 当前运行时回退：" + r.Warning + "。")
	}
	return b.String()
}
