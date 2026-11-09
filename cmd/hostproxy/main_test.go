package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestParseConfigRejectsEmptyCIDRs(t *testing.T) {
	if _, err := parseConfig([]string{"-allow-source", " "}); err == nil {
		t.Fatal("expected empty allow-source to fail")
	}
}

func TestAllAllowedRequiresEveryAddress(t *testing.T) {
	prefixes := mustPrefixes(t, "127.0.0.0/8,::1/128")
	if !allAllowed([]netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}, prefixes) {
		t.Fatal("expected loopback addresses to be allowed")
	}
	if allAllowed([]netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("8.8.8.8")}, prefixes) {
		t.Fatal("expected mixed public/loopback addresses to be denied")
	}
}

func TestSOCKS5ConnectRelaysLoopback(t *testing.T) {
	echoLn, stopEcho := startEchoServer(t)
	defer stopEcho()

	proxyLn, stopProxy := startProxy(t, mustPrefixes(t, "127.0.0.1/32"), mustPrefixes(t, "127.0.0.0/8"))
	defer stopProxy()

	conn, err := net.Dial("tcp", proxyLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte{socksVersion, 1, authNone}); err != nil {
		t.Fatal(err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		t.Fatal(err)
	}
	if methodReply[0] != socksVersion || methodReply[1] != authNone {
		t.Fatalf("unexpected method reply: %v", methodReply)
	}

	host, port := targetHostPort(t, echoLn.Addr())
	request := []byte{socksVersion, cmdConnect, 0, atypIPv4}
	request = append(request, host...)
	request = append(request, byte(port>>8), byte(port))
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != socksVersion || reply[1] != repSuccess {
		t.Fatalf("unexpected connect reply: %v", reply)
	}

	payload := []byte("hello through hostproxy")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo=%q want %q", got, payload)
	}
}

func TestSOCKS5DeniesNonLoopbackTarget(t *testing.T) {
	proxyLn, stopProxy := startProxy(t, mustPrefixes(t, "127.0.0.1/32"), mustPrefixes(t, "127.0.0.0/8"))
	defer stopProxy()

	conn, err := net.Dial("tcp", proxyLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte{socksVersion, 1, authNone}); err != nil {
		t.Fatal(err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		t.Fatal(err)
	}

	request := []byte{
		socksVersion, cmdConnect, 0, atypIPv4,
		192, 0, 2, 1,
		0, 80,
	}
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != repNotAllowed {
		t.Fatalf("reply=%v want target-not-allowed", reply)
	}
}

func startEchoServer(t *testing.T) (net.Listener, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	stop := func() {
		cancel()
		_ = ln.Close()
	}
	return ln, stop
}

func startProxy(t *testing.T, sources, targets []netip.Prefix) (net.Listener, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{cfg: config{
		allowSources:     sources,
		allowTargets:     targets,
		dialTimeout:      time.Second,
		handshakeTimeout: time.Second,
	}}
	go func() {
		_ = srv.serve(ctx, ln)
	}()
	stop := func() {
		cancel()
		_ = ln.Close()
	}
	return ln, stop
}

func targetHostPort(t *testing.T, addr net.Addr) ([]byte, uint16) {
	t.Helper()
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected address type %T", addr)
	}
	ip := tcpAddr.IP.To4()
	if ip == nil {
		t.Fatalf("test target is not IPv4: %s", tcpAddr)
	}
	return ip, uint16(tcpAddr.Port)
}

func mustPrefixes(t *testing.T, raw string) []netip.Prefix {
	t.Helper()
	prefixes, err := parsePrefixes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return prefixes
}

func TestWriteReplyLayout(t *testing.T) {
	var buf bytesBuffer
	if err := writeReply(&buf, repSuccess); err != nil {
		t.Fatal(err)
	}
	if len(buf) != 10 || buf[0] != socksVersion || buf[1] != repSuccess {
		t.Fatalf("unexpected reply: %v", []byte(buf))
	}
	if binary.BigEndian.Uint16(buf[8:10]) != 0 {
		t.Fatalf("unexpected bound port: %v", []byte(buf[8:10]))
	}
}

type bytesBuffer []byte

func (b *bytesBuffer) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}
