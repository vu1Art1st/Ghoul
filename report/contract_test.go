package report

import (
	"strings"
	"testing"
)

func TestCompactDeliveryReportKeepsRawRequestsBeyondClip(t *testing.T) {
	report := `## 概述

` + strings.Repeat("概述说明", 400) + `

## 影响与危害

高危。

## 受影响范围

/api

## 复现步骤

` + strings.Repeat("前置说明", 500) + `

` + "```http" + `
GET /api/items?user_id=102 HTTP/1.1
Host: demo.test
Authorization: Bearer test
` + "```" + `

## 证据

返回了其他用户数据。

## PoC

` + "```http" + `
GET /api/items?user_id=102 HTTP/1.1
Host: demo.test
Authorization: Bearer test
` + "```" + `

## 根因分析

缺少对象级授权。

## 修复建议

按当前会话主体查询。
`
	compact := CompactDeliveryReport(report, 200)
	if !strings.Contains(compact, "GET /api/items?user_id=102 HTTP/1.1") {
		t.Fatalf("raw HTTP request was lost during compaction:\n%s", compact)
	}
	if len([]rune(compact)) >= len([]rune(report)) {
		t.Fatal("long report was not compacted")
	}
}

func TestValidateDeliveryReportAllowsPoCReferenceToReproduction(t *testing.T) {
	for _, pocBody := range []string{
		"复现步骤中的 Yakit 原始请求包即为 PoC，替换 user_id 即可复用。",
		"Yakit 侧最小 PoC 即上文「步骤 1」中的原始请求包，替换 user_id 即可复用。",
	} {
		report := `## 概述

接口越权。

## 影响与危害

高危。

## 受影响范围

/api/items

## 复现步骤

` + "```http" + `
GET /api/items?user_id=102 HTTP/1.1
Host: demo.test
Authorization: Bearer test
` + "```" + `

## 证据

响应泄露了他人数据。

## PoC

` + pocBody + `

## 根因分析

缺少对象级授权。

## 修复建议

统一校验对象归属。
`
		if err := ValidateDeliveryReportAuto(report); err != nil {
			t.Fatal(err)
		}
	}
}
