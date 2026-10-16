package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tyutyutyu/dodu/pkg/cache"
	"github.com/tyutyutyu/dodu/pkg/docker/mock"
	"github.com/tyutyutyu/dodu/pkg/plan"
)

func TestColdScanPopulatesCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := mock.New()
	c.Daemon.ID = "cache-test"
	start := time.Now()
	if _, err := loadOrScan(context.Background(), c, &rootFlags{}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cold scan waited on its own cache lock")
	}
	path, _ := cache.DefaultPath()
	db, err := cache.OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Load(cache.Key(c.Daemon)); err != nil {
		t.Fatalf("snapshot not persisted: %v", err)
	}
}

func TestExportInvalidExtensionPreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd(BuildInfo{})
	cmd.SetArgs([]string{"--host", "unix:///nonexistent-dodu.sock", "export", path})
	err := cmd.Execute()
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("unsupported extension")) {
		t.Fatalf("expected validation before daemon access, got %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep" {
		t.Fatal("existing file modified")
	}
}

func TestPlanReadOnlyExit(t *testing.T) {
	if got := classifyExit(errors.Join(errors.New("execute"), plan.ErrReadOnly)); got != ExitReadOnlyDenied {
		t.Fatalf("got exit %d", got)
	}
}

func TestInvalidOptionsFailBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{
		{"prune", "--kind", "typo"},
		{"prune", "--apply"},
		{"--readonly", "prune", "--apply", "--yes"},
		{"scan", "--format", "typo"},
		{"--log-level", "typo", "scan"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			cmd := newRootCmd(BuildInfo{})
			cmd.SetArgs(append([]string{"--host", "unix:///nonexistent-dodu.sock"}, args...))
			err := cmd.Execute()
			if err == nil || bytes.Contains([]byte(err.Error()), []byte("ping docker")) {
				t.Fatalf("expected early validation, got %v", err)
			}
		})
	}
}
