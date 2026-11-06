package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDaemonInstallChecksSystemctl(t *testing.T) {
	for _, failAt := range []string{"", "daemon-reload", "enable", "restart"} {
		t.Run("failure="+failAt, func(t *testing.T) {
			a, out, errOut := testApp(t)
			var calls []string
			a.daemon = &daemonManager{
				unitPath: filepath.Join(t.TempDir(), "proxctl.service"),
				binary:   "/opt/prox ctl/bin%$name",
				run: func(ctx context.Context, args ...string) error {
					calls = append(calls, strings.Join(args, " "))
					if args[0] == failAt {
						return errors.New("systemctl failed")
					}
					return nil
				},
			}
			code := a.Run(context.Background(), []string{"daemon", "install"})
			if failAt != "" {
				if code == 0 || strings.Contains(out.String(), "成功安装") || !strings.Contains(errOut.String(), failAt) {
					t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
				}
				if failAt == "daemon-reload" && len(calls) != 1 {
					t.Fatalf("continued after failure: %v", calls)
				}
				return
			}
			if code != 0 {
				t.Fatal(errOut.String())
			}
			if !reflect.DeepEqual(calls, []string{"daemon-reload", "enable proxctl", "restart proxctl"}) {
				t.Fatal(calls)
			}
			data, err := os.ReadFile(a.daemon.unitPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `ExecStart="/opt/prox ctl/bin%%$$name" --config "`+a.Config.Path()+`" serve`) {
				t.Fatalf("incorrect executable/config quoting: %s", data)
			}
		})
	}
}

func TestDaemonActionsAndWriteFailure(t *testing.T) {
	a, _, errOut := testApp(t)
	a.daemon = &daemonManager{unitPath: filepath.Join(t.TempDir(), "missing", "proxctl.service"), binary: "/usr/bin/proxctl", run: func(context.Context, ...string) error { return errors.New("failed") }}
	for _, args := range [][]string{{}, {"unknown"}, {"status"}, {"start"}, {"stop"}, {"restart"}, {"install"}, {"status", "extra"}} {
		if code := a.Run(context.Background(), append([]string{"daemon"}, args...)); code == 0 {
			t.Fatalf("expected failure for %v", args)
		}
	}
	if errOut.Len() == 0 {
		t.Fatal("failure not explained")
	}
}

func TestDaemonControlsServiceWithBrokenConfig(t *testing.T) {
	a, _, errOut := testApp(t)
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	a.daemon = &daemonManager{run: func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}}
	for _, action := range []string{"status", "stop", "start", "restart"} {
		if code := a.Run(context.Background(), []string{"--config", path, "daemon", action}); code != 0 {
			t.Errorf("%s exit=%d stderr=%s", action, code, errOut)
		}
	}
	if code := a.Run(context.Background(), []string{"--config", path, "daemon", "install"}); code != 1 || !strings.Contains(errOut.String(), "解析配置文件失败") {
		t.Errorf("install exit=%d stderr=%s", code, errOut)
	}
	if !reflect.DeepEqual(calls, []string{"status proxctl", "stop proxctl", "start proxctl", "restart proxctl"}) {
		t.Errorf("systemctl calls=%q", calls)
	}
}
