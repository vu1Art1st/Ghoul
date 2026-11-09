// Command hostproxy is a small SOCKS5 CONNECT proxy intended to run in the
// host network namespace. It lets bridge-network containers reach services
// that are bound to the host's loopback interface.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	socksVersion = byte(0x05)
	authNone     = byte(0x00)
	authNoMethod = byte(0xff)

	cmdConnect = byte(0x01)

	atypIPv4   = byte(0x01)
	atypDomain = byte(0x03)
	atypIPv6   = byte(0x04)

	repSuccess          = byte(0x00)
	repGeneralFailure   = byte(0x01)
	repNotAllowed       = byte(0x02)
	repNetworkUnreach   = byte(0x03)
	repHostUnreach      = byte(0x04)
	repConnectionRefuse = byte(0x05)
	repTTLExpired       = byte(0x06)
	repCommandNotSup    = byte(0x07)
	repAddressNotSup    = byte(0x08)
)

var errNoAuthMethod = errors.New("client did not offer no-auth")

type config struct {
	listen           string
	allowSources     []netip.Prefix
	allowTargets     []netip.Prefix
	dialTimeout      time.Duration
	handshakeTimeout time.Duration
}

type destination struct {
	host   string
	port   uint16
	isName bool
}

func (d destination) String() string {
	return net.JoinHostPort(d.host, strconv.Itoa(int(d.port)))
}

type server struct {
	cfg config
}

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		log.Fatalf("[hostproxy] listen %s: %v", cfg.listen, err)
	}
	defer ln.Close()

	log.Printf("[hostproxy] listening on %s sources=%s targets=%s",
		cfg.listen, formatPrefixes(cfg.allowSources), formatPrefixes(cfg.allowTargets))

	if err := (&server{cfg: cfg}).serve(context.Background(), ln); err != nil {
		log.Fatalf("[hostproxy] serve: %v", err)
	}
}

func parseConfig(args []string) (config, error) {
	fs := flag.NewFlagSet("hostproxy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	listen := fs.String("listen", "0.0.0.0:11080", "TCP listen address")
	sources := fs.String("allow-source", "127.0.0.1/32,172.16.0.0/12,::1/128", "comma-separated source CIDRs")
	targets := fs.String("allow-target", "127.0.0.0/8,::1/128", "comma-separated target CIDRs")
	dialTimeout := fs.Duration("dial-timeout", 10*time.Second, "upstream dial timeout")
	handshakeTimeout := fs.Duration("handshake-timeout", 10*time.Second, "SOCKS handshake timeout")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	allowSources, err := parsePrefixes(*sources)
	if err != nil {
		return config{}, fmt.Errorf("invalid -allow-source: %w", err)
	}
	allowTargets, err := parsePrefixes(*targets)
	if err != nil {
		return config{}, fmt.Errorf("invalid -allow-target: %w", err)
	}
	if strings.TrimSpace(*listen) == "" {
		return config{}, errors.New("-listen must not be empty")
	}
	if *dialTimeout <= 0 || *handshakeTimeout <= 0 {
		return config{}, errors.New("timeouts must be positive")
	}

	return config{
		listen:           strings.TrimSpace(*listen),
		allowSources:     allowSources,
		allowTargets:     allowTargets,
		dialTimeout:      *dialTimeout,
		handshakeTimeout: *handshakeTimeout,
	}, nil
}

func parsePrefixes(raw string) ([]netip.Prefix, error) {
	parts := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if len(prefixes) == 0 {
		return nil, errors.New("at least one CIDR is required")
	}
	return prefixes, nil
}

func formatPrefixes(prefixes []netip.Prefix) string {
	parts := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		parts = append(parts, prefix.String())
	}
	return strings.Join(parts, ",")
}

func (s *server) serve(ctx context.Context, ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remote, ok := remoteIP(conn.RemoteAddr())
	if !ok || !ipAllowed(remote, s.cfg.allowSources) {
		log.Printf("[hostproxy] denied source %s", conn.RemoteAddr())
		return
	}

	_ = conn.SetDeadline(time.Now().Add(s.cfg.handshakeTimeout))
	if err := acceptNoAuth(conn); err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			log.Printf("[hostproxy] handshake from %s: %v", remote, err)
		}
		return
	}

	dest, err := readConnectRequest(conn)
	if err != nil {
		_ = writeReply(conn, replyForError(err))
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			log.Printf("[hostproxy] request from %s: %v", remote, err)
		}
		return
	}

	addrs, err := resolveDestination(ctx, dest, s.cfg.dialTimeout)
	if err != nil {
		_ = writeReply(conn, replyForError(err))
		log.Printf("[hostproxy] resolve %s from %s: %v", dest, remote, err)
		return
	}
	if !allAllowed(addrs, s.cfg.allowTargets) {
		_ = writeReply(conn, repNotAllowed)
		log.Printf("[hostproxy] denied target %s from %s", dest, remote)
		return
	}

	_ = conn.SetDeadline(time.Time{})
	upstream, err := dialDestination(ctx, addrs, dest.port, s.cfg.dialTimeout)
	if err != nil {
		_ = writeReply(conn, replyForError(err))
		log.Printf("[hostproxy] dial %s from %s: %v", dest, remote, err)
		return
	}
	defer upstream.Close()

	if err := writeReply(conn, repSuccess); err != nil {
		return
	}
	relay(conn, upstream)
}

func acceptNoAuth(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return fmt.Errorf("unsupported SOCKS version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	for _, method := range methods {
		if method == authNone {
			_, err := conn.Write([]byte{socksVersion, authNone})
			return err
		}
	}
	_, _ = conn.Write([]byte{socksVersion, authNoMethod})
	return errNoAuthMethod
}

func readConnectRequest(conn net.Conn) (destination, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return destination{}, err
	}
	if header[0] != socksVersion {
		return destination{}, fmt.Errorf("unsupported request version %d", header[0])
	}
	if header[1] != cmdConnect {
		return destination{}, fmt.Errorf("unsupported command %d", header[1])
	}

	dest := destination{}
	switch header[3] {
	case atypIPv4:
		buf := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return destination{}, err
		}
		addr, ok := netip.AddrFromSlice(buf)
		if !ok {
			return destination{}, errors.New("invalid IPv4 address")
		}
		dest.host = addr.Unmap().String()
	case atypIPv6:
		buf := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return destination{}, err
		}
		addr, ok := netip.AddrFromSlice(buf)
		if !ok {
			return destination{}, errors.New("invalid IPv6 address")
		}
		dest.host = addr.String()
	case atypDomain:
		length, err := readByte(conn)
		if err != nil {
			return destination{}, err
		}
		if length == 0 {
			return destination{}, errors.New("empty domain")
		}
		buf := make([]byte, int(length))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return destination{}, err
		}
		dest.host = string(buf)
		dest.isName = true
	default:
		return destination{}, fmt.Errorf("unsupported address type %d", header[3])
	}

	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return destination{}, err
	}
	dest.port = binary.BigEndian.Uint16(port)
	if dest.port == 0 {
		return destination{}, errors.New("empty target port")
	}
	return dest, nil
}

func readByte(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func resolveDestination(ctx context.Context, dest destination, timeout time.Duration) ([]netip.Addr, error) {
	if !dest.isName {
		addr, err := netip.ParseAddr(dest.host)
		if err != nil {
			return nil, fmt.Errorf("parse target address: %w", err)
		}
		return []netip.Addr{addr.Unmap()}, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, dest.host)
	if err != nil {
		return nil, fmt.Errorf("lookup %s: %w", dest.host, err)
	}

	seen := make(map[netip.Addr]bool, len(ips))
	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if !seen[addr] {
			seen[addr] = true
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("lookup %s: no addresses", dest.host)
	}
	return addrs, nil
}

func dialDestination(ctx context.Context, addrs []netip.Addr, port uint16, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	var lastErr error
	for _, addr := range addrs {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(addr.String(), strconv.Itoa(int(port))))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no target addresses")
	}
	return nil, lastErr
}

func writeReply(w io.Writer, rep byte) error {
	_, err := w.Write([]byte{
		socksVersion, rep, 0x00,
		atypIPv4, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
	})
	return err
}

func replyForError(err error) byte {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return repConnectionRefuse
	case errors.Is(err, syscall.ENETUNREACH):
		return repNetworkUnreach
	case errors.Is(err, syscall.EHOSTUNREACH):
		return repHostUnreach
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return repTTLExpired
	}
	if strings.Contains(err.Error(), "unsupported command") {
		return repCommandNotSup
	}
	if strings.Contains(err.Error(), "unsupported address type") {
		return repAddressNotSup
	}
	return repGeneralFailure
}

func remoteIP(addr net.Addr) (netip.Addr, bool) {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return netip.Addr{}, false
	}
	ip, ok := netip.AddrFromSlice(tcpAddr.IP)
	if !ok {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

func ipAllowed(addr netip.Addr, prefixes []netip.Prefix) bool {
	addr = addr.Unmap()
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func allAllowed(addrs []netip.Addr, prefixes []netip.Prefix) bool {
	if len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		if !ipAllowed(addr, prefixes) {
			return false
		}
	}
	return true
}

func relay(client, upstream net.Conn) {
	done := make(chan struct{}, 2)
	copyOneWay := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if closeWriter, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
		done <- struct{}{}
	}

	go copyOneWay(upstream, client)
	go copyOneWay(client, upstream)

	<-done
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	_ = upstream.SetDeadline(time.Now().Add(2 * time.Second))
	<-done
}
