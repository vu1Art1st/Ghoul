package server

import "testing"

func TestOverallC2Status(t *testing.T) {
	tests := []struct {
		name       string
		instance   string
		cursor     string
		lastError  string
		wantStatus string
	}{
		{name: "healthy connected", instance: "healthy", cursor: "connected", wantStatus: "healthy"},
		{name: "stale error with live stream", instance: "error", cursor: "connected", wantStatus: "healthy"},
		{name: "starting with live stream", instance: "connecting", cursor: "connected", wantStatus: "healthy"},
		{name: "reconnecting stream", instance: "healthy", cursor: "reconnecting", wantStatus: "reconnecting"},
		{name: "persistent stream error", instance: "healthy", cursor: "error", lastError: "websocket closed", wantStatus: "error"},
		{name: "persistent sync error", instance: "error", cursor: "connected", lastError: "health check failed", wantStatus: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := overallC2Status(test.instance, test.cursor, test.lastError); got != test.wantStatus {
				t.Fatalf("overallC2Status() = %q, want %q", got, test.wantStatus)
			}
		})
	}
}
