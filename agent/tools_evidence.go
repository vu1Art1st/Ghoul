package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// saveFindingScreenshot archives a real browser screenshot from the current
// intent workspace and binds it to an existing Finding. It deliberately stores
// the file through the host-owned evidence recorder so agents cannot point the
// platform at arbitrary local paths.
func (t *ToolSet) saveFindingScreenshot() actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: "save_finding_screenshot",
		Description: "把本次意图工作目录里的真实浏览器截图绑定到已登记的 Finding。" +
			"这是人工复核附件，不会自动进入报告；优先使用 capture_finding_screenshot。" +
			"只接受本次意图目录内的 PNG/JPEG/GIF，最大 20 MiB。",
		Schema: obj(map[string]any{
			"finding_id": idp("report_finding 返回的独立 finding_id（不是 finding_node_id）"),
			"path":       str("本次意图工作目录内的截图路径，例如 shots/idor-user-102.png"),
			"title":      str("截图标题，也是报告图注；说明系统和页面，例如 Autorize Lab：John 越权读取 Bob 私信"),
			"note":       str("该图证明什么；尽量写明当前身份、被篡改参数和返回的敏感结果"),
			"source_url": str("可选：截图对应的目标 URL"),
		}, "finding_id", "path", "title"),
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision {
			return acperm.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			if t.findingRecorder == nil {
				return actool.Errorf("截图证据存储不可用"), nil
			}
			if t.taskID <= 0 {
				return actool.Errorf("save_finding_screenshot 需要任务上下文"), nil
			}
			if tc == nil || strings.TrimSpace(tc.WorkingDir) == "" {
				return actool.Errorf("save_finding_screenshot 缺少本次意图工作目录"), nil
			}
			var args struct {
				FindingID json.RawMessage `json:"finding_id"`
				Path      string          `json:"path"`
				Title     string          `json:"title"`
				Note      string          `json:"note"`
				SourceURL string          `json:"source_url"`
			}
			if err := json.Unmarshal(in, &args); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			findingID := pid(args.FindingID)
			if findingID <= 0 {
				return actool.Errorf("finding_id 必须是 report_finding 返回的独立漏洞 ID"), nil
			}
			item, err := t.findingRecorder.RecordFindingScreenshot(ctx, db.FindingScreenshotInput{
				TaskID: t.taskID, FindingID: findingID, RunDir: tc.WorkingDir,
				Path: args.Path, Title: args.Title, SourceURL: args.SourceURL, Note: args.Note,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out, _ := json.Marshal(map[string]any{
				"evidence_id": item.ID,
				"finding_id":  findingID,
				"kind":        item.Kind,
				"title":       item.Title,
				"artifact":    fmt.Sprintf("artifact:%d", item.ID),
			})
			return actool.Text(fmt.Sprintf(
				"screenshot saved: finding %d evidence %d\n%s\n该截图已进入 Finding 待核验附件；不会自动进入报告，需人工复核。",
				findingID, item.ID, out,
			)), nil
		},
	})
}

// captureFindingScreenshot is the preferred screenshot path. The host owns
// browser launch, request interception and capture provenance, so the model
// cannot pass an arbitrary custom-rendered image as an original-site screenshot.
func (t *ToolSet) captureFindingScreenshot() actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: "capture_finding_screenshot",
		Description: "通过平台受控浏览器生成可审计截图。mode=native_action 时打开原始页面并操作原生控件；" +
			"mode=navigation_replay 时向原始 URL 重放请求并截取浏览器响应；mode=raw_http_exchange 时从已绑定流量生成明确标注的 HTTP 证据图。",
		Schema: obj(map[string]any{
			"finding_id":            idp("report_finding 返回的独立 finding_id"),
			"mode":                  map[string]any{"type": "string", "enum": []string{"native_action", "navigation_replay", "raw_http_exchange"}, "description": "截图模式，默认 native_action"},
			"page_url":              str("原始功能页面 URL"),
			"precondition_selector": str("可选：操作前必须存在的页面基线容器，默认 body"),
			"action_selector":       str("要点击的原始页面元素，例如 button:has-text('Get Messages')"),
			"wait_selector":         str("操作前必须存在、操作后用于等待结果的页面原生容器，例如 #testResponse"),
			"route_match":           str("Playwright 请求匹配规则，例如 **/autorize.php?action=messages*"),
			"http_method":           str("navigation_replay 的请求方法，默认 POST"),
			"request_body":          str("navigation_replay 的请求正文"),
			"content_type":          str("navigation_replay 的 Content-Type"),
			"query":                 map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "需要改写的查询参数，例如 {\"user_id\":\"102\"}"},
			"headers":               map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "可选：只对匹配请求覆盖的请求头，例如 Authorization"},
			"prepare_js":            str("可选：仅用于设置页面已有运行态，例如 currentToken/currentUserId；禁止 document.write、整页 innerHTML 替换"),
			"traffic_id":            str("raw_http_exchange 可选：使用 Finding 已绑定的指定流量 ID；省略时优先选择 proof 流量"),
			"wait_text":             str("可选：等待结果容器出现指定文本，例如 Bob Smith"),
			"title":                 str("截图标题和报告图注"),
			"note":                  str("说明当前身份、被改写参数、页面原生渲染结果"),
			"source_url":            str("可选：截图对应的目标 URL"),
			"full_page":             map[string]any{"type": "boolean", "description": "是否截整页，默认 false"},
		}, "finding_id", "title"),
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision {
			return acperm.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			if t.findingRecorder == nil {
				return actool.Errorf("受控截图证据存储不可用"), nil
			}
			if t.taskID <= 0 {
				return actool.Errorf("capture_finding_screenshot 需要任务上下文"), nil
			}
			if tc == nil || strings.TrimSpace(tc.WorkingDir) == "" {
				return actool.Errorf("capture_finding_screenshot 缺少本次意图工作目录"), nil
			}
			var args struct {
				FindingID            json.RawMessage   `json:"finding_id"`
				Mode                 string            `json:"mode"`
				PageURL              string            `json:"page_url"`
				PreconditionSelector string            `json:"precondition_selector"`
				ActionSelector       string            `json:"action_selector"`
				WaitSelector         string            `json:"wait_selector"`
				WaitText             string            `json:"wait_text"`
				PrepareJS            string            `json:"prepare_js"`
				RouteMatch           string            `json:"route_match"`
				HTTPMethod           string            `json:"http_method"`
				RequestBody          string            `json:"request_body"`
				ContentType          string            `json:"content_type"`
				Query                map[string]string `json:"query"`
				Headers              map[string]string `json:"headers"`
				TrafficID            string            `json:"traffic_id"`
				Title                string            `json:"title"`
				Note                 string            `json:"note"`
				SourceURL            string            `json:"source_url"`
				FullPage             bool              `json:"full_page"`
			}
			if err := json.Unmarshal(in, &args); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			findingID := pid(args.FindingID)
			if findingID <= 0 {
				return actool.Errorf("finding_id 必须是 report_finding 返回的独立漏洞 ID"), nil
			}
			mode := strings.ToLower(strings.TrimSpace(args.Mode))
			if mode == "" {
				mode = db.FindingCaptureNativeAction
			}
			item, err := t.findingRecorder.CaptureFindingScreenshot(ctx, db.FindingScreenshotCaptureInput{
				TaskID: t.taskID, FindingID: findingID, RunDir: tc.WorkingDir,
				Mode: mode, PageURL: args.PageURL, PreconditionSelector: args.PreconditionSelector,
				ActionSelector: args.ActionSelector, WaitSelector: args.WaitSelector,
				WaitText: args.WaitText, PrepareJS: args.PrepareJS, RouteMatch: args.RouteMatch,
				HTTPMethod: args.HTTPMethod, RequestBody: args.RequestBody, ContentType: args.ContentType,
				Query: args.Query, Headers: args.Headers, TrafficID: args.TrafficID, Title: args.Title, Note: args.Note,
				SourceURL: args.SourceURL, FullPage: args.FullPage,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out, _ := json.Marshal(map[string]any{
				"evidence_id": item.ID,
				"finding_id":  findingID,
				"kind":        item.Kind,
				"title":       item.Title,
				"provenance":  mode,
				"artifact":    fmt.Sprintf("artifact:%d", item.ID),
			})
			return actool.Text(fmt.Sprintf(
				"controlled screenshot saved: finding %d evidence %d\n%s\n截图模式、来源 URL 与真实性清单已写入证据元数据。",
				findingID, item.ID, out,
			)), nil
		},
	})
}

const findingScreenshotGuidance = `

**关键截图证据**：确认 Web/HTTP 漏洞并拿到独立 finding_id 后，必须尝试生成至少一张能直接看懂漏洞结果的真实浏览器截图（越权后的数据列表/他人订单或私信、后台页面、上传或命令回显页面等）。
- **首选 ` + "`capture_finding_screenshot`" + ` mode=native_action` + `**：平台打开原始页面、点击原生控件、拦截并改写指定请求参数，让页面自身 JavaScript 渲染响应后截图。
- 纯 API 或 JSON 响应使用 ` + "`mode=navigation_replay`" + `，向原始 URL 发送 ` + "`http_method`" + `/` + "`request_body`" + ` 并截取浏览器响应；这属于重放截图，不得描述为原功能页面。
- 没有可渲染页面时使用 ` + "`mode=raw_http_exchange`" + `；平台从 Finding 已绑定流量生成带来源 ID 和“原始 HTTP 交换证据”标识的图片，不能冒充原网站页面。
- ` + "`save_finding_screenshot`" + ` 仅作为待核验的人工附件，不会自动进入报告。不得用自绘 HTML、拼图或示意图冒充真实复现。
- 画面尽量同时包含：当前账号/身份、被篡改的 user_id/order_id 等参数、越权返回的敏感结果或与正常访问的差异。不得修改服务端响应内容，不要只截首页、空页面或与结论无关的背景。
- 截图必须来自真实复现，禁止生成示意图、拼图伪装结果或引用非本次任务文件。确实无法截图（非 HTTP、无浏览器、环境不可用）时在 evidence 里写明原因，不阻塞正常上报。`
