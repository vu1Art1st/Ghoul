// Package evidence preserves finding evidence independently of disposable traffic.
package evidence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
)

type Store struct {
	DB      *db.DB
	Traffic *traffic.Traffic
	Dir     string
}

const maxFindingScreenshotBytes = 20 << 20
const captureConfigPlaceholder = "__GHOUL_CAPTURE_CONFIG__"

//go:embed capture_playwright.js
var capturePlaywrightScript string

func renderCapturePlaywrightScript(rawCfg []byte) ([]byte, error) {
	if len(rawCfg) == 0 {
		return nil, errors.New("截图配置为空")
	}
	if !strings.Contains(capturePlaywrightScript, captureConfigPlaceholder) {
		return nil, errors.New("截图脚本缺少配置占位符")
	}
	script := strings.Replace(capturePlaywrightScript, captureConfigPlaceholder, string(rawCfg), 1)
	return []byte(script), nil
}

func New(pg *db.DB, tr *traffic.Traffic, dir string) *Store {
	return &Store{DB: pg, Traffic: tr, Dir: dir}
}

func hashPath(dir, hash string) (string, error) {
	if len(hash) != 64 {
		return "", errors.New("invalid evidence hash")
	}
	if _, err := hex.DecodeString(hash); err != nil || strings.ToLower(hash) != hash {
		return "", errors.New("invalid evidence hash")
	}
	return filepath.Join(dir, "blobs", hash[:2], hash+".bin"), nil
}

func verifyFile(path, hash string, length int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != length || hex.EncodeToString(h.Sum(nil)) != hash {
		return fmt.Errorf("证据正文校验失败: %s", hash)
	}
	return nil
}

// A new body becomes visible only after a durable write. Failed SQL commits may
// leave unreferenced files; GC reaps those after a full day's grace period.
func (s *Store) writeBody(r io.Reader, expectedLength int64, expectedHash string) (hash string, err error) {
	stage := filepath.Join(s.Dir, ".staging")
	if err = os.MkdirAll(stage, 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(stage, "body-")
	if err != nil {
		return "", err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return "", err
	}
	hash = hex.EncodeToString(h.Sum(nil))
	if n != expectedLength {
		return "", fmt.Errorf("正文不完整: 预期 %d 字节，读取 %d 字节", expectedLength, n)
	}
	if expectedHash != "" && expectedHash != hash {
		return "", errors.New("原始流量正文哈希不匹配")
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	path, err := hashPath(s.Dir, hash)
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(path); err == nil {
		if err = verifyFile(path, hash, n); err != nil {
			return "", err
		}
		// Refresh the grace period for a restored but not-yet-committed body.
		now := time.Now()
		return hash, os.Chtimes(path, now, now)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer d.Close()
	return hash, d.Sync()
}

// StageFindingsExport freezes bindings/report versions and makes private body
// copies before the HTTP response is started. The caller owns and removes dest.
func (s *Store) StageFindingsExport(ctx context.Context, findings []*db.DBFinding, dest string, copyBodies bool) error {
	return s.DB.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var snapshots []db.TrafficEvidenceSnapshot
		for _, f := range findings {
			list, err := db.FindingTrafficTx(tx, f.ID)
			if err != nil {
				return err
			}
			f.TrafficBindings = list.Bindings
			f.TrafficCount = len(list.Bindings)
			f.EvidenceVersion = list.Version
			f.ReportEvidenceVersion = list.ReportVersion
			if err = tx.QueryRow(`SELECT report FROM findings WHERE id=$1`, f.ID).Scan(&f.Report); err != nil {
				return err
			}
			for _, b := range list.Bindings {
				snapshots = append(snapshots, b.Snapshot)
			}
		}
		if copyBodies {
			return s.copySnapshots(snapshots, dest)
		}
		return nil
	})
}

func (s *Store) prepare(ctx context.Context, refs []db.TrafficRef) ([]db.PreparedTrafficEvidence, error) {
	refs, err := db.NormalizeTrafficRefs(refs)
	if err != nil {
		return nil, err
	}
	out := make([]db.PreparedTrafficEvidence, 0, len(refs))
	if len(refs) == 0 {
		return out, nil
	}
	ids := make([]string, len(refs))
	byID := map[string]db.TrafficRef{}
	for i, ref := range refs {
		ids[i] = ref.TrafficID
		byID[ref.TrafficID] = ref
	}
	err = s.Traffic.ReadEvidence(ctx, ids, func(e traffic.EvidenceExchange) error {
		rh, err := s.writeBody(e.Request, e.ReqLen, e.ReqHash)
		if err != nil {
			return err
		}
		ph, err := s.writeBody(e.Response, e.RespLen, e.RespHash)
		if err != nil {
			return err
		}
		v := db.TrafficEvidenceSnapshot{SourceTrafficID: e.ID, CapturedAt: e.TS, URL: e.URL, Method: e.Method, Status: e.Status, ContentType: e.ContentType,
			ReqHead: e.ReqHead, RespHead: e.RespHead, ReqHash: rh, RespHash: ph, ReqLen: e.ReqLen, RespLen: e.RespLen}
		// Raw wire bytes: normalize once here so the ID, the stored row and every
		// downstream consumer (archive, API responses) all see the same text.
		v = v.Normalize()
		v.ID = db.TrafficSnapshotID(v)
		out = append(out, db.PreparedTrafficEvidence{Ref: byID[e.ID], Snapshot: v})
		return nil
	})
	return out, err
}

func (s *Store) Record(ctx context.Context, in db.RecordFindingInput, refs []db.TrafficRef) (out *db.RecordedFinding, err error) {
	err = s.DB.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := db.LockTaskEvidenceTx(tx, in.TaskID); err != nil {
			return err
		}
		prepared, err := s.prepare(ctx, refs)
		if err != nil {
			return err
		}
		out, err = db.RecordFindingTx(ctx, tx, in, prepared)
		return err
	})
	return
}

// RecordFindingScreenshot copies one real screenshot from the worker's intent
// workspace into finding_evidence as a data URL. Keeping the image in the
// evidence row makes it survive task cleanup and archive/restore with the rest of
// the Finding evidence. The path must stay inside the intent workspace.
func (s *Store) RecordFindingScreenshot(ctx context.Context, in db.FindingScreenshotInput) (*db.FindingEvidence, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("证据存储不可用")
	}
	finding, err := s.DB.GetFinding(in.FindingID)
	if err != nil {
		return nil, err
	}
	if finding == nil {
		return nil, db.ErrFindingNotFound
	}
	if finding.TaskID == nil || *finding.TaskID <= 0 {
		return nil, errors.New("Finding 无所属任务")
	}
	taskID := *finding.TaskID
	if in.TaskID > 0 && in.TaskID != taskID {
		return nil, errors.New("截图与 Finding 不属于同一任务")
	}
	in.TaskID = taskID

	data, mime, resolvedPath, err := readFindingScreenshot(in.RunDir, in.Path)
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{
		"source":        "agent_screenshot",
		"provenance":    "unverified_file",
		"original_name": filepath.Base(resolvedPath),
		"mime":          mime,
		"original_size": len(data),
		"source_url":    strings.TrimSpace(in.SourceURL),
		"note":          strings.TrimSpace(in.Note),
		"task_id":       taskID,
	})
	return s.addFindingScreenshot(ctx, in.FindingID, in.Title, resolvedPath, data, mime, metadata)
}

func (s *Store) addFindingScreenshot(ctx context.Context, findingID int64, title, resolvedPath string, data []byte, mime string, metadata []byte) (*db.FindingEvidence, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(resolvedPath), filepath.Ext(resolvedPath))
	}
	content := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	item, _, err := s.DB.AddFindingEvidence(ctx, findingID, nil, "agent", "确认漏洞后保存关键页面截图", db.FindingEvidence{
		Kind: "screenshot", Title: title, Content: content, Size: int64(len(content)), MetadataJSON: metadata,
	})
	return item, err
}

func normalizeFindingCaptureMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", db.FindingCaptureNativeAction:
		return db.FindingCaptureNativeAction, nil
	case db.FindingCaptureNavigationReplay:
		return db.FindingCaptureNavigationReplay, nil
	case db.FindingCaptureRawHTTPExchange:
		return db.FindingCaptureRawHTTPExchange, nil
	default:
		return "", fmt.Errorf("不支持的截图模式 %q", strings.TrimSpace(raw))
	}
}

func (s *Store) readTrafficBody(snapshot db.TrafficEvidenceSnapshot, side string, limit int64) (string, error) {
	f, _, err := s.OpenBody(snapshot, side)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(data), "\uFFFD"), nil
}

func renderRawHTTPExchangeDocument(title, note string, snapshot db.TrafficEvidenceSnapshot, requestBody, responseBody string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "原始 HTTP 交换证据"
	}
	note = strings.TrimSpace(note)
	doc := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><style>
body{margin:0;background:#111827;color:#e5e7eb;font:14px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace}
main{max-width:1180px;margin:28px auto;padding:0 28px 40px}h1{font:700 24px/1.3 system-ui,sans-serif;color:#fbbf24}
.meta{display:grid;grid-template-columns:150px 1fr;gap:6px 16px;margin:18px 0;padding:16px;background:#1f2937;border-radius:8px}
.meta b{color:#93c5fd}.note{padding:12px 16px;border-left:4px solid #fbbf24;background:#27272a;white-space:pre-wrap}
h2{margin-top:26px;font:700 17px/1.3 system-ui,sans-serif;color:#f9fafb}.head,.body{margin:8px 0 0;padding:14px;background:#0b1220;border:1px solid #374151;border-radius:6px;white-space:pre-wrap;word-break:break-word;overflow-wrap:anywhere}
.tag{display:inline-block;padding:2px 8px;border-radius:999px;background:#7f1d1d;color:#fecaca;font:700 12px system-ui,sans-serif}</style></head><body><main>`
	doc += `<h1>` + html.EscapeString(title) + `</h1><div class="tag">原始 HTTP 交换证据，不是原功能页面渲染截图</div>`
	doc += `<div class="meta"><b>来源流量 ID</b><span>` + html.EscapeString(snapshot.SourceTrafficID) + `</span>`
	doc += `<b>请求</b><span>` + html.EscapeString(snapshot.Method+" "+snapshot.URL) + `</span>`
	doc += `<b>响应状态</b><span>` + html.EscapeString(fmt.Sprintf("%d %s", snapshot.Status, snapshot.ContentType)) + `</span></div>`
	if note != "" {
		doc += `<div class="note">` + html.EscapeString(note) + `</div>`
	}
	doc += `<h2>Request headers</h2><pre class="head">` + html.EscapeString(snapshot.ReqHead) + `</pre>`
	doc += `<h2>Request body</h2><pre class="body">` + html.EscapeString(requestBody) + `</pre>`
	doc += `<h2>Response headers</h2><pre class="head">` + html.EscapeString(snapshot.RespHead) + `</pre>`
	doc += `<h2>Response body</h2><pre class="body">` + html.EscapeString(responseBody) + `</pre></main></body></html>`
	return doc
}

func (s *Store) rawHTTPExchangeDocument(ctx context.Context, findingID int64, trafficID, title, note string) (string, db.TrafficEvidenceSnapshot, string, error) {
	list, err := s.DB.GetFindingTraffic(ctx, findingID)
	if err != nil {
		return "", db.TrafficEvidenceSnapshot{}, "", err
	}
	if list == nil || len(list.Bindings) == 0 {
		return "", db.TrafficEvidenceSnapshot{}, "", errors.New("Finding 尚未绑定可用于原始 HTTP 证据图的流量")
	}
	var selected *db.FindingTrafficBinding
	trafficID = strings.TrimSpace(trafficID)
	for i := range list.Bindings {
		binding := &list.Bindings[i]
		if trafficID != "" && binding.Snapshot.SourceTrafficID != trafficID {
			continue
		}
		if selected == nil || binding.Role == "proof" {
			selected = binding
		}
		if trafficID != "" || binding.Role == "proof" {
			break
		}
	}
	if selected == nil {
		return "", db.TrafficEvidenceSnapshot{}, "", fmt.Errorf("未找到指定流量 %q", trafficID)
	}
	requestBody, err := s.readTrafficBody(selected.Snapshot, "request", 512<<10)
	if err != nil {
		return "", db.TrafficEvidenceSnapshot{}, "", err
	}
	responseBody, err := s.readTrafficBody(selected.Snapshot, "response", 512<<10)
	if err != nil {
		return "", db.TrafficEvidenceSnapshot{}, "", err
	}
	doc := renderRawHTTPExchangeDocument(title, note, selected.Snapshot, requestBody, responseBody)
	return doc, selected.Snapshot, selected.Snapshot.URL, nil
}

// CaptureFindingScreenshot drives the real browser through playwright-cli. The
// helper opens the original page, rewrites only scoped requests, lets the page's
// own handlers render the response, and records provenance hashes proving that
// the document itself was not replaced.
func (s *Store) CaptureFindingScreenshot(ctx context.Context, in db.FindingScreenshotCaptureInput) (*db.FindingEvidence, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("证据存储不可用")
	}
	finding, err := s.DB.GetFinding(in.FindingID)
	if err != nil {
		return nil, err
	}
	if finding == nil {
		return nil, db.ErrFindingNotFound
	}
	if finding.TaskID == nil || *finding.TaskID <= 0 {
		return nil, errors.New("Finding 无所属任务")
	}
	taskID := *finding.TaskID
	if in.TaskID > 0 && in.TaskID != taskID {
		return nil, errors.New("截图与 Finding 不属于同一任务")
	}
	runDir, err := filepath.Abs(strings.TrimSpace(in.RunDir))
	if err != nil || strings.TrimSpace(in.RunDir) == "" {
		return nil, errors.New("截图缺少本次意图工作目录")
	}
	mode, err := normalizeFindingCaptureMode(in.Mode)
	if err != nil {
		return nil, err
	}
	sourceURL := strings.TrimSpace(in.SourceURL)
	documentHTML := ""
	trafficID := strings.TrimSpace(in.TrafficID)
	switch mode {
	case db.FindingCaptureNativeAction:
		if strings.TrimSpace(in.PageURL) == "" || strings.TrimSpace(in.ActionSelector) == "" ||
			strings.TrimSpace(in.WaitSelector) == "" || strings.TrimSpace(in.RouteMatch) == "" {
			return nil, errors.New("native_action 截图需要 page_url、action_selector、wait_selector 与 route_match")
		}
		if sourceURL == "" {
			sourceURL = strings.TrimSpace(in.PageURL)
		}
	case db.FindingCaptureNavigationReplay:
		if strings.TrimSpace(in.PageURL) == "" || strings.TrimSpace(in.RouteMatch) == "" {
			return nil, errors.New("navigation_replay 截图需要 page_url 与 route_match")
		}
		if strings.TrimSpace(in.HTTPMethod) == "" {
			in.HTTPMethod = http.MethodPost
		}
		in.HTTPMethod = strings.ToUpper(strings.TrimSpace(in.HTTPMethod))
		if in.Headers == nil {
			in.Headers = map[string]string{}
		}
		if strings.TrimSpace(in.ContentType) == "" && strings.TrimSpace(in.RequestBody) != "" {
			in.ContentType = "application/json"
		}
		if sourceURL == "" {
			sourceURL = strings.TrimSpace(in.PageURL)
		}
	case db.FindingCaptureRawHTTPExchange:
		var snapshot db.TrafficEvidenceSnapshot
		documentHTML, snapshot, sourceURL, err = s.rawHTTPExchangeDocument(ctx, in.FindingID, in.TrafficID, in.Title, in.Note)
		if err != nil {
			return nil, err
		}
		trafficID = snapshot.SourceTrafficID
		in.WaitSelector = "body"
	}
	cli, err := exec.LookPath("playwright-cli")
	if err != nil {
		return nil, errors.New("playwright-cli 不可用，无法启动受控浏览器截图")
	}
	sessionDir := filepath.Join(runDir, ".evidence-capture")
	if err = os.MkdirAll(sessionDir, 0o700); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	shotPath := filepath.Join(runDir, "shots", "evidence-"+stamp+".png")
	if err = os.MkdirAll(filepath.Dir(shotPath), 0o755); err != nil {
		return nil, err
	}
	scriptPath := filepath.Join(sessionDir, "capture-"+stamp+".js")
	cfg := map[string]any{
		"capture_mode":          mode,
		"document_html":         documentHTML,
		"page_url":              strings.TrimSpace(in.PageURL),
		"precondition_selector": strings.TrimSpace(in.PreconditionSelector),
		"action_selector":       strings.TrimSpace(in.ActionSelector),
		"wait_selector":         strings.TrimSpace(in.WaitSelector),
		"wait_text":             strings.TrimSpace(in.WaitText),
		"prepare_js":            strings.TrimSpace(in.PrepareJS),
		"route_match":           strings.TrimSpace(in.RouteMatch),
		"route_regex":           "",
		"query":                 in.Query,
		"headers":               in.Headers,
		"http_method":           strings.ToUpper(strings.TrimSpace(in.HTTPMethod)),
		"request_body":          in.RequestBody,
		"content_type":          strings.TrimSpace(in.ContentType),
		"traffic_id":            trafficID,
		"source_url":            sourceURL,
		"screenshot_path":       shotPath,
		"full_page":             in.FullPage,
		"viewport_width":        1440,
		"viewport_height":       1000,
	}
	if routeMatch := strings.TrimSpace(in.RouteMatch); routeMatch != "" {
		cfg["route_regex"] = globToRegex(routeMatch)
	}
	rawCfg, _ := json.MarshalIndent(cfg, "", "  ")
	script, err := renderCapturePlaywrightScript(rawCfg)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(scriptPath, script, 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(scriptPath)

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	session := fmt.Sprintf("ghoul-evidence-%d-%d", in.FindingID, time.Now().UnixNano())
	runCLI := func(dir string, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(runCtx, cli, args...)
		cmd.Dir = dir
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			return out, fmt.Errorf("%s failed: %w: %s", name, runErr, strings.TrimSpace(string(out)))
		}
		return out, nil
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		_ = exec.CommandContext(closeCtx, cli, "-s="+session, "close").Run()
	}()
	if _, err = runCLI(runDir, "browser open", "-s="+session, "open", "about:blank"); err != nil {
		return nil, err
	}
	captureOut, err := runCLI(runDir, "browser capture", "-s="+session, "run-code", "--filename="+scriptPath)
	if err != nil {
		return nil, err
	}
	manifest, err := parseCaptureManifest(captureOut)
	if err != nil {
		return nil, err
	}
	if ok, _ := manifest["ok"].(bool); !ok {
		return nil, fmt.Errorf("浏览器截图失败: %v", manifest["error"])
	}
	data, mime, resolvedPath, err := readFindingScreenshot(runDir, shotPath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	meta := map[string]any{
		"source":            "controlled_browser_capture",
		"provenance":        mode,
		"capture_mode":      mode,
		"mime":              mime,
		"original_size":     len(data),
		"screenshot_sha256": hex.EncodeToString(sum[:]),
		"source_url":        sourceURL,
		"traffic_id":        trafficID,
		"note":              strings.TrimSpace(in.Note),
		"task_id":           taskID,
		"capture":           manifest,
	}
	metadata, _ := json.Marshal(meta)
	return s.addFindingScreenshot(ctx, in.FindingID, in.Title, resolvedPath, data, mime, metadata)
}

func parseCaptureManifest(output []byte) (map[string]any, error) {
	const prefix = "GHOUL_CAPTURE_MANIFEST="
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var manifest map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &manifest); err != nil {
			return nil, fmt.Errorf("浏览器截图清单损坏: %w", err)
		}
		return manifest, nil
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != "### Result" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			raw := strings.TrimSpace(lines[j])
			if raw == "" {
				continue
			}
			var manifest map[string]any
			if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
				return nil, fmt.Errorf("浏览器截图清单损坏: %w", err)
			}
			return manifest, nil
		}
	}
	return nil, errors.New("浏览器截图清单缺失")
}

// globToRegex converts the small glob subset accepted by Playwright routes into
// a regexp used only for response metadata matching.
func globToRegex(pattern string) string {
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, `\*\*`, `.*`)
	quoted = strings.ReplaceAll(quoted, `\*`, `.*`)
	quoted = strings.ReplaceAll(quoted, `\?`, `.`)
	return "^" + quoted + "$"
}

func readFindingScreenshot(runDirInput, pathInput string) ([]byte, string, string, error) {
	runDir, err := filepath.Abs(strings.TrimSpace(runDirInput))
	if err != nil || strings.TrimSpace(runDirInput) == "" {
		return nil, "", "", errors.New("截图缺少本次意图工作目录")
	}
	path := strings.TrimSpace(pathInput)
	if path == "" {
		return nil, "", "", errors.New("截图 path 不能为空")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(runDir, path)
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, "", "", err
	}
	resolvedRun := runDir
	if real, e := filepath.EvalSymlinks(runDir); e == nil {
		resolvedRun = real
	}
	resolvedPath := path
	if real, e := filepath.EvalSymlinks(path); e == nil {
		resolvedPath = real
	}
	rel, err := filepath.Rel(resolvedRun, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", "", errors.New("截图必须位于本次意图工作目录内")
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("截图文件不可读: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", "", errors.New("截图必须是普通文件")
	}
	if info.Size() <= 0 || info.Size() > maxFindingScreenshotBytes {
		return nil, "", "", fmt.Errorf("截图大小必须在 1 字节到 %d MiB 之间", maxFindingScreenshotBytes>>20)
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, "", "", err
	}
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0]))
	switch mime {
	case "image/png", "image/jpeg", "image/gif":
	default:
		return nil, "", "", fmt.Errorf("只接受可嵌入 Word/浏览器的 PNG、JPEG 或 GIF 截图，检测到 %s", mime)
	}
	return data, mime, resolvedPath, nil
}

func (s *Store) Bind(ctx context.Context, findingID int64, refs []db.TrafficRef) (out *db.FindingTraffic, err error) {
	err = s.DB.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := db.LockFindingEvidenceTx(tx, findingID, nil); err != nil {
			return err
		}
		prepared, err := s.prepare(ctx, refs)
		if err != nil {
			return err
		}
		if err = db.AddFindingTrafficTx(tx, findingID, prepared); err != nil {
			return err
		}
		out, err = db.FindingTrafficTx(tx, findingID)
		return err
	})
	return
}

func (s *Store) WithBinding(ctx context.Context, findingID, bindingID int64, fn func(db.FindingTrafficBinding) error) error {
	return s.DB.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		list, err := db.FindingTrafficTx(tx, findingID)
		if err != nil {
			return err
		}
		for _, b := range list.Bindings {
			if b.ID == bindingID {
				return fn(b)
			}
		}
		return db.ErrEvidenceNotFound
	})
}

// Binding resolves one binding's metadata under the evidence lock and releases
// the lock before returning. Callers that then stream a body to a client must
// use this instead of WithBinding: verifyFile+io.Copy is O(body size), so a
// large download (or a slow client) holding WithEvidenceTx would block every
// evidence write process-wide. Reading the blob afterwards is safe — blobs are
// content-addressed and GC only reaps unreferenced files after a 24h grace
// period, and an already-open fd survives an unlink regardless.
func (s *Store) Binding(ctx context.Context, findingID, bindingID int64) (db.FindingTrafficBinding, error) {
	var out db.FindingTrafficBinding
	err := s.WithBinding(ctx, findingID, bindingID, func(b db.FindingTrafficBinding) error {
		out = b
		return nil
	})
	return out, err
}

// OpenBody may be called without holding the evidence lock; see Binding.
func (s *Store) OpenBody(snapshot db.TrafficEvidenceSnapshot, side string) (*os.File, int64, error) {
	hash, length := snapshot.ReqHash, snapshot.ReqLen
	if side == "response" {
		hash, length = snapshot.RespHash, snapshot.RespLen
	} else if side != "request" {
		return nil, 0, errors.New("side 必须为 request 或 response")
	}
	path, err := hashPath(s.Dir, hash)
	if err != nil {
		return nil, 0, err
	}
	if err = verifyFile(path, hash, length); err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path)
	return f, length, err
}

// CopySnapshots is used by both report downloads and portable task archives.
// The destination owns real copies, never links into either disposable store.
func (s *Store) CopySnapshots(ctx context.Context, snapshots []db.TrafficEvidenceSnapshot, dest string) error {
	return s.DB.WithEvidenceTx(ctx, func(*sql.Tx) error { return s.copySnapshots(snapshots, dest) })
}

func (s *Store) copySnapshots(snapshots []db.TrafficEvidenceSnapshot, dest string) error {
	for _, v := range snapshots {
		for _, side := range []string{"request", "response"} {
			if err := func() error {
				f, length, err := s.OpenBody(v, side)
				if err != nil {
					return err
				}
				defer f.Close()
				hash := v.ReqHash
				if side == "response" {
					hash = v.RespHash
				}
				target, err := hashPath(dest, hash)
				if err != nil {
					return err
				}
				if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					return err
				}
				out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
				if os.IsExist(err) {
					return verifyFile(target, hash, length)
				}
				if err != nil {
					return err
				}
				_, copyErr := io.Copy(out, f)
				syncErr := out.Sync()
				closeErr := out.Close()
				return errors.Join(copyErr, syncErr, closeErr)
			}(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) InstallSnapshots(ctx context.Context, snapshots []db.TrafficEvidenceSnapshot, source string) error {
	return s.WithInstalledSnapshots(ctx, snapshots, source, func() error { return nil })
}

// WithInstalledSnapshots pins installed bodies until the metadata restore finishes.
// The callback must not acquire another evidence advisory lock.
func (s *Store) WithInstalledSnapshots(ctx context.Context, snapshots []db.TrafficEvidenceSnapshot, source string, restore func() error) error {
	return s.DB.WithEvidenceTx(ctx, func(*sql.Tx) error {
		for _, v := range snapshots {
			if v.ID != db.TrafficSnapshotID(v) {
				return errors.New("归档证据快照元数据哈希不匹配")
			}
			for _, body := range []struct {
				hash   string
				length int64
			}{{v.ReqHash, v.ReqLen}, {v.RespHash, v.RespLen}} {
				if err := func() error {
					path, err := hashPath(source, body.hash)
					if err != nil {
						return err
					}
					f, err := os.Open(path)
					if err != nil {
						return err
					}
					defer f.Close()
					_, err = s.writeBody(f, body.length, body.hash)
					return err
				}(); err != nil {
					return err
				}
			}
		}
		return restore()
	})
}

func (s *Store) Collect(ctx context.Context, now time.Time) error {
	return s.DB.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE traffic_evidence_snapshots s SET unreferenced_at=$1
WHERE unreferenced_at IS NULL AND NOT EXISTS(SELECT 1 FROM finding_traffic_bindings b WHERE b.snapshot_id=s.id)`, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM traffic_evidence_snapshots s WHERE unreferenced_at<$1
AND NOT EXISTS(SELECT 1 FROM finding_traffic_bindings b WHERE b.snapshot_id=s.id)`, now.Add(-24*time.Hour)); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT req_hash FROM traffic_evidence_snapshots UNION SELECT resp_hash FROM traffic_evidence_snapshots`)
		if err != nil {
			return err
		}
		refs := map[string]bool{}
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				rows.Close()
				return err
			}
			refs[hash] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, root := range []string{filepath.Join(s.Dir, "blobs"), filepath.Join(s.Dir, ".staging")} {
			err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if os.IsNotExist(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				if refs[strings.TrimSuffix(entry.Name(), ".bin")] {
					return nil
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if now.Sub(info.ModTime()) < 24*time.Hour {
					return nil
				}
				return os.Remove(path)
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) RunGC(ctx context.Context) {
	timer := time.NewTicker(time.Hour)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			if err := s.Collect(ctx, now); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("[evidence] cleanup failed: %v", err)
			}
		}
	}
}
