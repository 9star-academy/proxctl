package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestProxyFailureRedactsResponseAndLog(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprint(conn, "raw-transport-secret\r\n")
	}))
	defer backend.Close()

	authority := strings.TrimPrefix(backend.URL, "http://")
	target := "http://review-user:review-password@" + authority + "/private-path?token=review-token"
	cfg := testProxyConfig(t, target)
	var logs bytes.Buffer
	server := NewServer(cfg, log.New(&logs, "", 0))
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "https://api.local/", nil))

	if w.Code != http.StatusBadGateway || w.Body.String() != "Bad Gateway\n" {
		t.Errorf("response status=%d body=%q", w.Code, w.Body.String())
	}
	for _, marker := range []string{"review-user", "review-password", "private-path", "review-token", "raw-transport-secret"} {
		if strings.Contains(logs.String(), marker) {
			t.Errorf("logs contain %q: %s", marker, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "api.local") || !strings.Contains(logs.String(), authority) {
		t.Errorf("logs are missing the affected domain or backend address: %s", logs.String())
	}
}

func TestStaticSiteContainsSymlinks(t *testing.T) {
	parent := t.TempDir()
	site := filepath.Join(parent, "site")
	if err := os.MkdirAll(filepath.Join(site, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"private.txt": "outside marker", "site/index.html": "SPA marker", "site/asset.txt": "asset marker"} {
		if err := os.WriteFile(filepath.Join(parent, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		"absolute.txt":      filepath.Join(parent, "private.txt"),
		"relative.txt":      "../private.txt",
		"outside":           "..",
		"nested/index.html": "../../private.txt",
		"inside.txt":        "asset.txt",
	} {
		if err := os.Symlink(target, filepath.Join(site, name)); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{CertDir: t.TempDir()}
	if err := cfg.AddRule(&config.Rule{Domain: "app.local", Type: config.TypeStatic, StaticDir: site, SPA: true}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, nil)
	for _, tc := range []struct {
		path, wantBody string
		wantStatus     int
	}{
		{"/absolute.txt", "", http.StatusForbidden},
		{"/relative.txt", "", http.StatusForbidden},
		{"/outside/private.txt", "", http.StatusForbidden},
		{"/nested/", "", http.StatusForbidden},
		{"/inside.txt", "asset marker", http.StatusOK},
		{"/dashboard", "SPA marker", http.StatusOK},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			server.ServeHTTP(w, httptest.NewRequest("GET", "https://app.local"+tc.path, nil))
			if w.Code != tc.wantStatus || (tc.wantBody != "" && w.Body.String() != tc.wantBody) || strings.Contains(w.Body.String(), "outside marker") {
				t.Errorf("status=%d body=%q, want %d and %q", w.Code, w.Body.String(), tc.wantStatus, tc.wantBody)
			}
		})
	}

	index := filepath.Join(site, "index.html")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../private.txt", index); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "https://app.local/dashboard", nil))
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "outside marker") {
		t.Errorf("escaped SPA index returned status=%d body=%q", w.Code, w.Body.String())
	}
}
