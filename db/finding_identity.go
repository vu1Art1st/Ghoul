package db

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

func severityRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

// MaxSeverity returns the higher severity while preserving the first value on
// ties or when both values are unknown.
func MaxSeverity(a, b string) string {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

// FindingFingerprint is a stable 128-bit identity for one reported defect:
// normalized vulnerability class, normalized title, and sorted asset anchors.
func FindingFingerprint(in RecordFindingInput) string {
	norm := func(s string) string {
		return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
	}
	ids := append([]int64(nil), in.AssetIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	raw := norm(NormalizeVulnClass(in.VulnClass)) + "|" + norm(in.Name) + "|" + strings.Join(parts, ",")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}
