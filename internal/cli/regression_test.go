package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/9star-academy/proxctl/internal/config"
)

func testApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return &App{Version: "test", Config: cfg, Out: out, Err: errOut}, out, errOut
}

func TestCLICommandValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want int
	}{
		{"help", nil, 0}, {"help alias", []string{"help"}, 0}, {"version", []string{"--version"}, 0},
		{"unknown", []string{"unknown"}, 1}, {"empty list", []string{"list"}, 0},
		{"list extra", []string{"ls", "extra"}, 1},
		{"config missing", []string{"--config"}, 1}, {"config empty", []string{"--config="}, 1},
		{"add missing domain", []string{"add"}, 1}, {"add missing target", []string{"add", "api.local"}, 1},
		{"add bad flag", []string{"add", "api.local", "--unknown"}, 1},
		{"add extra", []string{"add", "api.local", "--to", "8080", "extra"}, 1},
		{"add help", []string{"add", "api.local", "--help"}, 0},
		{"invalid domain", []string{"add", "https://api.local", "--to", "8080"}, 1},
		{"invalid target", []string{"add", "api.local", "--to", "bad-port"}, 1},
		{"invalid auth", []string{"add", "api.local", "--to", "8080", "--auth", "user"}, 1},
		{"valid auth", []string{"add", "API.LOCAL", "--to", "8080", "--auth", "user:pass"}, 0},
		{"static missing domain", []string{"static"}, 1}, {"static missing dir", []string{"static", "app.local"}, 1},
		{"static bad flag", []string{"static", "app.local", "--bad"}, 1},
		{"static help", []string{"static", "app.local", "--help"}, 0},
		{"static extra", []string{"static", "app.local", "--dir", "/", "extra"}, 1},
		{"static nonexistent", []string{"static", "app.local", "--dir", "/proxctl-test-does-not-exist"}, 1},
		{"static invalid domain", []string{"static", "bad/domain", "--dir", "/"}, 1},
		{"remove missing domain", []string{"rm"}, 1}, {"remove unknown", []string{"delete", "missing.local"}, 1},
		{"remove extra", []string{"rm", "api.local", "extra"}, 1},
		{"serve extra", []string{"serve", "extra"}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, out, errOut := testApp(t)
			if code := a.Run(context.Background(), tt.args); code != tt.want {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, tt.want, out, errOut)
			}
			if tt.want != 0 && errOut.Len() == 0 {
				t.Fatal("failure had no diagnostic")
			}
		})
	}
}

func TestHelpDoesNotLoadBrokenConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"--help", "--version", "list"} {
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		a, err := New("test", out, errOut)
		if err != nil {
			t.Fatal(err)
		}
		code := a.Run(context.Background(), []string{"--config", path, command})
		if (command == "list") != (code != 0) {
			t.Fatalf("command=%s exit=%d err=%s", command, code, errOut)
		}
	}
	if _, err := New("test", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUnreadableConfigReportsSudoHint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files despite permission bits")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) })
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	a, err := New("test", out, errOut)
	if err != nil {
		t.Fatal(err)
	}
	if code := a.Run(context.Background(), []string{"--config", path, "list"}); code == 0 || !strings.Contains(errOut.String(), "sudo") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestSaveFailuresAreReported(t *testing.T) {
	for _, command := range [][]string{{"add", "api.local", "--to", "8080"}, {"static", "app.local", "--dir", "/"}} {
		a, out, errOut := testApp(t)
		if err := os.Mkdir(a.Config.Path(), 0700); err != nil {
			t.Fatal(err)
		}
		if code := a.Run(context.Background(), command); code == 0 || !strings.Contains(errOut.String(), "保存配置失败") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, out, errOut)
		}
	}
}

func TestServeCancellationAndBindFailure(t *testing.T) {
	a, out, errOut := testApp(t)
	if err := a.Config.Save(); err != nil {
		t.Fatal(err)
	}
	a.Config.CertDir = t.TempDir()
	a.Config.HTTPPort, a.Config.HTTPSPort = 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := a.Run(ctx, []string{"serve"}); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	a.Config.HTTPPort = l.Addr().(*net.TCPAddr).Port
	failCtx, failCancel := context.WithTimeout(context.Background(), time.Second)
	defer failCancel()
	if code := a.Run(failCtx, []string{"serve"}); code == 0 || !strings.Contains(errOut.String(), "监听失败") {
		t.Fatalf("code=%d stderr=%s", code, errOut)
	}
}

func TestExplicitConfigSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.json")
	for _, prefix := range [][]string{{"--config", path}, {"--config=" + path}} {
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		a, err := New("test", out, errOut)
		if err != nil {
			t.Fatal(err)
		}
		args := append(append([]string{}, prefix...), "add", "api.local", "--to", "8080")
		if code := a.Run(context.Background(), args); code != 0 {
			t.Fatalf("exit=%d stderr=%s", code, errOut.String())
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := cfg.GetRule("api.local"); !ok {
			t.Fatal("explicit configuration was not used")
		}
	}
}

func TestRemoveReportsPersistenceFailure(t *testing.T) {
	a, out, errOut := testApp(t)
	// A directory at the file path reliably makes persistence fail on every OS.
	path := filepath.Join(t.TempDir(), "blocked.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddRule(&config.Rule{Domain: "api.local", Type: config.TypeProxy, Target: "127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	a.Config = cfg
	if code := a.Run(context.Background(), []string{"rm", "api.local"}); code == 0 {
		t.Fatalf("reported success: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "保存配置失败") {
		t.Fatalf("missing save failure: %s", errOut.String())
	}
}
