package agent

import (
	"strings"
	"testing"
)

func TestDetectExecutionMode(t *testing.T) {
	for _, goal := range []string{
		"漏洞专项：只验证 SQL 注入",
		"仅漏洞验证，不拿权限，不横向",
		"Vulnerability only; no foothold and no lateral movement",
	} {
		if got := detectExecutionMode(goal); got != ExecutionModeVulnerabilityOnly {
			t.Fatalf("goal %q mode=%q", goal, got)
		}
	}
	if got := detectExecutionMode("完成授权靶场的内网横向与提权"); got != ExecutionModeDefault {
		t.Fatalf("default mode=%q", got)
	}
	if block := executionModeBlock(ExecutionModeDefault); block != "" {
		t.Fatalf("default mode emitted block: %q", block)
	}
	if block := executionModeBlock(ExecutionModeVulnerabilityOnly); !strings.Contains(block, "不得") {
		t.Fatalf("vulnerability-only block missing hard boundary: %q", block)
	}
}
