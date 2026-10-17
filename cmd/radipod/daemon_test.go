package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfiguredRunAndSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"base_url":"http://127.0.0.1:8080","listen":"127.0.0.1:0","rules":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"sync", "-config", path, "-dry-run"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := run(ctx, []string{"run", "-config", path}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "episodes")); err != nil {
		t.Fatal(err)
	}
}
