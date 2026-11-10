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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/shared"
)

const maxJobLog = 256 << 10

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
	j := &job{record: db.HostInstallJob{ID: hex.EncodeToString(idBytes), Kind: kind, Target: target, State: "queued"}, proxy: proxy}
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
	if j.record.Kind == "python-uninstall" {
		if err := i.ensureVenv(ctx, j); err != nil {
			return 1, err
		}
		code, err := i.command(ctx, j, i.shared.Python(), "-m", "pip", "uninstall", "-y", j.record.Target)
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
	if _, err := os.Stat(i.shared.Python()); err == nil {
		return nil
	}
	python := ""
	for _, candidate := range []string{"python3", "python"} {
		if path, err := exec.LookPath(candidate); err == nil {
			python = path
			break
		}
	}
	if python == "" {
		return fmt.Errorf("未检测到 Python，无法创建共享运行环境")
	}
	code, err := i.command(ctx, j, python, "-m", "venv", filepath.Join(i.shared.PythonRoot(), "venv"))
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("创建虚拟环境失败")
	}
	return nil
}

func (i *Installer) installPython(ctx context.Context, j *job, spec string) (int, error) {
	if err := ValidatePackageSpec(spec); err != nil {
		return 1, err
	}
	if err := i.ensureVenv(ctx, j); err != nil {
		return 1, err
	}
	args := []string{"-m", "pip", "install", "--disable-pip-version-check", spec}
	if proxy := i.jobProxy(j); proxy != "" {
		args = append(args, "--proxy", proxy)
	}
	code, err := i.command(ctx, j, i.shared.Python(), args...)
	if err != nil || code != 0 {
		return code, err
	}
	// Record the actual resolved dependency graph after a successful install.
	i.freeze(ctx)
	return 0, nil
}

func (i *Installer) freeze(ctx context.Context) {
	lockPath := filepath.Join(i.shared.PythonRoot(), "locks", "requirements-freeze.txt")
	cmd := exec.CommandContext(ctx, i.shared.Python(), "-m", "pip", "freeze")
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
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "PIP_DISABLE_PIP_VERSION_CHECK=1")
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

func installReleaseZip(ctx context.Context, client *http.Client, j *job, toolID, assetURL string) (int, error) {
	// Implemented in archive.go to keep transport and path-safety checks separate.
	return downloadAndInstallZip(ctx, client, j, toolID, assetURL)
}
