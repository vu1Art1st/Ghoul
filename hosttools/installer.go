package hosttools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/shared"
)

const maxJobLog = 256 << 10

var (
	uvVersionRE = regexp.MustCompile(`^[0-9][A-Za-z0-9._+-]{0,39}$`)
	miseToolRE  = regexp.MustCompile(`^(uv|java|node|go)(?:@[0-9][A-Za-z0-9._+-]{0,39})?$`)
)

type Installer struct {
	db      *db.DB
	shared  *shared.Service
	ctx     context.Context
	sem     chan struct{}
	mu      sync.Mutex
	jobs    map[string]*job
	proxyFn func() string
}

type job struct {
	mu     sync.Mutex
	record db.HostInstallJob
	cancel context.CancelFunc
	proxy  string
}

func (j *job) append(text string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.record.Log += text
	if len(j.record.Log) > maxJobLog {
		j.record.Log = "[前段日志已截断]\n" + j.record.Log[len(j.record.Log)-maxJobLog:]
	}
}

func NewInstaller(ctx context.Context, pg *db.DB, sharedSvc *shared.Service, proxyFn func() string) *Installer {
	return &Installer{db: pg, shared: sharedSvc, ctx: ctx, sem: make(chan struct{}, 1), jobs: map[string]*job{}, proxyFn: proxyFn}
}

func (i *Installer) Start(kind, target string) (*db.HostInstallJob, error) {
	return i.start(kind, target, i.proxy())
}

func (i *Installer) StartTool(target, proxy string) (*db.HostInstallJob, error) {
	return i.start("tool", target, proxy)
}

func (i *Installer) start(kind, target, proxy string) (*db.HostInstallJob, error) {
	target = strings.TrimSpace(target)
	if kind == "python-package" {
		if err := ValidatePackageSpec(target); err != nil {
			return nil, err
		}
	} else if kind == "python-package-system" {
		if err := ValidatePackageSpec(target); err != nil {
			return nil, err
		}
	} else if kind == "python-venv" || kind == "uv-python" {
		target = strings.TrimSpace(target)
		if strings.ContainsAny(target, "\r\n\t") {
			return nil, fmt.Errorf("运行时参数包含非法字符")
		}
		if kind == "uv-python" && target != "" && !uvVersionRE.MatchString(target) {
			return nil, fmt.Errorf("uv Python 版本非法")
		}
	} else if kind == "mise-tool" {
		if err := ValidateMiseToolSpec(target); err != nil {
			return nil, fmt.Errorf("mise 工具规格非法: %w", err)
		}
	} else if kind == "python-uninstall" {
		if err := ValidatePackageSpec(target); err != nil {
			return nil, err
		}
	} else if kind == "tool" {
		if _, ok := Lookup(target); !ok {
			return nil, fmt.Errorf("工具不在白名单: %s", target)
		}
	} else {
		return nil, fmt.Errorf("不支持的安装类型")
	}
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	j := &job{record: db.HostInstallJob{ID: hex.EncodeToString(idBytes), Kind: kind, Target: target, State: "queued", Language: installLanguage(kind, target)}, proxy: proxy}
	if err := i.db.CreateHostInstallJob(context.Background(), &j.record); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(i.ctx)
	j.cancel = cancel
	i.mu.Lock()
	i.jobs[j.record.ID] = j
	i.mu.Unlock()
	go i.run(ctx, j)
	snap := j.record
	return &snap, nil
}

func (i *Installer) run(ctx context.Context, j *job) {
	i.sem <- struct{}{}
	defer func() { <-i.sem }()
	if ctx.Err() != nil {
		i.finish(j, "cancelled", 0, ctx.Err())
		return
	}
	now := time.Now()
	j.mu.Lock()
	j.record.State = "running"
	j.record.StartedAt = &now
	j.mu.Unlock()
	i.save(j)
	code, err := i.execute(ctx, j)
	state := "done"
	if ctx.Err() != nil {
		state = "cancelled"
	} else if err != nil {
		state = "failed"
	}
	i.finish(j, state, code, err)
}

func (i *Installer) finish(j *job, state string, code int, runErr error) {
	now := time.Now()
	j.mu.Lock()
	j.record.State, j.record.ExitCode, j.record.EndedAt = state, code, &now
	if runErr != nil {
		j.record.Error = redactProxy(runErr.Error(), j.proxy)
	}
	j.mu.Unlock()
	i.save(j)
}

func (i *Installer) save(j *job) {
	j.mu.Lock()
	snap := j.record
	j.mu.Unlock()
	_ = i.db.UpdateHostInstallJob(context.Background(), &snap)
}

func (i *Installer) Get(ctx context.Context, id string) (*db.HostInstallJob, error) {
	return i.db.HostInstallJob(ctx, id)
}
func (i *Installer) List(ctx context.Context) ([]*db.HostInstallJob, error) {
	return i.db.ListHostInstallJobs(ctx)
}
func (i *Installer) Cancel(id string) bool {
	i.mu.Lock()
	j := i.jobs[id]
	i.mu.Unlock()
	if j == nil || j.cancel == nil {
		return false
	}
	j.cancel()
	return true
}

func (i *Installer) execute(ctx context.Context, j *job) (int, error) {
	if j.record.Kind == "python-package" {
		return i.installPython(ctx, j, j.record.Target)
	}
	if j.record.Kind == "python-package-system" {
		return i.installSystemPython(ctx, j, j.record.Target)
	}
	if j.record.Kind == "python-venv" {
		base := j.record.Target
		clear := base == "repair" || base == "clear"
		if clear {
			base = ""
		}
		if err := i.ensureVenvWithBase(ctx, j, base, clear); err != nil {
			return 1, err
		}
		i.recordPythonVersion(ctx, j, i.shared.Python())
		return 0, nil
	}
	if j.record.Kind == "uv-python" {
		return i.installUvPython(ctx, j, j.record.Target)
	}
	if j.record.Kind == "mise-tool" {
		return i.installMiseTool(ctx, j, j.record.Target)
	}
	if j.record.Kind == "python-uninstall" {
		if err := i.ensureVenv(ctx, j); err != nil {
			return 1, err
		}
		code, err := i.runUvPip(ctx, j, "uninstall", "--python", i.shared.Python(), "-y", j.record.Target)
		if code == 0 {
			i.freeze(ctx)
		}
		return code, err
	}
	tool, _ := Lookup(j.record.Target)
	if tool.Kind == KindPython {
		return i.installPython(ctx, j, tool.Package)
	}
	if runtime.GOOS != "linux" {
		return 1, fmt.Errorf("当前平台仅提供手工安装指引")
	}
	switch tool.Kind {
	case KindApt:
		mgr := "apt-get"
		args := []string{"install", "-y", tool.Package}
		if _, err := exec.LookPath(mgr); err != nil {
			mgr = "dnf"
			if _, err := exec.LookPath(mgr); err != nil {
				return 1, fmt.Errorf("未检测到 apt-get/dnf")
			}
			args = []string{"install", "-y", tool.Package}
		}
		return i.command(ctx, j, mgr, args...)
	case KindGitHub:
		return i.installGitHub(ctx, j, tool)
	default:
		return 1, fmt.Errorf("该工具不支持自动安装")
	}
}

func (i *Installer) ensureVenv(ctx context.Context, j *job) error {
	return i.ensureVenvWithBase(ctx, j, "", false)
}

func (i *Installer) ensureVenvWithBase(ctx context.Context, j *job, base string, clear bool) error {
	if _, err := os.Stat(i.shared.Python()); err == nil {
		if !clear {
			return nil
		}
	}
	venvDir := filepath.Join(i.shared.PythonRoot(), "venvs", "default")
	if info, err := os.Stat(venvDir); err == nil && info.IsDir() && !clear {
		// The directory exists but its interpreter is not runnable on this host
		// (for example a venv copied from Windows into the Linux container).
		clear = true
		j.append("shared venv is present but unusable; rebuilding it\n")
	}
	if strings.TrimSpace(base) == "" {
		for _, candidate := range []string{"python3", "python"} {
			if path := i.findTool(candidate); path != "" {
				base = path
				break
			}
		}
	}
	if base == "" {
		return fmt.Errorf("未检测到 Python，无法创建共享运行环境")
	}
	if uv := i.findTool("uv"); uv != "" {
		args := []string{"venv", "--python", base}
		if clear {
			args = append(args, "--clear")
		}
		args = append(args, venvDir)
		code, runErr := i.command(ctx, j, uv, args...)
		if runErr == nil && code == 0 {
			return nil
		}
		j.append("uv venv failed; falling back to python -m venv\n")
	}
	if clear {
		_ = os.RemoveAll(venvDir)
	}
	code, err := i.command(ctx, j, base, "-m", "venv", venvDir)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("创建虚拟环境失败")
	}
	return nil
}

func (i *Installer) installUvPython(ctx context.Context, j *job, version string) (int, error) {
	uv := i.findTool("uv")
	if uv == "" {
		return 1, fmt.Errorf("未检测到 uv")
	}
	args := []string{"python", "install"}
	if version != "" {
		args = append(args, version)
	}
	return i.command(ctx, j, uv, args...)
}

func (i *Installer) installMiseTool(ctx context.Context, j *job, spec string) (int, error) {
	mise := i.findTool("mise")
	if mise == "" {
		return 1, fmt.Errorf("未检测到 mise")
	}
	if err := ValidateMiseToolSpec(spec); err != nil {
		return 1, err
	}
	return i.command(ctx, j, mise, "use", "--global", spec)
}

func (i *Installer) runUvPip(ctx context.Context, j *job, args ...string) (int, error) {
	python := i.shared.Python()
	fallbackArgs := make([]string, 0, len(args))
	for n := 0; n < len(args); n++ {
		if args[n] == "--python" && n+1 < len(args) {
			python = args[n+1]
			n++
			continue
		}
		fallbackArgs = append(fallbackArgs, args[n])
	}
	if uv := i.findTool("uv"); uv != "" {
		return i.command(ctx, j, uv, append([]string{"pip"}, args...)...)
	}
	return i.command(ctx, j, python, append([]string{"-m", "pip"}, fallbackArgs...)...)
}

func (i *Installer) installPython(ctx context.Context, j *job, spec string) (int, error) {
	if err := ValidatePackageSpec(spec); err != nil {
		return 1, err
	}
	if err := i.ensureVenv(ctx, j); err != nil {
		return 1, err
	}
	i.recordPythonVersion(ctx, j, i.shared.Python())
	args := []string{"install", "--python", i.shared.Python(), spec}
	if proxy := i.jobProxy(j); proxy != "" {
		args = append(args, "--proxy", proxy)
	}
	code, err := i.runUvPip(ctx, j, args...)
	if err != nil || code != 0 {
		return code, err
	}
	// Record the actual resolved dependency graph after a successful install.
	i.freeze(ctx)
	return 0, nil
}

func (i *Installer) systemPython() string {
	if p := strings.TrimSpace(os.Getenv("GHOUL_SYSTEM_PYTHON")); p != "" {
		return p
	}
	for _, candidate := range []string{"python3", "python"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func (i *Installer) installSystemPython(ctx context.Context, j *job, spec string) (int, error) {
	python := i.systemPython()
	if python == "" {
		return 1, fmt.Errorf("未检测到可用于回退的系统 Python")
	}
	version := i.pythonVersion(ctx, python)
	j.mu.Lock()
	j.record.ResolvedVersion = version
	j.mu.Unlock()
	j.append("system-install ephemeral interpreter=" + python + " version=" + version + " package=" + spec + "\n")
	var code int
	var err error
	if uv := i.findTool("uv"); uv != "" {
		args := []string{"pip", "install", "--system", "--python", python, spec}
		if proxy := i.jobProxy(j); proxy != "" {
			args = append(args, "--proxy", proxy)
		}
		code, err = i.command(ctx, j, uv, args...)
	} else {
		args := []string{"-m", "pip", "install", "--break-system-packages", spec}
		if proxy := i.jobProxy(j); proxy != "" {
			args = append(args, "--proxy", proxy)
		}
		code, err = i.command(ctx, j, python, args...)
	}
	if err != nil || code != 0 {
		return code, err
	}
	if code, err = i.verifySystemPackage(ctx, j, python, spec); err != nil || code != 0 {
		return code, err
	}
	j.append("system Python install is ephemeral and may be lost on container rebuild\n")
	return 0, nil
}

func (i *Installer) recordPythonVersion(ctx context.Context, j *job, python string) {
	version := i.pythonVersion(ctx, python)
	j.mu.Lock()
	j.record.ResolvedVersion = version
	j.mu.Unlock()
	if version != "" {
		j.append("resolved Python: " + version + "\n")
	}
}

func (i *Installer) pythonVersion(ctx context.Context, python string) string {
	cmd := exec.CommandContext(ctx, python, "--version")
	cmd.Env = os.Environ()
	if i.shared != nil {
		cmd.Env = append(cmd.Env, i.shared.Env()...)
	}
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func installLanguage(kind, target string) string {
	switch kind {
	case "python-package", "python-package-system", "python-venv", "uv-python", "python-uninstall":
		return "python"
	case "mise-tool":
		if idx := strings.IndexByte(target, '@'); idx >= 0 {
			target = target[:idx]
		}
		return strings.TrimSpace(target)
	default:
		return ""
	}
}

func (i *Installer) freeze(ctx context.Context) {
	lockPath := filepath.Join(i.shared.PythonRoot(), "locks", "requirements-freeze.txt")
	var cmd *exec.Cmd
	if uv := i.findTool("uv"); uv != "" {
		cmd = exec.CommandContext(ctx, uv, "pip", "freeze", "--python", i.shared.Python())
	} else {
		cmd = exec.CommandContext(ctx, i.shared.Python(), "-m", "pip", "freeze")
	}
	cmd.Env = append(os.Environ(), i.shared.Env()...)
	data, freezeErr := cmd.Output()
	if freezeErr == nil {
		_ = os.WriteFile(lockPath, data, 0o600)
	}
}

func (i *Installer) installGitHub(ctx context.Context, j *job, tool Tool) (int, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	if proxy := i.proxy(); proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return 1, err
		}
		client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+tool.Package+"/releases/latest", nil)
	resp, err := client.Do(req)
	if err != nil {
		return 1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1, fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return 1, err
	}
	arch := runtime.GOARCH
	needle := "linux_" + arch
	for _, asset := range release.Assets {
		if !strings.Contains(asset.Name, needle) || !strings.HasSuffix(asset.Name, ".zip") {
			continue
		}
		// Download recipes are kept in one helper; no release filename enters a shell.
		return installReleaseZip(ctx, client, j, tool.ID, asset.BrowserDownloadURL)
	}
	return 1, fmt.Errorf("Release 中没有 %s 的 zip 资产", needle)
}

func (i *Installer) command(ctx context.Context, j *job, name string, args ...string) (int, error) {
	proxy := i.jobProxy(j)
	j.append(redactProxy("$ "+name+" "+strings.Join(args, " ")+"\n", proxy))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	if i.shared != nil {
		cmd.Env = append(cmd.Env, i.shared.Env()...)
	}
	cmd.Env = append(cmd.Env, "DEBIAN_FRONTEND=noninteractive", "PIP_DISABLE_PIP_VERSION_CHECK=1")
	if proxy != "" {
		cmd.Env = append(cmd.Env, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy)
	}
	writer := &jobWriter{job: j, save: func() { i.save(j) }, proxy: proxy}
	cmd.Stdout, cmd.Stderr = writer, writer
	err := cmd.Run()
	i.save(j)
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), fmt.Errorf("%s 退出码 %d", name, exit.ExitCode())
	}
	return 0, err
}

func (i *Installer) findTool(name string) string {
	if i.shared != nil {
		if path := i.shared.FindTool(name); path != "" {
			return path
		}
	}
	path, _ := exec.LookPath(name)
	return path
}

func (i *Installer) verifySystemPackage(ctx context.Context, j *job, python, spec string) (int, error) {
	base := spec
	if idx := strings.IndexAny(base, "=<>!~[("); idx >= 0 {
		base = base[:idx]
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return 1, fmt.Errorf("无法从包规格解析包名")
	}
	if uv := i.findTool("uv"); uv != "" {
		return i.command(ctx, j, uv, "pip", "show", "--python", python, base)
	}
	return i.command(ctx, j, python, "-m", "pip", "show", base)
}

func (i *Installer) jobProxy(j *job) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.proxy != "" {
		return j.proxy
	}
	return i.proxy()
}

func redactProxy(text, proxy string) string {
	if proxy == "" {
		return text
	}
	text = strings.ReplaceAll(text, proxy, "[proxy]")
	if u, err := url.Parse(proxy); err == nil && u.User != nil {
		redacted := *u
		redacted.User = url.UserPassword(u.User.Username(), "****")
		text = strings.ReplaceAll(text, u.String(), redacted.String())
	}
	return text
}

func (i *Installer) proxy() string {
	if i.proxyFn == nil {
		return ""
	}
	return strings.TrimSpace(i.proxyFn())
}

type jobWriter struct {
	job   *job
	save  func()
	proxy string
}

func (w *jobWriter) Write(p []byte) (int, error) {
	text := redactProxy(string(p), w.proxy)
	w.job.append(text)
	w.save()
	return len(p), nil
}

func ValidatePackageSpec(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" || len(spec) > 200 || strings.ContainsAny(spec, " \t\r\n;&|$`'\"") || strings.HasPrefix(spec, "-") || strings.Contains(spec, "..") {
		return fmt.Errorf("Python 包规格非法，只允许明确的 PyPI 包名及可选版本约束")
	}
	for _, r := range spec {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-=[](),<>!~+@", r)) {
			return fmt.Errorf("Python 包规格含非法字符")
		}
	}
	if strings.ContainsAny(spec, "@:+") || strings.Contains(spec, "://") {
		return fmt.Errorf("只允许 PyPI 包名与版本约束，不允许 URL 或本地路径")
	}
	return nil
}

func ValidateMiseToolSpec(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" || !miseToolRE.MatchString(spec) {
		return fmt.Errorf("只允许 uv/java/node/go 及可选的 @version")
	}
	return nil
}

func installReleaseZip(ctx context.Context, client *http.Client, j *job, toolID, assetURL string) (int, error) {
	// Implemented in archive.go to keep transport and path-safety checks separate.
	return downloadAndInstallZip(ctx, client, j, toolID, assetURL)
}
