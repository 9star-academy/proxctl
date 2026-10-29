package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/9star-academy/proxctl/internal/config"
)

func testProxyConfig(t *testing.T, target string) *config.Config {
	t.Helper()
	cfg := &config.Config{CertDir: t.TempDir(), Rules: map[string]*config.Rule{}}
	if err := cfg.AddRule(&config.Rule{Domain: "api.local", Type: config.TypeProxy, Target: target}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestForwardedHeadersUseEachRequest(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s|%s", r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"))
	}))
	defer backend.Close()
	s := NewServer(testProxyConfig(t, backend.URL), nil)
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		r := httptest.NewRequest("GET", "https://api.local/test", nil)
		r.RemoteAddr = ip + ":1234"
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
		r.Header.Set("X-Real-IP", "203.0.113.99")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if got, want := w.Body.String(), ip+"|"+ip+"|api.local|https"; got != want {
			t.Errorf("forwarded=%q want=%q", got, want)
		}
	}
}

func TestChangingTargetInvalidatesCachedProxy(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "first") }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "second") }))
	defer second.Close()
	cfg := testProxyConfig(t, first.URL)
	s := NewServer(cfg, nil)
	for _, step := range []struct{ target, want string }{{first.URL, "first"}, {second.URL, "second"}} {
		if err := cfg.AddRule(&config.Rule{Domain: "api.local", Type: config.TypeProxy, Target: step.target}); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "https://api.local/", nil))
		if w.Body.String() != step.want {
			t.Fatalf("response=%q want=%q after target change", w.Body.String(), step.want)
		}
	}
}

func TestAuthenticatedCORS(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "protected") }))
	defer backend.Close()
	cfg := testProxyConfig(t, backend.URL)
	if err := cfg.AddRule(&config.Rule{Domain: "api.local", Type: config.TypeProxy, Target: backend.URL, Auth: "user:pass", CORS: true}); err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, nil)
	for _, tt := range []struct {
		name, method, password string
		preflight              bool
		want                   int
	}{
		{"preflight", "OPTIONS", "", true, 204},
		{"missing credentials", "GET", "", false, 401},
		{"wrong password", "GET", "wrong", false, 401},
		{"ordinary OPTIONS remains protected", "OPTIONS", "", false, 401},
		{"authenticated", "GET", "pass", false, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "https://api.local/", nil)
			r.Header.Set("Origin", "https://app.local")
			if tt.preflight {
				r.Header.Set("Access-Control-Request-Method", "GET")
				r.Header.Set("Access-Control-Request-Headers", "authorization")
			}
			if tt.password != "" {
				r.SetBasicAuth("user", tt.password)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tt.want || w.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Fatalf("status=%d origin=%q want=%d/*", w.Code, w.Header().Get("Access-Control-Allow-Origin"), tt.want)
			}
			if tt.preflight && !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
				t.Fatal("Authorization missing from allowed headers")
			}
		})
	}
}
