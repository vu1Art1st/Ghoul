package hosttools

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// RemoteFromEnv returns a Unix-socket runner only when explicitly configured.
func RemoteFromEnv(proxyFn ...func() string) JobRunner {
	socket := strings.TrimSpace(os.Getenv("GHOUL_HOSTD_SOCKET"))
	if socket == "" {
		return nil
	}
	var getProxy func() string
	if len(proxyFn) > 0 {
		getProxy = proxyFn[0]
	}
	return RemoteRunner{ProxyFn: getProxy, Client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}}
}
