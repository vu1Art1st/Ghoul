package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/report"
)

func validHTTPDeliveryReport(subject string) string {
	return `## 概述

` + subject + `。

## 影响与危害

可导致未授权数据读取，严重等级 high。

## 受影响范围

` + subject + ` 对应接口。

## 复现步骤

1. 发送 Yakit 原始请求包：

` + "```http" + `
GET /vuln?id=1 HTTP/1.1
Host: example.test
Authorization: Bearer test
` + "```" + `

## 证据

` + "```http" + `
HTTP/1.1 200 OK
Content-Type: application/json
` + "```" + `

## PoC

` + "```http" + `
GET /vuln?id=2 HTTP/1.1
Host: example.test
Authorization: Bearer test
` + "```" + `

## 根因分析

服务端缺少对象级授权校验。

## 修复建议

从会话读取当前用户身份，并统一执行对象级授权检查。
`
}

func TestValidateDeliveryReportRequiresAllSectionsAndYakitPacket(t *testing.T) {
	valid := validHTTPDeliveryReport("IDOR")
	if err := report.ValidateDeliveryReportAuto(valid); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}

	withoutPoCHeading := valid
	const pocHeading = "\n## PoC\n\n```http\nGET /vuln?id=2 HTTP/1.1\nHost: example.test\nAuthorization: Bearer test\n```\n"
	if !strings.Contains(withoutPoCHeading, pocHeading) {
		t.Fatal("test fixture changed unexpectedly")
	}
	withoutPoCHeading = strings.Replace(withoutPoCHeading, pocHeading, "\n", 1)
	if err := report.ValidateDeliveryReportAuto(withoutPoCHeading); err == nil {
		t.Fatal("report without PoC section should be rejected")
	}

	withoutRawPacket := strings.Replace(valid, "GET /vuln?id=1 HTTP/1.1", "GET /vuln?id=1", 1)
	if err := report.ValidateDeliveryReportAuto(withoutRawPacket); err == nil {
		t.Fatal("HTTP report without a raw request packet should be rejected")
	}
}

func TestUpdateFindingCaseRejectsIncompleteReportBeforeDatabaseWrite(t *testing.T) {
	body := `{"version":1,"title":"越权","report":"## 概述\n\n只有概述。","severity":"high","reason":"同根因"}`
	req := httptest.NewRequest(http.MethodPut, "/api/exploration/finding-cases/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	(&Server{}).updateFindingCase(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "交付结构校验") {
		t.Fatalf("unexpected error body: %s", rec.Body.String())
	}
}
