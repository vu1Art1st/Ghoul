// Package findingverify contains deterministic counter-evidence checklists and
// pivot hints used before a finding is promoted to a verified result.
package findingverify

import (
	"strings"
)

var classKeywords = []struct {
	class string
	keys  []string
}{
	{"cmdi", []string{"命令注入", "命令执行", "代码执行", "commandinjection", "cmdi", "rce"}},
	{"sqli", []string{"sql", "盲注"}},
	{"xss", []string{"xss", "跨站"}},
	{"ssrf", []string{"ssrf"}},
	{"upload", []string{"文件上传", "上传", "upload", "webshell"}},
	{"auth-bypass", []string{"越权", "未授权", "认证绕过", "鉴权", "idor", "bypass", "逻辑漏洞"}},
	{"info-leak", []string{"信息泄露", "敏感信息", "泄露", "leak", "disclosure"}},
}

var generalChecks = []string{
	"200 不等于利用成功：先用良性请求/恶意请求做对照，只有可重复的差异响应才算信号。",
	"结论必须绑定只有成功才会出现的特征，例如独有数据、线性延时、回连或文件落地。",
	"复现失败先排除环境、WAF、权限和时机因素；无法排除时记录 inconclusive，不直接判误报。",
}

var classChecks = map[string][]string{
	"sqli": {
		"布尔盲注必须有同请求在真/假条件下的稳定差异，单次报错不能证明可控。",
		"时间盲注必须有无 payload 基线，并在相同条件下重复至少两次得到对应延时。",
		"报错回显必须包含攻击者可控的字面量或函数结果，不能只有通用 SQL 错误。",
	},
	"xss": {
		"确认 payload 所在上下文与关键字符是否被编码，单纯回显不等于可执行。",
		"存储型必须由独立会话或页面重新读取，确认落地并按预期渲染。",
	},
	"ssrf": {
		"回连必须确认源地址属于目标服务端出口，排除 CDN、浏览器端渲染和本地代理。",
		"仅证明可出网不足以支撑高危，还需验证触达内网、元数据或状态变更接口。",
	},
	"cmdi": {
		"时间盲命令执行必须与设定延时线性对应，单次抖动不能作为结论。",
		"有命令输出或带外回连时优先使用该证据，时间盲只作为降级验证。",
	},
	"upload": {
		"必须同时验证文件落地和解析执行；可下载源码或仅返回上传成功不算利用。",
		"随机路径或无回显场景未确认落地点前不得判 verified。",
	},
	"auth-bypass": {
		"使用两个账号做对照，低权限会话必须读取到高权限账号预埋的唯一私有标记。",
		"读与写分别验证，能读公开或共享数据不构成越权。",
	},
	"info-leak": {
		"样本必须包含目标独有的真实数据，默认页、公开信息和测试数据不算泄露。",
		"仅路径、版本号或 banner 的低敏信息不得据此判高危。",
	},
}

func normalizeClass(v string) string {
	key := strings.ToLower(strings.TrimSpace(v))
	replacer := strings.NewReplacer(" ", "", "\t", "", "-", "", "_", "", "/", "", "、", "", ":", "")
	key = replacer.Replace(key)
	for _, rule := range classKeywords {
		for _, kw := range rule.keys {
			if strings.Contains(key, strings.ToLower(kw)) {
				return rule.class
			}
		}
	}
	return ""
}

// Checklist returns general checks plus class-specific checks.
func Checklist(vulnClass string) []string {
	out := append([]string(nil), generalChecks...)
	if class := normalizeClass(vulnClass); class != "" {
		out = append(out, classChecks[class]...)
	}
	return out
}

var pivotHints = map[string][]string{
	"sqli": {
		"改用布尔/时间盲注基线对照，先证明可控再扩大数据读取。",
		"检查同参数在 JSON、Cookie、Header 等不同注入位置是否存在过滤差异。",
	},
	"xss": {
		"切换到存储型读取链，确认写入后由另一个页面/会话触发。",
		"检查前端框架的 DOM sink 与后端转义边界，避免只测反射回显。",
	},
	"ssrf": {
		"优先验证元数据、内网服务指纹和 DNS rebinding，而不是只证明可出网。",
		"检查重定向、URL 解析差异和协议白名单是否可绕过。",
	},
	"cmdi": {
		"从时间盲切换到带外回连或输出回显，缩短因果链。",
		"检查参数拼接、文件包含、模板执行等相邻执行原语。",
	},
	"upload": {
		"验证落地点、访问路径和解析行为，必要时检查竞争条件与扩展名绕过。",
		"尝试从上传点转向同目录包含、下载或静态资源覆盖。",
	},
	"auth-bypass": {
		"建立双账号对象图，验证跨租户/跨用户的读与写差异。",
		"检查 ID 替换、批量接口、GraphQL 字段级鉴权和缓存键混淆。",
	},
	"info-leak": {
		"从版本/banner 转向真实业务数据样本，证明数据属于目标而非公开内容。",
		"检查备份、日志、调试端点和对象存储权限，避免只停留在目录枚举。",
	},
}

// PivotHints returns class-specific next directions. Tags take priority; when
// absent, the summary is matched against the same class keywords.
func PivotHints(tags []string, summary string, max int) []string {
	if max <= 0 {
		return nil
	}
	classes := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		class := normalizeClass(tag)
		if class != "" && !seen[class] {
			seen[class] = true
			classes = append(classes, class)
		}
	}
	if len(classes) == 0 {
		if class := normalizeClass(summary); class != "" {
			classes = append(classes, class)
		}
	}
	out := []string{}
	for _, class := range classes {
		for _, hint := range pivotHints[class] {
			out = append(out, "["+class+"] "+hint)
			if len(out) >= max {
				return out
			}
		}
	}
	return out
}
