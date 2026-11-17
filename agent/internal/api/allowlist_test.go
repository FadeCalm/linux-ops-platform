package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPAllowlistMatches(t *testing.T) {
	a, err := newIPAllowlist([]string{"10.0.0.5", "192.168.1.0/24", "fd00::5"})
	if err != nil {
		t.Fatalf("newIPAllowlist: %v", err)
	}
	cases := []struct {
		remote string
		want   bool
	}{
		{"10.0.0.5:1234", true},
		{"10.0.0.6:1234", false},
		{"192.168.1.77:9000", true},
		{"192.168.2.1:9000", false},
		{"[fd00::5]:1234", true},
		{"[fd00::6]:1234", false},
		{"127.0.0.1:1234", false},
		{"not-an-address", false},
	}
	for _, tc := range cases {
		if got := a.allows(tc.remote); got != tc.want {
			t.Errorf("allows(%q) = %v, want %v", tc.remote, got, tc.want)
		}
	}
}

func TestIPAllowlistMiddleware(t *testing.T) {
	a, err := newIPAllowlist([]string{"10.0.0.5"})
	if err != nil {
		t.Fatalf("newIPAllowlist: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "10.0.0.6:5555"
	a.middleware(next).ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want 403", denied.Code)
	}

	allowed := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "10.0.0.5:5555"
	a.middleware(next).ServeHTTP(allowed, req)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d, want 200", allowed.Code)
	}
}

func TestIPAllowlistIgnoresForwardedFor(t *testing.T) {
	a, err := newIPAllowlist([]string{"10.0.0.5"})
	if err != nil {
		t.Fatalf("newIPAllowlist: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "10.0.0.6:5555"
	req.Header.Set("X-Forwarded-For", "10.0.0.5")
	a.middleware(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (X-Forwarded-For must not be trusted)", rec.Code)
	}
}
