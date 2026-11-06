package cli

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/9star-academy/proxctl/internal/config"
)

func TestDoctorReportsDNSBackendAndTLS(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(backend.Close)
	front := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(front.Close)
	_, port, err := net.SplitHostPort(front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	a, out, errOut := testApp(t)
	target := strings.Replace(backend.URL, "://", "://test-user:secret-marker@", 1) + "?token=query-marker"
	rule := &config.Rule{Domain: "127.0.0.1", Type: config.TypeProxy, Target: target}
	if err := a.Config.AddRule(rule); err != nil {
		t.Fatal(err)
	}
	if err := a.Config.Save(); err != nil {
		t.Fatal(err)
	}
	path := a.Config.Path()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	code := a.Run(context.Background(), []string{"--config", path, "doctor", rule.Domain,
		"--expect-ip", "127.0.0.1", "--https-port", port})
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	for _, want := range []string{"[正常] 配置", "[正常] DNS", "[正常] 后端", "[失败] HTTPS", "证书", "不受信任"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	for _, secret := range []string{"test-user", "secret-marker", "query-marker"} {
		if strings.Contains(out.String()+errOut.String(), secret) {
			t.Error("doctor output disclosed the upstream URL credentials")
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("doctor altered config permissions: %v %v", info, err)
	}
}

func TestDoctorDistinguishesDNSMismatchAndClosedBackend(t *testing.T) {
	a, out, errOut := testApp(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := listener.Addr().String()
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	rule := &config.Rule{Domain: "127.0.0.1", Type: config.TypeProxy, Target: target}
	if err := a.Config.AddRule(rule); err != nil {
		t.Fatal(err)
	}
	if err := a.Config.Save(); err != nil {
		t.Fatal(err)
	}
	code := a.Run(context.Background(), []string{"--config", a.Config.Path(), "doctor", rule.Domain,
		"--expect-ip", "192.0.2.1", "--https-port", port})
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
	}
	for _, want := range []string{"[失败] DNS", "192.0.2.1", "[失败] 后端", "拒绝连接", "[失败] HTTPS"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestDoctorHTTPSReportsVerifiedCertificateExpiry(t *testing.T) {
	front := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(front.Close)
	a, out, _ := testApp(t)
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: doctorTimeout},
		Config:    front.Client().Transport.(*http.Transport).TLSClientConfig,
	}
	if !a.checkDoctorHTTPS(context.Background(), dialer, front.Listener.Addr().String()) {
		t.Fatalf("HTTPS check failed: %s", out)
	}
	if want := front.Certificate().NotAfter.UTC().Format(time.RFC3339); !strings.Contains(out.String(), want) {
		t.Errorf("HTTPS check omitted expiry %s: %s", want, out)
	}
}

func TestDoctorValidatesArgumentsBeforeReadingConfig(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"help", []string{"--help"}, 0, "proxctl doctor"},
		{"missing domain", nil, 1, "proxctl doctor"},
		{"URL", []string{"https://example.com"}, 1, "域名无效"},
		{"invalid IP", []string{"example.com", "--expect-ip", "wrong"}, 1, "--expect-ip"},
		{"invalid port", []string{"example.com", "--https-port", "65536"}, 1, "--https-port"},
		{"extra", []string{"example.com", "extra"}, 1, "doctor"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, out, errOut := testApp(t)
			code := a.Run(context.Background(), append([]string{"--config", a.Config.Path(), "doctor"}, tt.args...))
			if code != tt.code || !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errOut)
			}
		})
	}
}
