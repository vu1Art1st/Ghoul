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

func TestMarkdownUsesDetailedReportForUnmergedFinding(t *testing.T) {
	reportBody := `## 概述

演示接口存在越权读取。

## 影响与危害

高敏感数据可被批量读取，严重等级 high。

## 受影响范围

GET /api/orders

## 复现步骤

` + "```http" + `
GET /api/orders?user_id=102 HTTP/1.1
Host: demo.test
Authorization: Bearer test
` + "```" + `

## 证据

响应返回了其他用户订单。

## PoC

` + "```http" + `
GET /api/orders?user_id=102 HTTP/1.1
Host: demo.test
Authorization: Bearer test
` + "```" + `

## 根因分析

服务端未校验对象归属。

## 修复建议

从会话读取用户身份并执行对象级授权校验。
`
	md := Markdown(Input{
		Title: "task", Goal: "verify task markdown",
		GeneratedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
		DetailedFindings: []*db.DBFinding{{
			ID: 581, Name: "订单接口 IDOR", VulnClass: "IDOR", Severity: "high",
			Summary: "用 john 的 token 读取 bob 的订单", Report: reportBody,
		}},
		Warnings: []string{"历史报告结构待复核"},
	})
	for _, want := range []string{
		"报告完整性告警", "历史报告结构待复核", "详细报告：",
		"## 概述", "## 影响与危害", "## 受影响范围", "## 复现步骤",
		"## 证据", "## PoC", "## 根因分析", "## 修复建议",
		"GET /api/orders?user_id=102 HTTP/1.1",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("task markdown missing %q:\n%s", want, md)
		}
	}
}
