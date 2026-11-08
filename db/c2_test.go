package db

import (
	"encoding/json"
	"testing"
)

func TestC2HashesAreStable(t *testing.T) {
	raw := json.RawMessage(`{"id":"s1","status":"active"}`)
	if C2RawHash(raw) != C2RawHash(raw) {
		t.Fatal("raw hash must be deterministic")
	}
	payload := json.RawMessage(`{"id":"s1"}`)
	if C2EventKey("session_online", 100, payload) != C2EventKey("session_online", 100, payload) {
		t.Fatal("event key must be deterministic")
	}
	if C2EventKey("session_online", 100, payload) == C2EventKey("session_offline", 100, payload) {
		t.Fatal("event type must participate in the key")
	}
}
