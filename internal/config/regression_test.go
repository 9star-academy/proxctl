package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func loadTestConfig(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Each child loads its snapshot before the parent releases any writer.
func TestConfigWriterProcess(t *testing.T) {
	path := os.Getenv("PROXCTL_TEST_CONFIG_PATH")
	if path == "" {
		return
	}
	cfg := loadTestConfig(t, path)
	addTestRule(t, cfg, os.Getenv("PROXCTL_TEST_DOMAIN"))
	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(os.Stdin).ReadByte(); err != nil {
		t.Fatal(err)
	}
	saveTestConfig(t, cfg)
}

func TestIndependentProcessesPreserveRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var children []*exec.Cmd
	var releases []io.WriteCloser
	var diagnostics []*bytes.Buffer
	for _, domain := range []string{"first.local", "second.local", "third.local"} {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestConfigWriterProcess$", "-test.timeout=10s")
		cmd.Env = append(os.Environ(), "PROXCTL_TEST_CONFIG_PATH="+path, "PROXCTL_TEST_DOMAIN="+domain)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
		releases = append(releases, stdin)
		diagnostics = append(diagnostics, stderr)
		if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
			t.Fatalf("child readiness=%q err=%v", line, err)
		}
	}
	for _, release := range releases {
		if _, err := release.Write([]byte("go\n")); err != nil {
			t.Fatal(err)
		}
		release.Close()
	}
	for i, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer failed: %v %s", err, diagnostics[i])
		}
	}
	if got := len(loadTestConfig(t, path).ListRules()); got != len(children) {
		t.Fatalf("saved %d/%d process edits", got, len(children))
	}
}

func TestReloadRulesKeepsLastValidSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writer := loadTestConfig(t, path)
	addTestRule(t, writer, "old.local")
	saveTestConfig(t, writer)
	reader := loadTestConfig(t, path)
	writer.HTTPPort = 9080
	writer.RemoveRule("old.local")
	addTestRule(t, writer, "new.local")
	saveTestConfig(t, writer)
	if changed, err := reader.ReloadRules(); !changed || err != nil {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, ok := reader.GetRule("old.local"); ok {
		t.Fatal("removed rule still present")
	}
	if _, ok := reader.GetRule("new.local"); !ok {
		t.Fatal("new rule not loaded")
	}
	if reader.HTTPPort != 80 {
		t.Fatal("reload unexpectedly changed listener settings")
	}
	if changed, err := reader.ReloadRules(); changed || err != nil {
		t.Fatalf("unchanged reload=%v err=%v", changed, err)
	}
	for _, data := range []string{`{`, `null`, `[]`, `{"rules":{"bad.local":null}}`, `{"rules":{"bad.local":{"domain":"different.local","type":"proxy","target":"localhost:8080"}}}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if changed, err := reader.ReloadRules(); changed || err == nil {
			t.Fatalf("invalid configuration accepted: %s", data)
		}
		if _, ok := reader.GetRule("new.local"); !ok {
			t.Fatal("invalid reload replaced last good snapshot")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReloadRules(); err == nil {
		t.Fatal("missing config silently accepted")
	}
	saveTestConfig(t, writer)
	if _, err := reader.ReloadRules(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPathMustBeRegularFile(t *testing.T) {
	dir := t.TempDir()
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("directory accepted as configuration")
	}
	after, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("Load changed directory permissions: %04o -> %04o", before.Mode().Perm(), after.Mode().Perm())
	}
}

func TestDefaultConfigPrefersExistingSystemFile(t *testing.T) {
	home := t.TempDir()
	systemPath := filepath.Join(t.TempDir(), "config.json")
	wantPersonal := filepath.Join(home, ".config", "proxctl", "config.json")
	if got := defaultConfigPath(501, systemPath, home); got != wantPersonal {
		t.Fatal(got)
	}
	if got := defaultConfigPath(0, systemPath, home); got != systemPath {
		t.Fatal(got)
	}
	if err := os.WriteFile(systemPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := defaultConfigPath(501, systemPath, home); got != systemPath {
		t.Fatal(got)
	}
	if got := defaultConfigPath(501, filepath.Join(t.TempDir(), "missing"), ""); got != filepath.Join(".config", "proxctl", "config.json") {
		t.Fatal(got)
	}
}

func TestSettingsMergeAndInvalidFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first, second := loadTestConfig(t, path), loadTestConfig(t, path)
	first.Email = "test@example.com"
	first.CertDir = filepath.Join(t.TempDir(), "certs")
	first.HTTPSPort = 9443
	saveTestConfig(t, first)
	addTestRule(t, second, "api.local")
	saveTestConfig(t, second)
	if second.Email != first.Email || second.CertDir != first.CertDir || second.HTTPSPort != 9443 {
		t.Fatal("stale writer overwrote global settings")
	}
	for _, data := range []string{`{"http_port":-1}`, `{"https_port":65536}`, `{"rules":{"api.local":{"domain":"api.local","type":"other"}}}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if err := os.WriteFile(path, []byte(`{"http_port":0,"https_port":0,"cert_dir":"","rules":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := loadTestConfig(t, path)
	if loaded.HTTPPort != 80 || loaded.HTTPSPort != 443 || loaded.CertDir == "" || loaded.Rules == nil {
		t.Fatal("defaults missing")
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Save(); err == nil {
		t.Fatal("Save overwrote malformed config")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("directory accepted as config")
	}
}

func TestRuleValidation(t *testing.T) {
	for _, rule := range []*Rule{nil,
		{Domain: "", Type: TypeProxy, Target: "localhost:8080"},
		{Domain: "https://api.local", Type: TypeProxy, Target: "localhost:8080"},
		{Domain: "-bad.local", Type: TypeProxy, Target: "localhost:8080"},
		{Domain: "bad..local", Type: TypeProxy, Target: "localhost:8080"},
		{Domain: "api.local", Type: TypeProxy, Target: "localhost:8080", Auth: "invalid"},
		{Domain: "api.local", Type: TypeProxy, Target: "ftp://localhost:8080"},
		{Domain: "api.local", Type: TypeProxy, Target: "http://"},
		{Domain: "api.local", Type: TypeProxy, Target: "localhost:0"},
		{Domain: "api.local", Type: TypeProxy, Target: "localhost:99999"},
		{Domain: "api.local", Type: TypeStatic, StaticDir: "relative"},
		{Domain: "api.local", Type: "unknown"},
	} {
		cfg := loadTestConfig(t, filepath.Join(t.TempDir(), "config.json"))
		if err := cfg.AddRule(rule); err == nil {
			data, _ := json.Marshal(rule)
			t.Fatalf("accepted invalid rule: %s", data)
		}
	}
	for _, target := range []string{"127.0.0.1:8080", "http://localhost", "https://localhost:9443/base?q=1", "[::1]:8080"} {
		if _, err := TargetURL(target); err != nil {
			t.Fatalf("valid target %s rejected: %v", target, err)
		}
	}
}

func addTestRule(t *testing.T, cfg *Config, domain string) {
	t.Helper()
	if err := cfg.AddRule(&Rule{Domain: domain, Type: TypeProxy, Target: "127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
}

func saveTestConfig(t *testing.T, cfg *Config) {
	t.Helper()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "config.json")
	cfg := loadTestConfig(t, path)
	if err := cfg.AddRule(&Rule{Domain: "admin.local", Type: TypeProxy, Target: "127.0.0.1:8080", Auth: "user:example"}); err != nil {
		t.Fatal(err)
	}
	saveTestConfig(t, cfg)
	for _, name := range []string{path, filepath.Dir(path)} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("%s mode=%04o permits other users", name, info.Mode().Perm())
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	loadTestConfig(t, path)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("legacy configuration was not secured: info=%v err=%v", info, err)
	}
}

func TestIndependentWritersMergeChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first, second := loadTestConfig(t, path), loadTestConfig(t, path)
	addTestRule(t, first, "first.local")
	addTestRule(t, second, "second.local")
	saveTestConfig(t, first)
	saveTestConfig(t, second)
	loaded := loadTestConfig(t, path)
	if len(loaded.ListRules()) != 2 {
		t.Fatalf("lost an independent writer's rule: %+v", loaded.ListRules())
	}
	remover, updater := loadTestConfig(t, path), loadTestConfig(t, path)
	if !remover.RemoveRule("first.local") {
		t.Fatal("rule not found")
	}
	addTestRule(t, updater, "third.local")
	saveTestConfig(t, remover)
	saveTestConfig(t, updater)
	loaded = loadTestConfig(t, path)
	if _, ok := loaded.GetRule("first.local"); ok {
		t.Fatal("stale writer resurrected a deleted rule")
	}
	if len(loaded.ListRules()) != 2 {
		t.Fatalf("want second and third rules, got %+v", loaded.ListRules())
	}
}

func TestConcurrentConfigWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	const writers = 8
	configs := make([]*Config, writers)
	for i := range configs {
		configs[i] = loadTestConfig(t, path)
		addTestRule(t, configs[i], string(rune('a'+i))+".local")
	}
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for _, cfg := range configs {
		wg.Go(func() { errs <- cfg.Save() })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := len(loadTestConfig(t, path).ListRules()); got != writers {
		t.Fatalf("saved %d/%d independent edits", got, writers)
	}
}

func TestRuleSnapshotsCannotBeMutatedByCaller(t *testing.T) {
	cfg := loadTestConfig(t, filepath.Join(t.TempDir(), "config.json"))
	rule := &Rule{Domain: "api.local", Type: TypeProxy, Target: "127.0.0.1:8080"}
	if err := cfg.AddRule(rule); err != nil {
		t.Fatal(err)
	}
	rule.Target = "127.0.0.1:9999"
	snapshot, _ := cfg.GetRule("api.local")
	if snapshot.Target != "127.0.0.1:8080" {
		t.Fatal("AddRule retained a mutable caller pointer")
	}
	snapshot.Target = "changed"
	cfg.ListRules()[0].Target = "changed again"
	current, _ := cfg.GetRule("api.local")
	if current.Target != "127.0.0.1:8080" {
		t.Fatal("GetRule/ListRules exposed a mutable internal pointer")
	}
}
