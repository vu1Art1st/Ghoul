package agent

import (
	"strings"
	"unicode/utf8"
)

type EvidenceGrade string

const (
	EvidenceUnverified EvidenceGrade = "unverified"
	EvidenceVersion    EvidenceGrade = "version_only"
	EvidenceBehavior   EvidenceGrade = "behavior"
	EvidenceReplay     EvidenceGrade = "replay"
)

const evidenceDisciplineGuidance = `

**证据分级与上报门槛**：
- critical/high 必须有可机械复现的原始证据：HTTP 状态码与响应特征、curl/sqlmap/nuclei 输出、命令回显、文件哈希或截图引用。
- 只有版本/banner 匹配不等于漏洞；发行版 backport 可能修补后仍保留旧版本号。版本匹配只能作为线索，行为未验证时最高报 medium。
- 被阻挡时先区分“安全机制”与“外部约束”：输入校验、权限判定、ORM 防护属于安全机制；WAF、网络隔离、组件未部署属于外部约束。外部约束下可上报，但必须在证据里写明前置条件。
- 观察不等于推断。evidence 只放实际看到的请求/响应、命令输出或文件摘要；推断写入 summary，不能伪装成证据。
`

func evidenceHasMechanicalSignal(evidence string) bool {
	lower := strings.ToLower(evidence)
	for _, signal := range []string{
		"http/1.", "http/2", "curl ", "curl -", "sqlmap", "nuclei",
		"status: 200", "status: 403", "status: 301", "status: 302",
		`"code":`, `"status":`, "location:", "set-cookie:", "content-type:",
		"uid=", "whoami", "command output", "命令输出", "sha256", "md5",
	} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func evidenceVersionOnly(evidence string) bool {
	lower := strings.ToLower(evidence)
	version := false
	for _, signal := range []string{"version", "banner", "server:", "x-powered-by", "powered-by"} {
		if strings.Contains(lower, signal) {
			version = true
			break
		}
	}
	return version && !evidenceHasMechanicalSignal(evidence)
}

// EvaluateFindingEvidence is the code-owned evidence gate used before a finding
// is persisted. It returns a correction message instead of silently trusting the
// model's severity claim.
func EvaluateFindingEvidence(vulnClass, severity, evidence string) (EvidenceGrade, string) {
	evidence = strings.TrimSpace(evidence)
	severity = strings.ToLower(strings.TrimSpace(severity))
	n := utf8.RuneCountInString(evidence)
	if n == 0 {
		return EvidenceUnverified, "evidence 不能为空：请提供实际请求/响应、命令输出、日志或文件哈希；推断不能作为漏洞证据"
	}
	mechanical := evidenceHasMechanicalSignal(evidence)
	if evidenceVersionOnly(evidence) {
		return EvidenceVersion, "证据只有版本/banner 匹配，没有行为级验证。请实际触发漏洞并记录响应差异；无法触发时降为 medium 并注明“版本匹配，行为未验证”"
	}
	if severity == "critical" && n < 200 {
		return EvidenceUnverified, "critical 证据至少需要 200 字的可核验原始材料（请求/响应、命令输出或回显）；若只有间接推断，请降级"
	}
	if severity == "high" && n < 120 {
		return EvidenceUnverified, "high 证据至少需要 120 字的可核验原始材料（请求/响应、命令输出或回显）；若只有间接推断，请降级"
	}
	if (severity == "critical" || severity == "high") && !mechanical {
		return EvidenceUnverified, "critical/high 证据缺少可机械复现的凭据（HTTP 状态码、curl/sqlmap/nuclei 输出、命令回显或文件哈希）。请实际触发并补全证据，或降为 medium"
	}
	lowerClass := strings.ToLower(strings.TrimSpace(vulnClass))
	if strings.Contains(lowerClass, "credential") || strings.Contains(lowerClass, "默认口令") || strings.Contains(lowerClass, "弱口令") {
		if !strings.Contains(strings.ToLower(evidence), "login") &&
			!strings.Contains(evidence, "登录成功") &&
			!strings.Contains(strings.ToLower(evidence), "set-cookie") &&
			!strings.Contains(strings.ToLower(evidence), "token") {
			return EvidenceUnverified, "凭据类漏洞必须记录实际登录成功或获得有效会话的机械证据；仅凭页面提示、默认值或猜测不能作为确认漏洞"
		}
	}
	if mechanical {
		if strings.Contains(strings.ToLower(evidence), "curl ") && strings.Contains(strings.ToLower(evidence), "http/") {
			return EvidenceReplay, ""
		}
		return EvidenceBehavior, ""
	}
	return EvidenceUnverified, ""
}
