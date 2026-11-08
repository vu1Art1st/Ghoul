package server

import "testing"

func TestC2SecurityNetworkChecks(t *testing.T) {
	if !c2HostAllowed("http://127.0.0.1:18081", nil) {
		t.Fatal("loopback must be allowed")
	}
	if c2HostAllowed("https://sidecar.example.com", nil) {
		t.Fatal("remote host without allowlist must fail")
	}
	if !c2HostAllowed("https://sidecar.example.com", []string{"sidecar.example.com"}) {
		t.Fatal("explicit allowlist must pass")
	}
	if !c2TransportSafe("http://127.0.0.1:18081", false) {
		t.Fatal("loopback HTTP is the local sidecar default")
	}
	if c2TransportSafe("http://sidecar.example.com", false) {
		t.Fatal("remote HTTP must fail")
	}
	if !c2TransportSafe("https://sidecar.example.com", false) {
		t.Fatal("remote HTTPS must pass")
	}
}
