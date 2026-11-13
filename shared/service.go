// Package shared manages reviewed, versioned artifacts and the shared Python runtime.
package shared

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
)

var assetNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

type Service struct {
	dataDir string
	db      *db.DB
}

func New(dataDir string, pg *db.DB) (*Service, error) {
	root := filepath.Join(dataDir, "shared")
	for _, dir := range []string{
		root,
		filepath.Join(root, "assets"),
		filepath.Join(root, "staging"),
		filepath.Join(root, "python", "locks"),
		filepath.Join(root, "python", "venvs"),
		filepath.Join(root, "python", "uv", "cache"),
		filepath.Join(root, "python", "uv", "python"),
		filepath.Join(root, "python", "uv", "bin"),
		filepath.Join(root, "mise", "data"),
		filepath.Join(root, "mise", "config"),
		filepath.Join(root, "mise", "cache"),
		filepath.Join(root, "mise", "state"),
		filepath.Join(root, "mise", "shims"),
		filepath.Join(root, "lib"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Service{dataDir: dataDir, db: pg}, nil
}

func (s *Service) Root() string       { return filepath.Join(s.dataDir, "shared") }
func (s *Service) PythonRoot() string { return filepath.Join(s.Root(), "python") }
func (s *Service) Python() string {
	for _, root := range []string{
		filepath.Join(s.PythonRoot(), "venvs", "default"),
		filepath.Join(s.PythonRoot(), "venv"),
	} {
		if runtime.GOOS == "windows" {
			p := filepath.Join(root, "Scripts", "python.exe")
			if _, err := os.Stat(p); err == nil {
				return p
			}
			continue
		}
		p := filepath.Join(root, "bin", "python")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(s.PythonRoot(), "venvs", "default", "Scripts", "python.exe")
	}
	return filepath.Join(s.PythonRoot(), "venvs", "default", "bin", "python")
}
func (s *Service) UvRoot() string    { return filepath.Join(s.PythonRoot(), "uv") }
func (s *Service) MiseRoot() string  { return filepath.Join(s.Root(), "mise") }
func (s *Service) VenvsRoot() string { return filepath.Join(s.PythonRoot(), "venvs") }

// FindTool resolves an executable from the managed shared directories before
// falling back to PATH. This keeps host installs and agent shells on the same
// uv/mise binaries.
func (s *Service) FindTool(names ...string) string {
	if len(names) == 0 {
		return ""
	}
	dirs := []string{
		filepath.Join(s.UvRoot(), "bin"),
		filepath.Join(s.MiseRoot(), "shims"),
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if filepath.IsAbs(name) {
			if info, err := os.Stat(name); err == nil && !info.IsDir() {
				return name
			}
			continue
		}
		for _, dir := range dirs {
			candidates := []string{filepath.Join(dir, name)}
			if runtime.GOOS == "windows" {
				candidates = append([]string{filepath.Join(dir, name+".exe")}, candidates...)
			}
			for _, candidate := range candidates {
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					return candidate
				}
			}
		}
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
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
	Name, DisplayName, Kind, Description, Language, Entrypoint, Usage string
	Tags, Dependencies                                                []string
	SourcePath                                                        string
	RuntimeSpec                                                       string
	SourceTaskID, SourceIntentID                                      int64
	AssetID, RuntimeID                                                int64
	Healthcheck                                                       json.RawMessage
}

func (s *Service) Publish(ctx context.Context, in PublishInput) (*db.SharedAsset, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !assetNameRE.MatchString(in.Name) {
		return nil, fmt.Errorf("稳定路径标识需为 1-80 位字母、数字、点、下划线或连字符")
	}
	switch in.Kind {
	case "script", "tool", "library", "skill", "template", "ctf_report":
	default:
		return nil, fmt.Errorf("类型需为 script/tool/library/skill/template/ctf_report")
	}
	src, err := s.resolveWorkspace(in.SourcePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if in.Kind == "script" || in.Kind == "template" || (in.Kind == "ctf_report" && !info.IsDir()) {
		if info.IsDir() {
			return nil, fmt.Errorf("%s 单文件资产不能选择目录", in.Kind)
		}
		in.Entrypoint = filepath.Base(src)
	}
	if (in.Kind == "tool" || in.Kind == "library" || in.Kind == "skill") && !info.IsDir() {
		return nil, fmt.Errorf("工具、语言库和 Skill 必须选择目录")
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
	displayName, err := validateDisplayName(in.DisplayName, in.Name)
	if err != nil {
		return nil, err
	}
	tags, err := normalizeStringList(in.Tags, 32, 64, "标签")
	if err != nil {
		return nil, err
	}
	dependencies, err := normalizeStringList(in.Dependencies, 64, 128, "依赖")
	if err != nil {
		return nil, err
	}
	a := &db.SharedAsset{Name: in.Name, DisplayName: displayName, Kind: in.Kind, Description: strings.TrimSpace(in.Description), Language: strings.TrimSpace(in.Language), Entrypoint: in.Entrypoint, Usage: in.Usage, Tags: tags, Dependencies: dependencies, RuntimeID: in.RuntimeID, RuntimeSpec: in.RuntimeSpec, Healthcheck: in.Healthcheck, SourceTaskID: in.SourceTaskID, SourceIntentID: in.SourceIntentID}
	if in.AssetID > 0 {
		existing, err := s.db.SharedAsset(ctx, in.AssetID)
		if err != nil || existing == nil {
			return nil, fmt.Errorf("共享资产不存在")
		}
		if existing.Status == "disabled" || existing.Status == "archived" {
			return nil, fmt.Errorf("已停用资产不可发布新版本")
		}
		a = existing
		if strings.TrimSpace(in.DisplayName) != "" {
			a.DisplayName = displayName
		}
		a.Description, a.Language, a.Entrypoint, a.Usage = in.Description, in.Language, in.Entrypoint, in.Usage
		a.Tags, a.Dependencies = tags, dependencies
		if in.RuntimeID > 0 || strings.TrimSpace(in.RuntimeSpec) != "" || len(in.Healthcheck) > 0 {
			a.RuntimeID, a.RuntimeSpec, a.Healthcheck = in.RuntimeID, in.RuntimeSpec, in.Healthcheck
		}
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
		} else {
			entry = filepath.Join(staging, filepath.Base(src))
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
		if versionInfo, err := os.Stat(dest); err == nil && versionInfo.IsDir() {
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
	result, err := s.db.SharedAsset(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	detail, _ := json.Marshal(map[string]any{"version": result.CurrentVersion, "content_hash": hash})
	_ = s.db.CreateSharedAssetEvent(ctx, &db.SharedAssetEvent{AssetID: a.ID, Action: "publish_version", Detail: detail})
	return result, nil
}

type AssetMetaPatch struct {
	DisplayName       *string   `json:"display_name"`
	Description       *string   `json:"description"`
	Tags              *[]string `json:"tags"`
	Language          *string   `json:"language"`
	Usage             *string   `json:"usage"`
	Dependencies      *[]string `json:"dependencies"`
	RuntimeSpec       *string   `json:"runtime_spec"`
	ExpectedUpdatedAt time.Time `json:"-"`
}

func (s *Service) UpdateMeta(ctx context.Context, id int64, patch AssetMetaPatch) (*db.SharedAsset, error) {
	current, err := s.db.SharedAsset(ctx, id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, fmt.Errorf("共享资产不存在")
	}
	before, _ := json.Marshal(map[string]any{
		"display_name": current.DisplayName, "description": current.Description, "tags": current.Tags,
		"language": current.Language, "usage": current.Usage, "dependencies": current.Dependencies,
		"runtime_spec": current.RuntimeSpec,
	})
	next := *current
	if patch.DisplayName != nil {
		next.DisplayName, err = validateDisplayName(*patch.DisplayName, current.Name)
		if err != nil {
			return nil, err
		}
	}
	if patch.Description != nil {
		next.Description = strings.TrimSpace(*patch.Description)
		if utf8.RuneCountInString(next.Description) > 4000 {
			return nil, fmt.Errorf("用途说明不能超过 4000 个字符")
		}
	}
	if patch.Tags != nil {
		next.Tags, err = normalizeStringList(*patch.Tags, 32, 64, "标签")
		if err != nil {
			return nil, err
		}
	}
	if patch.Language != nil {
		next.Language = strings.TrimSpace(*patch.Language)
		if utf8.RuneCountInString(next.Language) > 64 {
			return nil, fmt.Errorf("语言不能超过 64 个字符")
		}
	}
	if patch.Usage != nil {
		next.Usage = strings.TrimSpace(*patch.Usage)
		if utf8.RuneCountInString(next.Usage) > 20000 {
			return nil, fmt.Errorf("调用示例不能超过 20000 个字符")
		}
	}
	if patch.Dependencies != nil {
		next.Dependencies, err = normalizeStringList(*patch.Dependencies, 64, 128, "依赖")
		if err != nil {
			return nil, err
		}
	}
	if patch.RuntimeSpec != nil {
		next.RuntimeSpec = strings.TrimSpace(*patch.RuntimeSpec)
		if utf8.RuneCountInString(next.RuntimeSpec) > 500 {
			return nil, fmt.Errorf("运行时说明不能超过 500 个字符")
		}
	}
	expected := patch.ExpectedUpdatedAt
	if expected.IsZero() {
		expected = current.UpdatedAt
	}
	updated, err := s.db.UpdateSharedAssetMetaIfCurrent(ctx, &next, expected)
	if err != nil {
		return nil, err
	}
	after, _ := json.Marshal(map[string]any{
		"display_name": updated.DisplayName, "description": updated.Description, "tags": updated.Tags,
		"language": updated.Language, "usage": updated.Usage, "dependencies": updated.Dependencies,
		"runtime_spec": updated.RuntimeSpec,
	})
	detail, _ := json.Marshal(map[string]any{"before": json.RawMessage(before), "after": json.RawMessage(after)})
	_ = s.db.CreateSharedAssetEvent(ctx, &db.SharedAssetEvent{AssetID: id, Action: "update_metadata", Detail: detail})
	return updated, nil
}

func validateDisplayName(raw, fallback string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		name = strings.TrimSpace(fallback)
	}
	if name == "" {
		return "", fmt.Errorf("显示名称不能为空")
	}
	if utf8.RuneCountInString(name) > 120 {
		return "", fmt.Errorf("显示名称不能超过 120 个字符")
	}
	return name, nil
}

func normalizeStringList(raw []string, maxItems, maxRunes int, label string) ([]string, error) {
	if len(raw) > maxItems {
		return nil, fmt.Errorf("%s不能超过 %d 项", label, maxItems)
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		if utf8.RuneCountInString(value) > maxRunes {
			return nil, fmt.Errorf("%s单项不能超过 %d 个字符", label, maxRunes)
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
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
		target := dst
		if info, err := os.Stat(dst); err == nil && info.IsDir() {
			target = filepath.Join(dst, filepath.Base(src))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
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
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		} else if entry.IsDir() {
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
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

func (s *Service) Versions(ctx context.Context, assetID int64) ([]*db.SharedAssetVersion, error) {
	asset, err := s.db.SharedAsset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, fmt.Errorf("共享资产不存在")
	}
	return s.db.ListSharedAssetVersions(ctx, assetID)
}

func (s *Service) Events(ctx context.Context, assetID int64) ([]*db.SharedAssetEvent, error) {
	asset, err := s.db.SharedAsset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, fmt.Errorf("共享资产不存在")
	}
	return s.db.ListSharedAssetEvents(ctx, assetID)
}

type AssetFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Dir        bool      `json:"dir"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type AssetFile struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Binary     bool      `json:"binary"`
	TooLarge   bool      `json:"too_large"`
	Content    string    `json:"content,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
}

const maxSharedPreview = 2 << 20

func (s *Service) resolveVersionRoot(ctx context.Context, assetID int64, version int) (string, *db.SharedAssetVersion, error) {
	asset, err := s.db.SharedAsset(ctx, assetID)
	if err != nil {
		return "", nil, err
	}
	if asset == nil {
		return "", nil, fmt.Errorf("共享资产不存在")
	}
	if version == 0 {
		version = asset.CurrentVersion
	}
	if version <= 0 {
		return "", nil, fmt.Errorf("共享版本不存在")
	}
	record, err := s.db.SharedAssetVersion(ctx, assetID, version)
	if err != nil {
		return "", nil, err
	}
	if record == nil {
		return "", nil, fmt.Errorf("共享版本不存在")
	}
	root, err := filepath.EvalSymlinks(s.VersionDir(assetID, version))
	if err != nil {
		return "", nil, fmt.Errorf("共享版本目录不可用: %w", err)
	}
	return root, record, nil
}

func resolveVersionPath(root, rel string) (string, string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", "", fmt.Errorf("路径越界")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("路径越界")
	}
	if clean == "." {
		clean = ""
	}
	target := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", err
	}
	if !within(root, resolved) {
		return "", "", fmt.Errorf("路径越界")
	}
	return target, filepath.ToSlash(clean), nil
}

func (s *Service) VersionFiles(ctx context.Context, assetID int64, version int, rel string) ([]AssetFileEntry, string, error) {
	root, _, err := s.resolveVersionRoot(ctx, assetID, version)
	if err != nil {
		return nil, "", err
	}
	target, clean, err := resolveVersionPath(root, rel)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("不是目录")
	}
	dirEntries, err := os.ReadDir(target)
	if err != nil {
		return nil, "", err
	}
	out := make([]AssetFileEntry, 0, len(dirEntries))
	for _, entry := range dirEntries {
		entryInfo, err := entry.Info()
		if err != nil {
			continue
		}
		entryRel := filepath.ToSlash(filepath.Join(clean, entry.Name()))
		out = append(out, AssetFileEntry{
			Name: entry.Name(), Path: entryRel, Dir: entryInfo.IsDir(),
			Size: entryInfo.Size(), ModifiedAt: entryInfo.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, clean, nil
}

func (s *Service) ReadVersionFile(ctx context.Context, assetID int64, version int, rel string) (*AssetFile, error) {
	root, _, err := s.resolveVersionRoot(ctx, assetID, version)
	if err != nil {
		return nil, err
	}
	target, clean, err := resolveVersionPath(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("是目录，不能作为文件读取")
	}
	file := &AssetFile{Path: clean, Size: info.Size(), ModifiedAt: info.ModTime()}
	if info.Size() > maxSharedPreview {
		file.Binary = true
		file.TooLarge = true
		return file, nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		file.Binary = true
		return file, nil
	}
	file.Content = string(data)
	return file, nil
}

func (s *Service) VersionDownloadPath(ctx context.Context, assetID int64, version int, rel string) (string, string, error) {
	root, _, err := s.resolveVersionRoot(ctx, assetID, version)
	if err != nil {
		return "", "", err
	}
	target, _, err := resolveVersionPath(root, rel)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("不能直接下载目录")
	}
	return target, filepath.Base(target), nil
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
	entry := filepath.Join(dest, filepath.FromSlash(a.Entrypoint))
	if info, err := os.Stat(entry); err != nil || info.IsDir() {
		return "", fmt.Errorf("共享版本入口文件缺失")
	}
	return entry, nil
}

func (s *Service) SetStatus(ctx context.Context, id int64, status string) error {
	current, err := s.db.SharedAsset(ctx, id)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("共享资产不存在")
	}
	if err := s.db.SetSharedAssetStatus(ctx, id, status); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"from": current.Status, "to": status})
	_ = s.db.CreateSharedAssetEvent(ctx, &db.SharedAssetEvent{AssetID: id, Action: "status", Detail: detail})
	return s.reconcileLibraries()
}

func (s *Service) Env() []string {
	uv := s.FindTool("uv")
	mise := s.FindTool("mise")
	env := []string{
		"GHOUL_SHARED_DIR=" + s.Root(),
		"GHOUL_PYTHONPATH=" + filepath.Join(s.Root(), "lib"),
		"GHOUL_RUNTIME_MODE=prefer-shared",
		"GHOUL_ALLOW_SYSTEM_PYTHON_INSTALL=1",
		"UV_PYTHON_INSTALL_DIR=" + filepath.Join(s.UvRoot(), "python"),
		"UV_PYTHON_BIN_DIR=" + filepath.Join(s.UvRoot(), "bin"),
		"UV_CACHE_DIR=" + filepath.Join(s.UvRoot(), "cache"),
		"UV_PROJECT_ENVIRONMENT=" + filepath.Join(s.VenvsRoot(), "default"),
		"MISE_DATA_DIR=" + filepath.Join(s.MiseRoot(), "data"),
		"MISE_CONFIG_DIR=" + filepath.Join(s.MiseRoot(), "config"),
		"MISE_CACHE_DIR=" + filepath.Join(s.MiseRoot(), "cache"),
		"MISE_STATE_DIR=" + filepath.Join(s.MiseRoot(), "state"),
	}
	if info, err := os.Stat(s.Python()); err == nil && !info.IsDir() {
		env = append(env, "GHOUL_PYTHON="+s.Python())
	} else {
		env = append(env, "GHOUL_PYTHON=")
	}
	if uv != "" {
		env = append(env, "GHOUL_UV="+uv)
	} else {
		env = append(env, "GHOUL_UV=")
	}
	if mise != "" {
		env = append(env, "GHOUL_MISE="+mise)
	} else {
		env = append(env, "GHOUL_MISE=")
	}
	if systemPython, err := exec.LookPath("python3"); err == nil {
		env = append(env, "GHOUL_SYSTEM_PYTHON="+systemPython)
	} else if systemPython, err := exec.LookPath("python"); err == nil {
		env = append(env, "GHOUL_SYSTEM_PYTHON="+systemPython)
	}
	path := os.Getenv("PATH")
	parts := []string{filepath.Dir(s.Python()), filepath.Join(s.UvRoot(), "bin"), filepath.Join(s.MiseRoot(), "shims")}
	existing := make([]string, 0, len(parts))
	for _, part := range parts {
		if info, err := os.Stat(part); err == nil && info.IsDir() {
			existing = append(existing, part)
		}
	}
	if len(existing) > 0 {
		path = strings.Join(existing, string(os.PathListSeparator)) + string(os.PathListSeparator) + path
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
	uv := s.FindTool("uv")
	mise := s.FindTool("mise")
	systemPython, _ := exec.LookPath("python3")
	if systemPython == "" {
		systemPython, _ = exec.LookPath("python")
	}
	return map[string]any{
		"available":     err == nil,
		"interpreter":   s.Python(),
		"manager":       "uv",
		"uv":            uv,
		"mise":          mise,
		"system_python": systemPython,
		"venvs_root":    s.VenvsRoot(),
		"uv_root":       s.UvRoot(),
		"mise_root":     s.MiseRoot(),
		"packages":      packages,
		"updated_at":    time.Now(),
	}
}
