package hosttools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

type fakeRunner struct{ proxy string }

func (f *fakeRunner) Start(_, _ string) (*db.HostInstallJob, error) {
	return &db.HostInstallJob{ID: "plain"}, nil
}
func (f *fakeRunner) StartTool(_, proxy string) (*db.HostInstallJob, error) {
	f.proxy = proxy
	return &db.HostInstallJob{ID: "proxied"}, nil
}
func (f *fakeRunner) Get(context.Context, string) (*db.HostInstallJob, error) { return nil, nil }
func (f *fakeRunner) List(context.Context) ([]*db.HostInstallJob, error) {
	return []*db.HostInstallJob{}, nil
}
func (f *fakeRunner) Cancel(string) bool { return true }

func TestHelperPassesPerJobProxy(t *testing.T) {
	runner := &fakeRunner{}
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"id":"nmap","proxy":"http://user:pass@proxy:8080"}`))
	rec := httptest.NewRecorder()
	Handler(runner).ServeHTTP(rec, req)
	if rec.Code != 202 || runner.proxy == "" {
		t.Fatalf("code=%d proxy=%q", rec.Code, runner.proxy)
	}
}

func TestProxyIsRedactedFromLogs(t *testing.T) {
	proxy := "http://user:secret@proxy:8080"
	for _, text := range []string{"pip --proxy " + proxy, proxy + " refused"} {
		if got := redactProxy(text, proxy); strings.Contains(got, "secret") || strings.Contains(got, proxy) {
			t.Fatalf("leaked proxy: %s", got)
		}
	}
}
