package proxy

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/9star-academy/proxctl/internal/config"
)

func TestProxyRouting(t *testing.T) {
	// Create mock backend target
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Hit", "true")
		w.Write([]byte("hello from backend"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		CertDir: t.TempDir(),
		Rules:   make(map[string]*config.Rule),
	}
	cfg.AddRule(&config.Rule{
		Domain: "api.local",
		Type:   config.TypeProxy,
		Target: backend.URL,
		CORS:   true,
	})

	server := NewServer(cfg, nil)

	// Test request
	req := httptest.NewRequest(http.MethodGet, "https://api.local/test", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if string(body) != "hello from backend" {
		t.Fatalf("expected body 'hello from backend', got %s", string(body))
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("expected CORS header *")
	}
}

func TestStaticSiteAndSPAFallback(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "index.html")
	os.WriteFile(indexPath, []byte("<h1>SPA App</h1>"), 0644)

	cfg := &config.Config{
		CertDir: t.TempDir(),
		Rules:   make(map[string]*config.Rule),
	}
	cfg.AddRule(&config.Rule{
		Domain:    "app.local",
		Type:      config.TypeStatic,
		StaticDir: tmpDir,
		SPA:       true,
	})

	server := NewServer(cfg, nil)

	// Request nonexistent route /dashboard, should fallback to index.html because SPA=true
	req := httptest.NewRequest(http.MethodGet, "https://app.local/dashboard", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "<h1>SPA App</h1>" {
		t.Fatalf("expected SPA fallback to index.html, got: %s", string(body))
	}
}

func TestBasicAuth(t *testing.T) {
	cfg := &config.Config{
		CertDir: t.TempDir(),
		Rules:   make(map[string]*config.Rule),
	}
	cfg.AddRule(&config.Rule{
		Domain: "admin.local",
		Type:   config.TypeStatic,
		Auth:   "admin:secret",
	})

	server := NewServer(cfg, nil)

	// 1. Without auth header -> 401
	req := httptest.NewRequest(http.MethodGet, "https://admin.local/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// 2. With valid auth header -> 200/404 (authorized)
	req = httptest.NewRequest(http.MethodGet, "https://admin.local/", nil)
	validAuth := base64.StdEncoding.EncodeToString([]byte("admin:secret"))
	req.Header.Set("Authorization", "Basic "+validAuth)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("expected authorized, got 401")
	}
}
