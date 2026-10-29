package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunVersionAndInvalidConfiguration(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"proxctl", "--version"}
	if code := run(); code != 0 {
		t.Fatalf("version exit=%d", code)
	}
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"proxctl", "--config", path, "list"}
	if code := run(); code == 0 {
		t.Fatal("invalid configuration returned success")
	}
}
