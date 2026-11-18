package evidence

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

const evidenceOnePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestReadFindingScreenshot(t *testing.T) {
	runDir := t.TempDir()
	path := filepath.Join(runDir, "shots", "idor.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(evidenceOnePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, mime, resolved, err := readFindingScreenshot(runDir, "shots/idor.png")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || string(got) != string(data) || resolved != path {
		t.Fatalf("screenshot read mismatch: mime=%s resolved=%s bytes=%d", mime, resolved, len(got))
	}
}

func TestReadFindingScreenshotRejectsTraversalAndNonImage(t *testing.T) {
	runDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readFindingScreenshot(runDir, outside); err == nil || !strings.Contains(err.Error(), "工作目录内") {
		t.Fatalf("outside path should be rejected, got %v", err)
	}
	textPath := filepath.Join(runDir, "evidence.txt")
	if err := os.WriteFile(textPath, []byte("command output"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readFindingScreenshot(runDir, "evidence.txt"); err == nil {
		t.Fatal("non-image screenshot should be rejected")
	}
}

func TestParseCaptureManifest(t *testing.T) {
	out := []byte("noise\nGHOUL_CAPTURE_MANIFEST={\"ok\":true,\"route_rewrites\":1}\nmore\n")
	manifest, err := parseCaptureManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := manifest["ok"].(bool); !ok {
		t.Fatalf("manifest=%v", manifest)
	}
	if _, err := parseCaptureManifest([]byte("noise")); err == nil {
		t.Fatal("missing manifest should fail")
	}
	cliOut := []byte("### Result\n{\"ok\":true,\"route_rewrites\":2}\n### Ran Playwright code\n")
	manifest, err = parseCaptureManifest(cliOut)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := manifest["ok"].(bool); !ok {
		t.Fatalf("run-code result manifest=%v", manifest)
	}
}

func TestRenderCapturePlaywrightScriptEmbedsConfig(t *testing.T) {
	cfg, err := json.MarshalIndent(map[string]any{
		"page_url": "https://example.test/item",
		"query":    map[string]string{"id": "2"},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	script, err := renderCapturePlaywrightScript(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	if strings.Contains(text, captureConfigPlaceholder) {
		t.Fatal("capture config placeholder was not replaced")
	}
	if strings.Contains(text, "process.") || strings.Contains(text, "Buffer.from") {
		t.Fatal("capture script still depends on Node globals unavailable in playwright-cli run-code")
	}
	if !strings.Contains(text, `"page_url": "https://example.test/item"`) {
		t.Fatal("capture config was not embedded in the generated script")
	}
	if !strings.Contains(text, "options.postData") || !strings.Contains(text, "document_html") {
		t.Fatal("capture script must support navigation replay and raw HTTP evidence documents")
	}
}

func TestNormalizeFindingCaptureMode(t *testing.T) {
	cases := map[string]string{
		"":                    db.FindingCaptureNativeAction,
		"native_action":       db.FindingCaptureNativeAction,
		"NAVIGATION_REPLAY":   db.FindingCaptureNavigationReplay,
		" raw_http_exchange ": db.FindingCaptureRawHTTPExchange,
	}
	for raw, want := range cases {
		got, err := normalizeFindingCaptureMode(raw)
		if err != nil || got != want {
			t.Fatalf("mode %q => %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := normalizeFindingCaptureMode("custom_html"); err == nil {
		t.Fatal("unknown capture mode must be rejected")
	}
}

func TestRenderRawHTTPExchangeDocumentMarksProvenance(t *testing.T) {
	doc := renderRawHTTPExchangeDocument(
		"重复键 action 载荷位移",
		"响应回显 exfiltrate",
		db.TrafficEvidenceSnapshot{
			SourceTrafficID: "traffic-42",
			URL:             "https://example.test/action?id=<unsafe>",
			Method:          "POST",
			Status:          200,
			ContentType:     "application/json",
			ReqHead:         "Content-Type: application/json",
			RespHead:        "HTTP/1.1 200 OK",
		},
		`{"action":"scan","action":"exfiltrate"}`,
		`{"status":"success","action":"exfiltrate"}`,
	)
	for _, want := range []string{
		"原始 HTTP 交换证据，不是原功能页面渲染截图",
		"traffic-42",
		"https://example.test/action?id=&lt;unsafe&gt;",
		`exfiltrate`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("raw HTTP evidence document missing %q", want)
		}
	}
}

func TestGlobToRegex(t *testing.T) {
	re := regexp.MustCompile(globToRegex("**/autorize.php?action=messages*"))
	if !re.MatchString("https://labs.cai.do/autorize.php?action=messages&user_id=102") {
		t.Fatalf("route regex did not match: %s", re.String())
	}
}
