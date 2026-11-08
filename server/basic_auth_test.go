package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type memoryBasicAuthStore struct {
	values map[string]string
}

func (s *memoryBasicAuthStore) GetSetting(key string) (string, bool, error) {
	value, ok := s.values[key]
	return value, ok, nil
}

func (s *memoryBasicAuthStore) SetSettingContext(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func enabledBasicAuthServer(t *testing.T) *Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(basicAuthConfig{
		Enabled: true, Username: "entry", PasswordHash: string(hash), Revision: "revision-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryBasicAuthStore{values: map[string]string{basicAuthKey: string(raw)}}
	s := &Server{jwtKey: []byte("0123456789abcdef0123456789abcdef")}
	if err := s.basicAuth.load(store); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBasicAuthGateChallengeAndCookie(t *testing.T) {
	s := enabledBasicAuthServer(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := s.requireBasicAuth(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("unauthenticated response: code=%d challenge=%q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("entry", "test-password")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid basic auth code=%d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != basicAuthCookie || !cookies[0].HttpOnly {
		t.Fatalf("missing HttpOnly gate cookie: %+v", cookies)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("gate cookie rejected: %d", rec.Code)
	}
}

func TestBasicAuthValidationAndRevisionRevokesCookie(t *testing.T) {
	if validBasicUsername("") || validBasicUsername("bad:name") || validBasicUsername("bad\nname") {
		t.Fatal("invalid Basic Auth username accepted")
	}
	if !validBasicUsername("operator") {
		t.Fatal("valid Basic Auth username rejected")
	}
	s := enabledBasicAuthServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	cfg := s.basicAuth.snapshot()
	if err := s.setBasicAuthCookie(rec, req, cfg); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	req.AddCookie(cookie)
	if !s.validBasicAuthCookie(req, cfg) {
		t.Fatal("fresh cookie should be valid")
	}
	cfg.Revision = "revision-2"
	if s.validBasicAuthCookie(req, cfg) {
		t.Fatal("revision change did not revoke old cookie")
	}
}
