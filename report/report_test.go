package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestMarkdownRendersFindingCaseAsOneDeliveryItem(t *testing.T) {
	taskID := int64(4)
	md := Markdown(Input{
		Title:       "task4",
		Goal:        "verify merge",
		GeneratedAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC),
		Cases: []CaseView{{
			Case: db.FindingCase{
				ID: 9, TaskID: &taskID, Title: "MCP 网关无鉴权", Reason: "同一匿名 tools/call 入口",
				Severity: db.SeverityCritical, SeverityReason: "无需认证即可调用工具",
				Report: "## 概述\n\nMCP 网关未校验 Authorization。",
			},
			Members: []*db.DBFinding{{ID: 105}, {ID: 108}},
		}},
	})
	for _, want := range []string{
		"确认发现：**1** 个",
		"[CRITICAL] MCP 网关无鉴权",
		"合并 Finding：#105、#108",
		"MCP 网关未校验 Authorization",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}
	if strings.Count(md, "MCP 网关无鉴权") != 1 {
		t.Fatalf("case must be rendered once:\n%s", md)
	}
}
