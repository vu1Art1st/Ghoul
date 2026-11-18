package report

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DeliveryReportSections is the required structure of every finding-level
// report that can be exported as a delivery artifact.
var DeliveryReportSections = []string{
	"概述",
	"影响与危害",
	"受影响范围",
	"复现步骤",
	"证据",
	"PoC",
	"根因分析",
	"修复建议",
}

var (
	deliveryHeadingRE = regexp.MustCompile(`(?m)^##[ \t]+(.+?)[ \t]*$`)
	rawRequestLineRE  = regexp.MustCompile(`(?mi)^[A-Z]{3,10}[ \t]+\S+[ \t]+HTTP/(?:1\.[01]|2(?:\.0)?)[ \t]*$`)
	hostHeaderRE      = regexp.MustCompile(`(?mi)^Host:[ \t]*\S+`)
	fencedBlockRE     = regexp.MustCompile("(?ms)^```[^\\n]*\\r?\\n(.*?)\\r?\\n```[ \\t]*$")
	placeholderRE     = regexp.MustCompile(`(?i)^(待补充|待验证|todo|tbd|n/?a|无|暂无|[-—]+)$`)
)

// ValidationOptions controls protocol-specific report checks.
type ValidationOptions struct {
	RequireHTTPRawRequest bool
}

// ReportSection is one parsed level-two Markdown section.
type ReportSection struct {
	Name string
	Body string
}

// AutoValidationOptions infers whether the report is about an HTTP target.
// Non-HTTP findings can still use the same eight-section structure without
// being forced to invent an HTTP request packet.
func AutoValidationOptions(markdown string) ValidationOptions {
	return ValidationOptions{RequireHTTPRawRequest: LooksLikeHTTPReport(markdown)}
}

// LooksLikeHTTPReport recognizes the common HTTP report vocabulary used by the
// reporter. It intentionally errs on the side of requiring a raw request.
func LooksLikeHTTPReport(markdown string) bool {
	lower := strings.ToLower(markdown)
	if strings.Contains(lower, "http/1.") || strings.Contains(lower, "http/2") ||
		strings.Contains(lower, "https://") || strings.Contains(lower, "http://") ||
		strings.Contains(lower, "host:") || strings.Contains(lower, "burp") ||
		strings.Contains(lower, "yakit") {
		return true
	}
	for _, method := range []string{" get ", " post ", " put ", " patch ", " delete "} {
		if strings.Contains(" "+lower+" ", method) {
			return true
		}
	}
	return false
}

// ValidateDeliveryReportAuto validates both the fixed section contract and the
// HTTP/Yakit request requirement inferred from the report itself.
func ValidateDeliveryReportAuto(markdown string) error {
	return ValidateDeliveryReport(markdown, AutoValidationOptions(markdown))
}

// ValidateDeliveryReport checks that all required sections exist in order,
// contain non-placeholder bodies, and include a Yakit-compatible raw HTTP
// request where the report describes an HTTP vulnerability.
func ValidateDeliveryReport(markdown string, opts ValidationOptions) error {
	sections := ParseDeliverySections(markdown)
	if len(sections) == 0 {
		return fmt.Errorf("报告缺少二级章节，必须包含：%s", strings.Join(DeliveryReportSections, "、"))
	}

	byName := make(map[string]ReportSection, len(sections))
	order := make(map[string]int, len(sections))
	issues := make([]string, 0)
	for i, section := range sections {
		name := normalizeSectionName(section.Name)
		if _, ok := byName[name]; ok {
			issues = append(issues, fmt.Sprintf("章节“%s”重复", name))
			continue
		}
		byName[name] = section
		order[name] = i
	}

	lastIndex := -1
	for _, required := range DeliveryReportSections {
		section, ok := byName[required]
		if !ok {
			issues = append(issues, fmt.Sprintf("缺少章节“## %s”", required))
			continue
		}
		if idx := order[required]; idx < lastIndex {
			issues = append(issues, fmt.Sprintf("章节“## %s”顺序错误", required))
		} else {
			lastIndex = idx
		}
		body := strings.TrimSpace(section.Body)
		if body == "" || placeholderRE.MatchString(body) {
			issues = append(issues, fmt.Sprintf("章节“## %s”正文为空或仅为占位符", required))
		}
	}

	if opts.RequireHTTPRawRequest {
		repro := byName["复现步骤"]
		if len(ExtractHTTPRawRequests(repro.Body)) == 0 {
			issues = append(issues, "HTTP 漏洞的“## 复现步骤”缺少 Yakit 原始请求包（需包含 METHOD /path HTTP/1.x 与 Host 头）")
		}
		poc := byName["PoC"]
		if len(ExtractHTTPRawRequests(poc.Body)) == 0 &&
			!pocReferencesReproduction(poc.Body) {
			issues = append(issues, "HTTP 漏洞的“## PoC”缺少 Yakit 原始请求包；若复用复现步骤，请明确写“复现步骤即为 PoC”")
		}
	}

	for _, section := range sections {
		name := normalizeSectionName(section.Name)
		if !containsString(DeliveryReportSections, name) && strings.TrimSpace(section.Body) == "" {
			issues = append(issues, fmt.Sprintf("章节“## %s”正文为空", name))
		}
	}
	if len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "；"))
	}
	return nil
}

// ParseDeliverySections extracts level-two headings and their bodies. Content
// before the first H2, such as a title and metadata list, is intentionally not
// part of a delivery section.
func ParseDeliverySections(markdown string) []ReportSection {
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	matches := deliveryHeadingRE.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return nil
	}
	sections := make([]ReportSection, 0, len(matches))
	for i, match := range matches {
		name := strings.TrimSpace(markdown[match[2]:match[3]])
		bodyStart := match[1]
		bodyEnd := len(markdown)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		sections = append(sections, ReportSection{
			Name: name,
			Body: strings.TrimSpace(markdown[bodyStart:bodyEnd]),
		})
	}
	return sections
}

// ExtractHTTPRawRequests returns de-duplicated Yakit/Burp-compatible raw HTTP
// request packets from fenced Markdown code blocks.
func ExtractHTTPRawRequests(markdown string) []string {
	matches := fencedBlockRE.FindAllStringSubmatch(strings.ReplaceAll(markdown, "\r\n", "\n"), -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		packet := strings.TrimSpace(match[1])
		if !rawRequestLineRE.MatchString(packet) || !hostHeaderRE.MatchString(packet) {
			continue
		}
		if seen[packet] {
			continue
		}
		seen[packet] = true
		out = append(out, packet)
	}
	return out
}

// CompactDeliveryReport preserves the report's section structure, clips long
// prose per section, and re-attaches every raw HTTP request packet in full.
func CompactDeliveryReport(markdown string, perSectionRunes int) string {
	if perSectionRunes <= 0 {
		perSectionRunes = 1600
	}
	sections := ParseDeliverySections(markdown)
	if len(sections) == 0 {
		return clipRunesWithNotice(strings.TrimSpace(markdown), perSectionRunes*len(DeliveryReportSections))
	}
	var b strings.Builder
	for _, section := range sections {
		body := strings.TrimSpace(section.Body)
		if utf8.RuneCountInString(body) > perSectionRunes {
			body = clipSectionPreservingRequests(body, perSectionRunes)
		}
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", section.Name, body)
	}
	return strings.TrimSpace(b.String())
}

func clipSectionPreservingRequests(body string, limit int) string {
	clipped := clipRunesWithNotice(body, limit)
	for _, packet := range ExtractHTTPRawRequests(body) {
		if strings.Contains(clipped, packet) {
			continue
		}
		clipped += "\n\n```http\n" + packet + "\n```"
	}
	return clipped
}

func clipRunesWithNotice(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "\n\n[本节过长，已省略部分说明；原始请求包已单独保留]"
}

func normalizeSectionName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, "*_`")
	name = strings.TrimSuffix(name, "：")
	name = strings.TrimSuffix(name, ":")
	return strings.TrimSpace(name)
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func pocReferencesReproduction(body string) bool {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "复现步骤即为 poc") ||
		strings.Contains(lower, "复现步骤即为poc") {
		return true
	}
	if !strings.Contains(lower, "复现步骤") {
		if strings.Contains(lower, "yakit") &&
			(strings.Contains(lower, "上文") || strings.Contains(lower, "上述")) &&
			(strings.Contains(lower, "原始请求包") || strings.Contains(lower, "请求包")) {
			return true
		}
		return false
	}
	for _, reference := range []string{"见", "即为", "同上", "上文", "上述", "复用", "引用"} {
		if strings.Contains(lower, reference) {
			return true
		}
	}
	return false
}
