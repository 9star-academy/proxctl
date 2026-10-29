package proxy

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/9star-academy/proxctl/internal/config"
)

type eventWriter chan string

func (w eventWriter) Write(data []byte) (int, error) { w <- string(data); return len(data), nil }

func waitForEvent(t *testing.T, events <-chan string, text string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line := <-events:
			if strings.Contains(line, text) {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for log %q", text)
		}
	}
}

func cacheTestCertificate(t *testing.T, s *Server) *x509.CertPool {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"api.local"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	data := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}), certificate...)
	if err := s.CertManager.Cache.Put(context.Background(), "api.local", data); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("invalid test certificate")
	}
	return roots
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestLiveServerReloadsAddedUpdatedAndRemovedRules(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "first") }))
	defer backend.Close()
	newBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "second") }))
	defer newBackend.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	writer, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	writer.CertDir = t.TempDir()
	if err := writer.AddRule(&config.Rule{Domain: "api.local", Type: config.TypeProxy, Target: backend.URL}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Save(); err != nil {
		t.Fatal(err)
	}
	daemonCfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	events := make(eventWriter, 32)
	s := NewServer(daemonCfg, log.New(events, "", 0))
	roots := cacheTestCertificate(t, s)
	httpListener, tlsListener := listenLocal(t), listenLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, httpListener, tlsListener) }()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "api.local"}}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(6 * time.Second):
			t.Error("server did not stop")
		}
	})
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request := func(host string, wantStatus int, wantBody string) {
		t.Helper()
		r, err := http.NewRequest("GET", "https://"+tlsListener.Addr().String()+"/test", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = host
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != wantStatus || (wantBody != "" && string(body) != wantBody) {
			t.Fatalf("host=%s status=%d body=%q err=%v", host, response.StatusCode, body, err)
		}
	}
	request("API.LOCAL:443", 200, "first")
	request("new.local", 404, "")
	if err := writer.AddRule(&config.Rule{Domain: "new.local", Type: config.TypeProxy, Target: backend.URL}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Save(); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, "域名规则已重新加载")
	request("new.local", 200, "first")
	if err := writer.AddRule(&config.Rule{Domain: "api.local", Type: config.TypeProxy, Target: newBackend.URL}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Save(); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, "域名规则已重新加载")
	request("api.local", 200, "second")
	validJSON, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, "规则重载失败")
	request("api.local", 200, "second")
	if err := os.WriteFile(path, validJSON, 0600); err != nil {
		t.Fatal(err)
	}
	if !writer.RemoveRule("api.local") {
		t.Fatal("rule missing")
	}
	if err := writer.Save(); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, "域名规则已重新加载")
	request("api.local", 404, "")
	if err := s.hostPolicy(context.Background(), "api.local"); err == nil {
		t.Fatal("removed host still authorized for ACME")
	}
	if err := s.hostPolicy(context.Background(), "NEW.LOCAL"); err != nil {
		t.Fatal(err)
	}

	redirectClient := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, _ := http.NewRequest("GET", "http://"+httpListener.Addr().String()+"/path?q=1", nil)
	r.Host = "new.local:80"
	response, err := redirectClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 301 || response.Header.Get("Location") != "https://new.local/path?q=1" {
		t.Fatalf("incorrect redirect: %v", response)
	}
}

func TestListenerStartupFailures(t *testing.T) {
	occupied, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	for _, tt := range []struct {
		name                string
		httpPort, httpsPort int
		want                string
	}{
		{"HTTP in use", port, 0, "HTTP 监听失败"},
		{"HTTPS in use", 0, port, "HTTPS 监听失败"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testProxyConfig(t, "127.0.0.1:8080")
			cfg.HTTPPort, cfg.HTTPSPort = tt.httpPort, tt.httpsPort
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := NewServer(cfg, nil).Start(ctx); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	cfg := testProxyConfig(t, "127.0.0.1:8080")
	cfg.CertDir = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(cfg.CertDir, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := NewServer(cfg, nil).Start(context.Background()); err == nil {
		t.Fatal("invalid cert directory accepted")
	}
}

func TestWebSocketUpgradeAndTunnel(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "upgrade missing", 400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		// A single masked text frame from a client, followed by an unmasked reply.
		frame := make([]byte, 8)
		if _, err := io.ReadFull(rw, frame); err != nil {
			return
		}
		if string(frame) != string([]byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2}) {
			return
		}
		conn.Write([]byte{0x81, 2, 'o', 'k'})
	}))
	defer backend.Close()
	front := httptest.NewServer(NewServer(testProxyConfig(t, backend.URL), nil))
	defer front.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: api.local\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("upgrade failed: %v", response)
	}
	if _, err := conn.Write([]byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(reader, reply); err != nil || string(reply) != string([]byte{0x81, 2, 'o', 'k'}) {
		t.Fatalf("reply=%v err=%v", reply, err)
	}
}
