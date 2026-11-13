package db

import "testing"

func TestTaskRelationMatchReportsTermsAndConfidence(t *testing.T) {
	reason, confidence := taskRelationMatch(
		"ACME portal",
		"tested example.com and its login component",
		"verify websocket authentication",
		[]string{"example.com", "websocket", "unrelated"},
	)
	if reason != "命中关键词：example.com、websocket" {
		t.Fatalf("reason = %q", reason)
	}
	if confidence != 0.79 {
		t.Fatalf("confidence = %v, want 0.79", confidence)
	}
}
