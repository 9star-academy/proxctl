package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstaller(t *testing.T) {
	script, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, platform, arch, uid, fail, asset string
		success                                bool
	}{
		{"linux amd64", "Linux", "x86_64", "0", "", "proxctl-linux-amd64", true},
		{"linux arm64 sudo", "Linux", "aarch64", "1000", "", "proxctl-linux-arm64", true},
		{"macOS", "Darwin", "arm64", "0", "", "proxctl-darwin-arm64", true},
		{"unsupported OS", "Windows", "amd64", "0", "", "", false},
		{"unsupported architecture", "Linux", "armv7", "0", "", "", false},
		{"download failure", "Linux", "amd64", "0", "download", "proxctl-linux-amd64", false},
		{"install failure", "Linux", "amd64", "0", "install", "proxctl-linux-amd64", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			mocks := map[string]string{
				"uname":  `case "$1" in -s) echo "$PROXCTL_TEST_OS";; -m) echo "$PROXCTL_TEST_ARCH";; esac`,
				"id":     `echo "$PROXCTL_TEST_UID"`,
				"mktemp": `exec /usr/bin/mktemp "$PROXCTL_TEST_TMP/download.XXXXXX"`,
				"sudo":   `exec "$@"`,
				"mv": `if [ "$PROXCTL_TEST_FAIL" = install ]; then exit 1; fi
test "$2" = /usr/local/bin/proxctl || exit 2
cp "$1" "$PROXCTL_TEST_DEST" && rm "$1"`,
				"curl": `if [ "$PROXCTL_TEST_FAIL" = download ]; then exit 22; fi
file=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in -o) file="$2"; shift;; https://*) url="$1";; esac
  shift
done
test "$url" = "https://github.com/9star-academy/proxctl/releases/latest/download/$PROXCTL_TEST_ASSET" || exit 3
test -n "$file" || exit 4
echo binary > "$file"`,
			}
			for name, body := range mocks {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(dir, "installed")
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "PROXCTL_TEST_TMP="+dir, "PROXCTL_TEST_DEST="+dest, "PROXCTL_TEST_OS="+tt.platform, "PROXCTL_TEST_ARCH="+tt.arch, "PROXCTL_TEST_UID="+tt.uid, "PROXCTL_TEST_FAIL="+tt.fail, "PROXCTL_TEST_ASSET="+tt.asset)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tt.success {
				t.Fatalf("success=%v err=%v output=%s", tt.success, err, output)
			}
			if tt.success {
				data, err := os.ReadFile(dest)
				if err != nil || string(data) != "binary\n" {
					t.Fatalf("installed=%q err=%v", data, err)
				}
				info, err := os.Stat(dest)
				if err != nil || info.Mode().Perm() != 0755 {
					t.Fatalf("wrong binary permissions: %v %v", info, err)
				}
			} else if strings.Contains(string(output), "安装成功") {
				t.Fatal("failure reported success")
			}
			remaining, err := filepath.Glob(filepath.Join(dir, "download.*"))
			if err != nil || len(remaining) != 0 {
				t.Fatalf("temporary files leaked: %v err=%v", remaining, err)
			}
		})
	}
}
