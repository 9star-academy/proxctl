package config

import (
	"path/filepath"
	"testing"
)

func TestConfigSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load initial config: %v", err)
	}

	rule := &Rule{
		Domain: "api.example.com",
		Type:   TypeProxy,
		Target: "127.0.0.1:8080",
		CORS:   true,
	}

	if err := cfg.AddRule(rule); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Reload and check
	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}

	r, found := loaded.GetRule("api.example.com")
	if !found || r.Target != "127.0.0.1:8080" || !r.CORS {
		t.Fatalf("loaded rule mismatch: %+v", r)
	}

	// Remove rule
	if !loaded.RemoveRule("api.example.com") {
		t.Fatalf("expected rule to be removed")
	}
	if len(loaded.ListRules()) != 0 {
		t.Fatalf("expected 0 rules after remove")
	}
}
