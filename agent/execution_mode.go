package agent

import "strings"

type ExecutionMode string

const (
	ExecutionModeDefault           ExecutionMode = "default"
	ExecutionModeVulnerabilityOnly ExecutionMode = "vulnerability_only"
)

func detectExecutionMode(goal string) ExecutionMode {
	lower := strings.ToLower(goal)
	for _, needle := range []string{
		"仅漏洞", "只测漏洞", "漏洞专项", "不拿权限", "不获取权限", "不横向", "不建立立足点",
		"vulnerability only", "vuln-only", "no foothold", "no lateral movement",
	} {
		if strings.Contains(lower, needle) {
			return ExecutionModeVulnerabilityOnly
		}
	}
	return ExecutionModeDefault
}

func executionModeBlock(mode ExecutionMode) string {
	if mode != ExecutionModeVulnerabilityOnly {
		return ""
	}
	return "\n\n【执行模式：仅漏洞验证（最高优先级约束）】：本任务只做漏洞发现、验证和证据固化，" +
		"不得为获取主机控制权而执行持久化、凭据转储、横向移动、提权、隧道或植入操作。" +
		"若验证需要最小化利用，只执行证明漏洞成立所必需的动作，并在证据中记录边界。"
}
