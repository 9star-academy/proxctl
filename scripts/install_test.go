package scripts

import (
	"crypto/sha256"
	"fmt"
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
		name, platform, arch, uid, version, fail, asset, message string
	}{
		{name: "linux amd64 latest", platform: "Linux", arch: "x86_64", uid: "0", asset: "proxctl-linux-amd64"},
		{name: "linux arm64 sudo pinned", platform: "Linux", arch: "aarch64", uid: "1000", version: "v1.2.3", asset: "proxctl-linux-arm64"},
		{name: "macOS latest", platform: "Darwin", arch: "arm64", uid: "1000", asset: "proxctl-darwin-arm64"},
		{name: "unsupported OS", platform: "Windows", arch: "amd64", uid: "0", message: "不支持的操作系统"},
		{name: "unsupported architecture", platform: "Linux", arch: "armv7", uid: "0", message: "不支持的架构"},
		{name: "invalid version", platform: "Linux", arch: "amd64", uid: "0", version: "../main", message: "版本号无效"},
		{name: "latest redirect must identify a tag", platform: "Linux", arch: "amd64", uid: "0", fail: "redirect", message: "无法确定最新版本"},
		{name: "missing checksum file", platform: "Linux", arch: "amd64", uid: "0", fail: "manifest", asset: "proxctl-linux-amd64", message: "无法下载校验文件"},
		{name: "duplicate checksums", platform: "Linux", arch: "amd64", uid: "0", fail: "duplicate", asset: "proxctl-linux-amd64", message: "校验文件缺少唯一有效的"},
		{name: "download failure", platform: "Linux", arch: "amd64", uid: "0", fail: "download", asset: "proxctl-linux-amd64", message: "无法下载"},
		{name: "corrupt download leaves old executable", platform: "Linux", arch: "amd64", uid: "1000", fail: "checksum", asset: "proxctl-linux-amd64", message: "校验失败"},
		{name: "corrupt staging leaves old executable", platform: "Linux", arch: "amd64", uid: "1000", fail: "staging", asset: "proxctl-linux-amd64", message: "校验失败"},
		{name: "install failure leaves old executable", platform: "Linux", arch: "amd64", uid: "0", fail: "install", asset: "proxctl-linux-amd64", message: "替换失败"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			systemBin := filepath.Join(dir, "system-bin")
			for _, path := range []string{bin, systemBin} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(systemBin, "proxctl")
			if err := os.WriteFile(dest, []byte("old executable\n"), 0755); err != nil {
				t.Fatal(err)
			}
			const privileged = `test "$PROXCTL_TEST_UID" = 0 || test "${PROXCTL_TEST_SUDO:-}" = 1
`
			mocks := map[string]string{
				"uname": `case "$1" in -s) echo "$PROXCTL_TEST_OS";; -m) echo "$PROXCTL_TEST_ARCH";; esac`,
				"id":    `echo "$PROXCTL_TEST_UID"`,
				"mktemp": `if [ "$#" -eq 0 ]; then exec /usr/bin/mktemp "$PROXCTL_TEST_TMP/download.XXXXXX"; fi
test "$1" = -d
case "$2" in
	/usr/local/bin/.proxctl.XXXXXX)
		test "$PROXCTL_TEST_UID" = 0 || test "${PROXCTL_TEST_SUDO:-}" = 1
		exec /usr/bin/mktemp -d "$PROXCTL_TEST_TMP/system-bin/.proxctl.XXXXXX";;
	*/proxctl-download.XXXXXX)
		exec /usr/bin/mktemp -d "$PROXCTL_TEST_TMP/download.XXXXXX";;
	*) exit 2;;
esac`,
				"sudo":  `export PROXCTL_TEST_SUDO=1; exec "$@"`,
				"mkdir": privileged + `test "$1" = -p; test "$2" = /usr/local/bin`,
				"tee": privileged + `case "$1" in "$PROXCTL_TEST_TMP"/system-bin/.proxctl.*/proxctl);; *) exit 2;; esac
if [ "$PROXCTL_TEST_FAIL" = staging ]; then cat >/dev/null; printf 'wrong bytes\n' > "$1"; else cat > "$1"; fi`,
				"mv": privileged + `if [ "$PROXCTL_TEST_FAIL" = install ]; then exit 1; fi
if [ "$#" -eq 2 ]; then exec /bin/mv -f "$1" "$PROXCTL_TEST_DEST"; fi
test "$1" = -f
case "$2" in "$PROXCTL_TEST_TMP"/system-bin/.proxctl.*/proxctl);; *) exit 2;; esac
test "$3" = /usr/local/bin/proxctl
touch "$PROXCTL_TEST_TMP/replaced-from-staging"
exec /bin/mv -f "$2" "$PROXCTL_TEST_DEST"`,
				"curl": `file=""
url=""
format=""
while [ "$#" -gt 0 ]; do
	case "$1" in -o) file="$2"; shift;; -w) format="$2"; shift;; https://*) url="$1";; esac
	shift
done
base="https://github.com/9star-academy/proxctl/releases"
if [ "$url" = "$base/latest" ]; then
	test -z "$PROXCTL_TEST_VERSION"
	test "$file" = /dev/null
	test "$format" = '%{url_effective}'
	if [ "$PROXCTL_TEST_FAIL" = redirect ]; then printf '%s' "$base"; else printf '%s' "$base/tag/v1.2.3"; fi
	exit 0
fi
test -n "$file"
case "$url" in
	"$base/download/v1.2.3/SHA256SUMS")
		if [ "$PROXCTL_TEST_FAIL" = manifest ]; then exit 22; fi
		printf '%s  %s\n' "$PROXCTL_TEST_SHA256" "$PROXCTL_TEST_ASSET" > "$file"
		if [ "$PROXCTL_TEST_FAIL" = duplicate ]; then cat "$file" > "$file.copy"; cat "$file.copy" >> "$file"; fi;;
	"$base/download/v1.2.3/$PROXCTL_TEST_ASSET"|"$base/latest/download/$PROXCTL_TEST_ASSET")
		if [ "$PROXCTL_TEST_FAIL" = download ]; then exit 22; fi
		if [ "$PROXCTL_TEST_FAIL" = checksum ]; then echo corrupt > "$file"; else echo binary > "$file"; fi;;
	*) exit 3;;
esac`,
			}
			for name, body := range mocks {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{script}
			if tt.version != "" {
				args = append(args, "--version", tt.version)
			}
			cmd := exec.Command("bash", args...)
			cmd.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PROXCTL_TEST_TMP="+dir,
				"PROXCTL_TEST_DEST="+dest,
				"PROXCTL_TEST_OS="+tt.platform,
				"PROXCTL_TEST_ARCH="+tt.arch,
				"PROXCTL_TEST_UID="+tt.uid,
				"PROXCTL_TEST_VERSION="+tt.version,
				"PROXCTL_TEST_FAIL="+tt.fail,
				"PROXCTL_TEST_ASSET="+tt.asset,
				fmt.Sprintf("PROXCTL_TEST_SHA256=%x", sha256.Sum256([]byte("binary\n"))),
			)
			output, err := cmd.CombinedOutput()
			want := "old executable\n"
			if tt.message == "" {
				if err != nil {
					t.Fatalf("install failed: %v, output=%s", err, output)
				}
				if _, err := os.Stat(filepath.Join(dir, "replaced-from-staging")); err != nil {
					t.Fatalf("binary was not atomically replaced from privileged staging: %v", err)
				}
				want = "binary\n"
			} else if err == nil || strings.Contains(string(output), "安装成功") || !strings.Contains(string(output), tt.message) {
				t.Fatalf("expected error %q, got %v output=%s", tt.message, err, output)
			}
			data, err := os.ReadFile(dest)
			if err != nil || string(data) != want {
				t.Fatalf("installed=%q, want %q, err=%v", data, want, err)
			}
			info, err := os.Stat(dest)
			if err != nil || info.Mode().Perm() != 0755 {
				t.Fatalf("wrong binary permissions: %v %v", info, err)
			}
			for _, pattern := range []string{filepath.Join(dir, "download.*"), filepath.Join(systemBin, ".proxctl.*")} {
				remaining, err := filepath.Glob(pattern)
				if err != nil || len(remaining) != 0 {
					t.Fatalf("temporary files leaked: %v err=%v", remaining, err)
				}
			}
		})
	}
}
