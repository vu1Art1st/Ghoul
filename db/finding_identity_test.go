package db

import "testing"

func TestFindingFingerprintStableAcrossOrderAndVariants(t *testing.T) {
	a := RecordFindingInput{VulnClass: "SQLi", Name: "Search IDOR", AssetIDs: []int64{8, 3}}
	b := RecordFindingInput{VulnClass: "sql注入", Name: " search idor ", AssetIDs: []int64{3, 8}}
	if FindingFingerprint(a) != FindingFingerprint(b) {
		t.Fatal("fingerprint should ignore asset order and normalized class variants")
	}
	if FindingFingerprint(a) == FindingFingerprint(RecordFindingInput{VulnClass: "XSS", Name: "Search IDOR", AssetIDs: []int64{3, 8}}) {
		t.Fatal("different classes must not share a fingerprint")
	}
}

func TestNormalizeVulnClassKnownAliases(t *testing.T) {
	for input, want := range map[string]string{
		"SQLi": "注入", "sql injection": "注入", "永恒之蓝": "已知漏洞",
		"IDOR": "未授权访问", "default credentials": "弱口令", "CVE-2021-41773": "已知漏洞",
	} {
		if got := NormalizeVulnClass(input); got != want {
			t.Errorf("NormalizeVulnClass(%q)=%q, want %q", input, got, want)
		}
	}
}
