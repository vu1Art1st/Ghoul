// Package hosttools is the single catalog for environment inventory and installs.
package hosttools

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Kind string

const (
	KindApt    Kind = "apt"
	KindGitHub Kind = "github"
	KindPython Kind = "python"
	KindManual Kind = "manual"
)

type Tool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	Category    string `json:"category"`
	Kind        Kind   `json:"kind"`
	Package     string `json:"package,omitempty"`
	Import      string `json:"import,omitempty"`
	Available   bool   `json:"available"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	Installable bool   `json:"installable"`
	Install     string `json:"install,omitempty"`
}

var catalog = []Tool{
	{ID: "nmap", Name: "nmap", Purpose: "端口扫描与服务指纹", Category: "network", Kind: KindApt, Package: "nmap"},
	{ID: "masscan", Name: "masscan", Purpose: "大规模端口快扫", Category: "network", Kind: KindApt, Package: "masscan"},
	{ID: "curl", Name: "curl", Purpose: "HTTP 请求与调试", Category: "runtime", Kind: KindApt, Package: "curl"},
	{ID: "openssl", Name: "openssl", Purpose: "TLS 证书与加解密", Category: "network", Kind: KindApt, Package: "openssl"},
	{ID: "dig", Name: "dig", Purpose: "DNS 查询", Category: "dns", Kind: KindApt, Package: "dnsutils"},
	{ID: "ssh", Name: "ssh", Purpose: "登录验证与隧道", Category: "network", Kind: KindApt, Package: "openssh-client"},
	{ID: "python3", Name: "python3", Purpose: "脚本执行与自定义 PoC", Category: "runtime", Kind: KindApt, Package: "python3"},
	{ID: "node", Name: "node", Purpose: "Node.js 与 MCP 运行时", Category: "runtime", Kind: KindApt, Package: "nodejs"},
	{ID: "sqlmap", Name: "sqlmap", Purpose: "SQL 注入检测", Category: "web", Kind: KindApt, Package: "sqlmap"},
	{ID: "ffuf", Name: "ffuf", Purpose: "目录与参数模糊枚举", Category: "web", Kind: KindApt, Package: "ffuf"},
	{ID: "gobuster", Name: "gobuster", Purpose: "目录、DNS 与 vhost 枚举", Category: "web", Kind: KindApt, Package: "gobuster"},
	{ID: "nikto", Name: "nikto", Purpose: "Web 服务器安全检查", Category: "web", Kind: KindApt, Package: "nikto"},
	{ID: "nuclei", Name: "nuclei", Purpose: "基于模板的漏洞扫描", Category: "web", Kind: KindGitHub, Package: "projectdiscovery/nuclei"},
	{ID: "httpx", Name: "httpx", Purpose: "Web 存活与指纹探测", Category: "web", Kind: KindGitHub, Package: "projectdiscovery/httpx"},
	{ID: "subfinder", Name: "subfinder", Purpose: "子域名枚举", Category: "dns", Kind: KindGitHub, Package: "projectdiscovery/subfinder"},
	{ID: "naabu", Name: "naabu", Purpose: "快速端口扫描", Category: "network", Kind: KindGitHub, Package: "projectdiscovery/naabu"},
	{ID: "dnsx", Name: "dnsx", Purpose: "DNS 解析与记录提取", Category: "dns", Kind: KindGitHub, Package: "projectdiscovery/dnsx"},
	{ID: "katana", Name: "katana", Purpose: "Web 爬虫", Category: "web", Kind: KindGitHub, Package: "projectdiscovery/katana"},
	{ID: "wafw00f", Name: "wafw00f", Purpose: "WAF 识别", Category: "web", Kind: KindPython, Package: "wafw00f"},
	{ID: "ddddocr", Name: "ddddocr", Purpose: "验证码识别 Python 库", Category: "runtime", Kind: KindPython, Package: "ddddocr", Import: "ddddocr"},
	{ID: "requests", Name: "requests", Purpose: "Python HTTP 客户端库", Category: "runtime", Kind: KindPython, Package: "requests", Import: "requests"},
	{ID: "pymongo", Name: "pymongo", Purpose: "MongoDB Python 客户端库", Category: "middleware", Kind: KindPython, Package: "pymongo", Import: "pymongo"},
	{ID: "paho-mqtt", Name: "paho-mqtt", Purpose: "MQTT Python 客户端库", Category: "middleware", Kind: KindPython, Package: "paho-mqtt", Import: "paho.mqtt.client"},
	{ID: "seclists", Name: "seclists", Purpose: "通用字典与爆破语料", Category: "material", Kind: KindManual, Install: "git clone --depth 1 https://github.com/danielmiessler/SecLists.git /opt/SecLists"},
}

func Lookup(id string) (Tool, bool) {
	for _, t := range catalog {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}

func Inventory(python string) []Tool {
	out := make([]Tool, 0, len(catalog))
	for _, t := range catalog {
		t.Installable = t.Kind != KindManual && (runtime.GOOS == "linux" || t.Kind == KindPython)
		if t.Kind == KindPython && t.Import != "" {
			if python != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				cmd := exec.CommandContext(ctx, python, "-c", "import importlib.util,sys;sys.exit(0 if importlib.util.find_spec(sys.argv[1]) else 1)", t.Import)
				t.Available = cmd.Run() == nil
				cancel()
				if t.Available {
					t.Path = python
				}
			}
		} else if path, err := exec.LookPath(t.ID); err == nil {
			t.Available, t.Path = true, path
		}
		out = append(out, t)
	}
	return out
}

func SummaryText(python string) string {
	var b strings.Builder
	count := 0
	for _, t := range Inventory(python) {
		if !t.Available {
			continue
		}
		count++
		b.WriteString("- " + t.ID + ": " + t.Purpose + "\n")
	}
	if count == 0 {
		return ""
	}
	return b.String()
}
