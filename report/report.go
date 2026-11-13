// Package report renders a deliverable Markdown report from the exploration
// graph: confirmed findings (with severity/evidence) (docs §15 P5).
package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

// Input bundles what the report needs.
type Input struct {
	Title       string
	Goal        string
	GeneratedAt time.Time
	AssetCounts map[string]int
	Findings    []*db.Node
	Cases       []CaseView
}

type CaseView struct {
	Case     db.FindingCase
	Members  []*db.DBFinding
	Evidence string
}

type findingView struct {
	VulnClass string
	Name      string
	Severity  string
	Summary   string
	PoC       string
}

func parseFinding(n *db.Node) findingView {
	var p struct {
		VulnClass string `json:"vulnclass"`
		Name      string `json:"name"`
		Severity  string `json:"severity"`
		Summary   string `json:"summary"`
		Evidence  struct {
			PoC string `json:"poc"`
		} `json:"evidence"`
	}
	_ = json.Unmarshal(n.Payload, &p)
	return findingView{p.VulnClass, p.Name, p.Severity, p.Summary, p.Evidence.PoC}
}

var sevRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "": 4}

// Markdown renders the report.
func Markdown(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 渗透测试报告 — %s\n\n", nz(in.Title, "未命名任务"))
	fmt.Fprintf(&b, "- **任务目标**：%s\n", nz(in.Goal, "（未指定）"))
	fmt.Fprintf(&b, "- **生成时间**：%s\n\n", in.GeneratedAt.Format("2006-01-02 15:04:05"))

	// summary
	fmt.Fprintf(&b, "## 摘要\n\n")
	fmt.Fprintf(&b, "- 确认发现：**%d** 个\n", len(in.Findings)+len(in.Cases))
	fmt.Fprintf(&b, "- 资产：")
	var types []string
	for t := range in.AssetCounts {
		types = append(types, t)
	}
	sort.Strings(types)
	for i, t := range types {
		if i > 0 {
			b.WriteString("、")
		}
		fmt.Fprintf(&b, "%s %d", t, in.AssetCounts[t])
	}
	b.WriteString("\n\n")

	// findings
	fmt.Fprintf(&b, "## 发现\n\n")
	if len(in.Findings) == 0 && len(in.Cases) == 0 {
		b.WriteString("_本次未确认漏洞。_\n\n")
	} else {
		index := 1
		cases := append([]CaseView(nil), in.Cases...)
		sort.SliceStable(cases, func(i, j int) bool {
			return sevRank[strings.ToLower(cases[i].Case.Severity)] < sevRank[strings.ToLower(cases[j].Case.Severity)]
		})
		for _, item := range cases {
			fmt.Fprintf(&b, "### %d. [%s] %s\n\n", index, strings.ToUpper(nz(item.Case.Severity, "info")), nz(item.Case.Title, "未命名漏洞组"))
			if item.Case.Reason != "" {
				fmt.Fprintf(&b, "**归并理由：** %s\n\n", item.Case.Reason)
			}
			if item.Case.SeverityReason != "" {
				fmt.Fprintf(&b, "**评级依据：** %s\n\n", item.Case.SeverityReason)
			}
			if len(item.Members) > 0 {
				ids := make([]string, 0, len(item.Members))
				for _, member := range item.Members {
					ids = append(ids, fmt.Sprintf("#%d", member.ID))
				}
				fmt.Fprintf(&b, "_合并 Finding：%s_\n\n", strings.Join(ids, "、"))
			}
			b.WriteString(item.Case.Report)
			b.WriteString("\n\n")
			index++
		}
		fs := make([]findingView, 0, len(in.Findings))
		for _, n := range in.Findings {
			fs = append(fs, parseFinding(n))
		}
		sort.SliceStable(fs, func(i, j int) bool { return sevRank[fs[i].Severity] < sevRank[fs[j].Severity] })
		for _, f := range fs {
			fmt.Fprintf(&b, "### %d. [%s] %s\n\n", index, strings.ToUpper(nz(f.Severity, "info")), nz(f.Name, nz(f.VulnClass, "未分类")))
			fmt.Fprintf(&b, "%s\n\n", nz(f.Summary, ""))
			if f.PoC != "" {
				fmt.Fprintf(&b, "**PoC / 证据：**\n\n```\n%s\n```\n\n", f.PoC)
			}
			index++
		}
	}

	return b.String()
}

func nz(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
