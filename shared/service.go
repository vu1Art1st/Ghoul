// Package shared manages reviewed, versioned artifacts and the shared Python runtime.
package shared

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

var assetNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

type Service struct {
	dataDir string
	db      *db.DB
}

func New(dataDir string, pg *db.DB) (*Service, error) {
	root := filepath.Join(dataDir, "shared")
	for _, dir := range []string{root, filepath.Join(root, "assets"), filepath.Join(root, "staging"), filepath.Join(root, "python", "locks"), filepath.Join(root, "lib")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Service{dataDir: dataDir, db: pg}, nil
}

func (s *Service) Root() string       { return filepath.Join(s.dataDir, "shared") }
func (s *Service) PythonRoot() string { return filepath.Join(s.Root(), "python") }
func (s *Service) Python() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(s.PythonRoot(), "venv", "Scripts", "python.exe")
	}
	return filepath.Join(s.PythonRoot(), "venv", "bin", "python")
}

func (s *Service) resolveWorkspace(rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("需要工作空间相对路径")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("路径越界")
	}
	abs := filepath.Join(s.dataDir, clean)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(s.dataDir)
	if err != nil {
		return "", err
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("符号链接越过工作空间")
	}
	if within(filepath.Join(root, "shared"), resolved) {
		return "", fmt.Errorf("共享版本不可作为发布源")
	}
	return abs, nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

type PublishInput struct {
	Name, Kind, Description, Language, Entrypoint, Usage string
	Tags, Dependencies                                   []string
	SourcePath                                           string
	SourceTaskID, SourceIntentID                         int64
	AssetID                                              int64
}

func (s *Service) Publish(ctx context.Context, in PublishInput) (*db.SharedAsset, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !assetNameRE.MatchString(in.Name) {
		return nil, fmt.Errorf("名称需为 1-80 位字母、数字、点、下划线或连字符")
	}
	switch in.Kind {
	case "script", "tool", "library":
	default:
		return nil, fmt.Errorf("类型需为 script/tool/library")
	}
	src, err := s.resolveWorkspace(in.SourcePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if in.Kind == "script" {
		if info.IsDir() {
			return nil, fmt.Errorf("单文件脚本不能选择目录")
		}
		in.Entrypoint = filepath.Base(src)
	}
	if in.Kind != "script" && !info.IsDir() {
		return nil, fmt.Errorf("工具或语言库必须选择目录")
	}
	if info.IsDir() && in.Entrypoint == "" {
		return nil, fmt.Errorf("目录资产需要入口文件")
	}
	if in.Entrypoint != "" {
		clean := filepath.Clean(filepath.FromSlash(in.Entrypoint))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return nil, fmt.Errorf("入口文件路径非法")
		}
	}
	a := &db.SharedAsset{Name: in.Name, Kind: in.Kind, Description: strings.TrimSpace(in.Description), Language: in.Language, Entrypoint: in.Entrypoint, Usage: in.Usage, Tags: in.Tags, Dependencies: in.Dependencies, SourceTaskID: in.SourceTaskID, SourceIntentID: in.SourceIntentID}
	if in.AssetID > 0 {
		existing, err := s.db.SharedAsset(ctx, in.AssetID)
		if err != nil || existing == nil {
			return nil, fmt.Errorf("共享资产不存在")
		}
		if existing.Status == "disabled" || existing.Status == "archived" {
			return nil, fmt.Errorf("已停用资产不可发布新版本")
		}
		a = existing
		a.Description, a.Language, a.Entrypoint, a.Usage = in.Description, in.Language, in.Entrypoint, in.Usage
		a.Tags, a.Dependencies = in.Tags, in.Dependencies
		if err := s.db.UpdateSharedAssetMeta(ctx, a); err != nil {
			return nil, err
		}
	} else if err := s.db.CreateSharedAsset(ctx, a); err != nil {
		return nil, err
	}
	created := in.AssetID == 0
	published := false
	defer func() {
		if created && !published {
			_, _ = s.db.Exec(`DELETE FROM shared_assets WHERE id=$1 AND current_version=0`, a.ID)
		}
	}()
	if in.Kind == "library" && in.Language != "" && strings.ToLower(in.Language) != "python" {
		return nil, fmt.Errorf("当前语言库仅支持 Python；其他语言请先以工具类型保存")
	}
	staging, err := os.MkdirTemp(filepath.Join(s.Root(), "staging"), "publish-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	hash, err := copyValidated(src, staging)
	if err != nil {
		return nil, err
	}
	if in.Entrypoint != "" {
		entry := staging
		if info.IsDir() {
			entry = filepath.Join(staging, filepath.FromSlash(in.Entrypoint))
		}
		if fi, err := os.Stat(entry); err != nil || fi.IsDir() {
			return nil, fmt.Errorf("入口文件不存在")
		}
	}
	validation, _ := json.Marshal(map[string]any{"syntax": validateSyntax(staging, in.Language), "files": countFiles(staging)})
	v := &db.SharedAssetVersion{AssetID: a.ID, SourcePath: filepath.ToSlash(in.SourcePath), ContentHash: hash, Validation: validation}
	// The asset row lock serializes publishers and protects the version allocation.
	if err := s.db.AddSharedAssetVersion(ctx, v, func(version int) error {
		dest := s.VersionDir(a.ID, version)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.Rename(staging, dest); err != nil {
			return fmt.Errorf("共享版本落盘失败: %w", err)
		}
		if info.IsDir() {
			return makeTreeReadOnly(dest)
		}
		return os.Chmod(dest, 0o444)
	}); err != nil {
		return nil, err
	}
	if err := s.reconcileLibraries(); err != nil {
		return nil, err
	}
	published = true
	return s.db.SharedAsset(ctx, a.ID)
}

func (s *Service) reconcileLibraries() error {
	assets, err := s.db.ListSharedAssets(context.Background(), "", false)
	if err != nil {
		return err
	}
	libDir := filepath.Join(s.Root(), "lib")
	_ = os.MkdirAll(libDir, 0o755)
	for _, a := range assets {
		if a.Kind != "library" {
			continue
		}
		dest := filepath.Join(libDir, a.Name)
		current, err := os.Readlink(dest)
		if err == nil {
			resolved := current
			if !filepath.IsAbs(current) {
				resolved = filepath.Join(libDir, current)
			}
			if filepath.Clean(resolved) == filepath.Clean(s.VersionDir(a.ID, a.CurrentVersion)) {
				continue
			}
			if err := os.Remove(dest); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
		}
		if a.Status != "published" {
			continue
		}
		rel, err := filepath.Rel(libDir, s.VersionDir(a.ID, a.CurrentVersion))
		if err != nil {
			return err
		}
		if err := os.Symlink(rel, dest); err == nil {
			continue
		} else if runtime.GOOS != "windows" {
			return err
		}
		if err := copyTree(s.VersionDir(a.ID, a.CurrentVersion), dest); err != nil {
			return err
		}
	}
	return nil
}

func makeTreeReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o555)
		}
		return os.Chmod(path, 0o444)
	})
}

func copyValidated(src, dst string) (string, error) {
	h := sha256.New()
	var size int64
	if info, err := os.Stat(src); err == nil && !info.IsDir() {
		if info.Size() > 512<<20 {
			return "", fmt.Errorf("共享资产总大小不能超过 512 MiB")
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(dst), filepath.Base(src)), data, 0o644); err != nil {
			return "", err
		}
		_, _ = h.Write(data)
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("共享资产不允许包含符号链接: %s", rel)
		}
		if entry.IsDir() {
			if entry.Name() == "__pycache__" || entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("仅支持普通文件: %s", rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		if size > 512<<20 {
			return fmt.Errorf("共享资产总大小不能超过 512 MiB")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.MultiWriter(out, h), in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

// copyTree copies a published directory into a mutable task-local workspace.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func countFiles(root string) int {
	n := 0
	_ = filepath.WalkDir(root, func(_ string, e fs.DirEntry, _ error) error {
		if e != nil && !e.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func validateSyntax(root, language string) string {
	if strings.ToLower(language) != "python" {
		return "not_checked"
	}
	ok := true
	_ = filepath.WalkDir(root, func(_ string, e fs.DirEntry, _ error) error {
		if e != nil && !e.IsDir() && filepath.Ext(e.Name()) == ".py" {
			ok = true
		}
		return nil
	})
	if ok {
		return "manual_review_required"
	}
	return "not_checked"
}

func (s *Service) VersionDir(assetID int64, version int) string {
	return filepath.Join(s.Root(), "assets", fmt.Sprint(assetID), "versions", fmt.Sprint(version))
}

func (s *Service) List(ctx context.Context, q string, publishedOnly bool) ([]*db.SharedAsset, error) {
	return s.db.ListSharedAssets(ctx, q, publishedOnly)
}

func (s *Service) Get(ctx context.Context, id int64) (*db.SharedAsset, error) {
	return s.db.SharedAsset(ctx, id)
}

func (s *Service) Materialize(ctx context.Context, assetID int64, version int, destination string) (string, error) {
	a, err := s.db.SharedAsset(ctx, assetID)
	if err != nil || a == nil || a.Status != "published" {
		return "", fmt.Errorf("共享资产不存在或未发布")
	}
	if version == 0 {
		version = a.CurrentVersion
	}
	v, err := s.db.SharedAssetVersion(ctx, assetID, version)
	if err != nil || v == nil {
		return "", fmt.Errorf("共享版本不存在")
	}
	if destination == "" {
		return "", fmt.Errorf("缺少任务工作目录")
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(root, "shared", a.Name)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	if !within(resolvedRoot, resolvedParent) {
		return "", fmt.Errorf("任务 shared 目录包含越界符号链接")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		return "", fmt.Errorf("目标已存在: %s", dest)
	}
	if err := copyTree(s.VersionDir(assetID, version), dest); err != nil {
		return "", err
	}
	return filepath.Join(dest, filepath.FromSlash(a.Entrypoint)), nil
}

func (s *Service) SetStatus(ctx context.Context, id int64, status string) error {
	if err := s.db.SetSharedAssetStatus(ctx, id, status); err != nil {
		return err
	}
	return s.reconcileLibraries()
}

func (s *Service) Env() []string {
	env := []string{"GHOUL_SHARED_DIR=" + s.Root(), "GHOUL_PYTHON=" + s.Python(), "GHOUL_PYTHONPATH=" + filepath.Join(s.Root(), "lib")}
	path := os.Getenv("PATH")
	if runtime.GOOS == "windows" {
		path = filepath.Dir(s.Python()) + ";" + path
	} else {
		path = filepath.Dir(s.Python()) + ":" + path
	}
	return append(env, "PATH="+path, "PYTHONPATH="+filepath.Join(s.Root(), "lib"))
}

func (s *Service) PythonStatus() map[string]any {
	packages := []map[string]string{}
	freeze := filepath.Join(s.PythonRoot(), "locks", "requirements-freeze.txt")
	if data, err := os.ReadFile(freeze); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "==", 2)
			p := map[string]string{"name": parts[0]}
			if len(parts) == 2 {
				p["version"] = parts[1]
			}
			packages = append(packages, p)
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i]["name"] < packages[j]["name"] })
	_, err := os.Stat(s.Python())
	return map[string]any{"available": err == nil, "interpreter": s.Python(), "packages": packages, "updated_at": time.Now()}
}
