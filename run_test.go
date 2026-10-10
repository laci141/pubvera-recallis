package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunRemovesKeyConfigOnShutdown covers the whole lifecycle of the key
// config: a dir left by a crashed process is purged at start, exactly one fresh
// dir exists while serving, and a graceful shutdown removes it. Before run()
// existed the cleanup was a defer in main() that SIGTERM and log.Fatal skipped.
func TestRunRemovesKeyConfigOnShutdown(t *testing.T) {
	tmp := t.TempDir()
	// os.TempDir reads TMPDIR on Unix and TMP/TEMP on Windows.
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)

	stale := filepath.Join(tmp, "recallis-openfda-stale")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "config.toml"), []byte("auth_header = \"Basic stale\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, ln, testKey) }()

	url := "http://" + ln.Addr().String() + "/healthz"
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			code := resp.StatusCode
			resp.Body.Close()
			if code == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("/healthz did not return 200 within 5s (last error: %v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale config dir was not purged: %v", err)
	}
	dirs, err := filepath.Glob(filepath.Join(tmp, "recallis-openfda-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Errorf("while running: %d recallis-openfda-* dirs %v, want exactly 1", len(dirs), dirs)
	}
	if _, ok := envValue(cliEnv(), "DRUG_ENFORCEMENT_CONFIG"); !ok {
		t.Error("while running: cliEnv has no DRUG_ENFORCEMENT_CONFIG")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v after shutdown, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s of cancel")
	}

	dirs, err = filepath.Glob(filepath.Join(tmp, "recallis-openfda-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 {
		t.Errorf("after shutdown: %d recallis-openfda-* dirs left %v, want 0", len(dirs), dirs)
	}
	if _, ok := envValue(cliEnv(), "DRUG_ENFORCEMENT_CONFIG"); ok {
		t.Error("after shutdown: cliEnv still has DRUG_ENFORCEMENT_CONFIG")
	}
}
