package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/9star-academy/proxctl/internal/config"
)

func TestCLIAddListRemove(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	app := &App{
		Version: "1.0.0",
		Out:     out,
		Err:     errOut,
		Config:  cfg,
	}

	ctx := context.Background()

	// 1. Add proxy rule
	code := app.Run(ctx, []string{"add", "api.example.com", "--to", "8080", "--cors"})
	if code != 0 {
		t.Fatalf("add failed: %d, err: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "反向代理配置成功") {
		t.Fatalf("expected success message, got: %s", out.String())
	}

	// 2. Add static rule
	out.Reset()
	code = app.Run(ctx, []string{"static", "app.example.com", "--dir", tmpDir, "--spa"})
	if code != 0 {
		t.Fatalf("static failed: %d, err: %s", code, errOut.String())
	}

	// 3. List
	out.Reset()
	code = app.Run(ctx, []string{"list"})
	if code != 0 {
		t.Fatalf("list failed: %d", code)
	}
	if !strings.Contains(out.String(), "api.example.com") || !strings.Contains(out.String(), "app.example.com") {
		t.Fatalf("list output missing domains: %s", out.String())
	}

	// 4. Remove
	out.Reset()
	code = app.Run(ctx, []string{"rm", "api.example.com"})
	if code != 0 {
		t.Fatalf("remove failed: %d", code)
	}
	if len(cfg.ListRules()) != 1 {
		t.Fatalf("expected 1 rule remaining, got %d", len(cfg.ListRules()))
	}
}
