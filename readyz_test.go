package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// /readyz reports whether the CLI binary runCLI would spawn is there and
// runnable, without running it. Each case points CLI_BIN into a temp dir, so
// the tests exercise the same cliBinary() lookup runCLI uses.

// writeCLI creates a file at dir/name with exactly mode. Chmod after the write
// so the mode does not depend on the umask.
func writeCLI(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func getReadyz(t *testing.T, h http.Handler) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return rec.Code, rec.Body.String()
}

// assertNoPath is the guard for the path never reaching the body: /readyz may
// be reachable from outside, and the path is internal detail.
func assertNoPath(t *testing.T, body, dir, path string) {
	t.Helper()
	for _, s := range []string{path, dir, filepath.ToSlash(path), filepath.ToSlash(dir)} {
		if strings.Contains(body, s) {
			t.Errorf("body %q leaks the path %q", body, s)
		}
	}
}

func TestReadyzExecutableFileIsReady(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLI_BIN", writeCLI(t, dir, "cli", 0o755))

	code, body := getReadyz(t, http.HandlerFunc(handleReadyz))
	if code != http.StatusOK || body != "ready" {
		t.Errorf("got %d %q, want 200 %q", code, body, "ready")
	}
}

func TestReadyzMissingBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such-cli")
	t.Setenv("CLI_BIN", path)

	code, body := getReadyz(t, http.HandlerFunc(handleReadyz))
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "missing") {
		t.Errorf("got %d %q, want 503 containing %q", code, body, "missing")
	}
	assertNoPath(t, body, dir, path)
}

func TestReadyzDirectoryIsNotReady(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLI_BIN", path)

	code, body := getReadyz(t, http.HandlerFunc(handleReadyz))
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "not a regular file") {
		t.Errorf("got %d %q, want 503 containing %q", code, body, "not a regular file")
	}
	assertNoPath(t, body, dir, path)
}

func TestReadyzNonExecutableFileIsNotReady(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Measured on go1.27.0 windows/amd64: files written with 0644, 0755 and
		// 0600 all stat as 0666, so there is no execute bit to test, and the
		// handler skips that check on Windows for the same reason.
		t.Skip("Windows reports no execute bits: every file stats as 0666")
	}
	dir := t.TempDir()
	path := writeCLI(t, dir, "cli", 0o644)
	t.Setenv("CLI_BIN", path)

	code, body := getReadyz(t, http.HandlerFunc(handleReadyz))
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "not executable") {
		t.Errorf("got %d %q, want 503 containing %q", code, body, "not executable")
	}
	assertNoPath(t, body, dir, path)
}

// TestReadyzIsRouted sends the request through newMux, the route table main()
// serves, so a registration that is dropped or shadowed by "/" (which would
// 404 here) fails.
func TestReadyzIsRouted(t *testing.T) {
	dir := t.TempDir()
	mux := newMux()

	t.Setenv("CLI_BIN", filepath.Join(dir, "no-such-cli"))
	if code, body := getReadyz(t, mux); code != http.StatusServiceUnavailable || !strings.Contains(body, "not ready: cli binary missing") {
		t.Errorf("missing binary via mux: got %d %q, want 503 from handleReadyz", code, body)
	}

	t.Setenv("CLI_BIN", writeCLI(t, dir, "cli", 0o755))
	if code, body := getReadyz(t, mux); code != http.StatusOK || body != "ready" {
		t.Errorf("ready binary via mux: got %d %q, want 200 %q", code, body, "ready")
	}
}
