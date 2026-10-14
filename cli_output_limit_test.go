package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runCLI buffers the child's stdout and stderr in memory. Time and concurrency
// were bounded; bytes were not, so a runaway or hostile CLI could grow both
// buffers until the server ran out of memory, and a successful run handed the
// whole stdout to the client with 200. These tests drive the real handler with
// a stub CLI that writes a controlled amount to each stream.

const (
	stubJSONPrefix = `{"results":["`
	stubJSONSuffix = `"],"meta":{}}`
)

// buildOutputStubCLI compiles a child CLI that writes STUB_STDERR_FILL bytes to
// stderr, then a JSON object carrying STUB_STDOUT_FILL filler bytes to stdout,
// then sleeps STUB_SLEEP_MS and exits 0. A real child process, same mechanism
// as the other stubs, so the exec path under test is the production one.
func buildOutputStubCLI(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	src := `package main
import (
	"bytes"
	"io"
	"os"
	"strconv"
	"time"
)
func fill(w io.Writer, n int) {
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	for n > 0 {
		k := len(chunk)
		if n < k {
			k = n
		}
		if _, err := w.Write(chunk[:k]); err != nil {
			return
		}
		n -= k
	}
}
func main() {
	out, _ := strconv.Atoi(os.Getenv("STUB_STDOUT_FILL"))
	errN, _ := strconv.Atoi(os.Getenv("STUB_STDERR_FILL"))
	ms, _ := strconv.Atoi(os.Getenv("STUB_SLEEP_MS"))
	fill(os.Stderr, errN)
	os.Stdout.WriteString(` + "`" + stubJSONPrefix + "`" + `)
	fill(os.Stdout, out)
	os.Stdout.WriteString(` + "`" + stubJSONSuffix + "`" + `)
	time.Sleep(time.Duration(ms) * time.Millisecond)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write stub source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module outstub\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write stub go.mod: %v", err)
	}
	bin := filepath.Join(dir, "outstub")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build output stub CLI: %v\n%s", err, out)
	}
	t.Setenv("CLI_BIN", bin)
	t.Setenv("STUB_STDOUT_FILL", "0")
	t.Setenv("STUB_STDERR_FILL", "0")
	t.Setenv("STUB_SLEEP_MS", "0")
}

// TestCLIStdoutOverLimitIsNotServed: a child that writes far more than any real
// response and then keeps running must be stopped, and the client must get a
// controlled error, never the (partial or complete) oversized JSON with 200.
// The stub sleeps 15 s after writing, so a return well inside that proves the
// child was killed rather than waited for.
func TestCLIStdoutOverLimitIsNotServed(t *testing.T) {
	buildOutputStubCLI(t)
	t.Setenv("STUB_STDOUT_FILL", strconv.Itoa(32<<20))
	t.Setenv("STUB_SLEEP_MS", "15000")

	start := time.Now()
	var code int
	var body string
	captureLog(t, func() { code, body, _ = postCheck(t, "anything") })
	elapsed := time.Since(start)

	if code == http.StatusOK {
		t.Errorf("status = 200 for a %d-byte stdout; an oversized CLI response must not be served", 32<<20)
	}
	if code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
	if strings.HasPrefix(body, stubJSONPrefix) || len(body) > 4096 {
		t.Errorf("response body carries CLI output (%d bytes, starts %q)", len(body), body[:min(len(body), 40)])
	}
	if elapsed > 10*time.Second {
		t.Errorf("runCLI returned after %s; the child should be killed at the limit, not waited for", elapsed.Round(time.Millisecond))
	}
}

// TestCLIStderrFloodIsBounded: a successful run that floods stderr still returns
// its JSON unchanged, and the stderr bytes are not all held in memory. The
// allocation delta is measured across the whole request; 64 MiB of stderr in an
// unbounded buffer allocates at least that much.
func TestCLIStderrFloodIsBounded(t *testing.T) {
	buildOutputStubCLI(t)
	const flood = 64 << 20
	t.Setenv("STUB_STDERR_FILL", strconv.Itoa(flood))
	t.Setenv("STUB_STDOUT_FILL", "10")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var code int
	var body string
	captureLog(t, func() { code, body, _ = postCheck(t, "anything") })
	runtime.ReadMemStats(&after)

	if want := stubJSONPrefix + strings.Repeat("x", 10) + stubJSONSuffix; code != http.StatusOK || body != want {
		t.Errorf("status = %d body = %q, want 200 and %q", code, body, want)
	}
	grew := after.TotalAlloc - before.TotalAlloc
	t.Logf("request allocated %d bytes for a %d-byte stderr", grew, flood)
	if grew > 16<<20 {
		t.Errorf("request allocated %d bytes for a %d-byte stderr; stderr must be capped while it is written", grew, flood)
	}
}

// TestCLIStdoutAtLimitIsServed: a response of exactly cliStdoutLimit bytes is a
// normal response and comes back byte for byte.
func TestCLIStdoutAtLimitIsServed(t *testing.T) {
	buildOutputStubCLI(t)
	fill := cliStdoutLimit - len(stubJSONPrefix) - len(stubJSONSuffix)
	t.Setenv("STUB_STDOUT_FILL", strconv.Itoa(fill))

	var code int
	var body string
	captureLog(t, func() { code, body, _ = postCheck(t, "anything") })

	if code != http.StatusOK || len(body) != cliStdoutLimit {
		t.Fatalf("status = %d len(body) = %d, want 200 and %d bytes", code, len(body), cliStdoutLimit)
	}
	if body != stubJSONPrefix+strings.Repeat("x", fill)+stubJSONSuffix {
		t.Errorf("body at the limit differs from what the CLI wrote")
	}
}

// TestCappedBufferDropsPastTheLimit covers the stderr mode: the child keeps
// writing successfully, only the first limit bytes are kept.
func TestCappedBufferDropsPastTheLimit(t *testing.T) {
	b := &cappedBuffer{limit: 8}
	for _, p := range []string{"abcde", "fghij", "klmno"} {
		if n, err := b.Write([]byte(p)); n != len(p) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", p, n, err, len(p))
		}
	}
	if b.String() != "abcdefgh" || !b.exceeded {
		t.Errorf("buffer = %q exceeded = %v, want %q true", b.String(), b.exceeded, "abcdefgh")
	}
}

// TestCappedBufferStopsAtTheLimit covers the stdout mode: the write that passes
// the limit fails and onLimit runs exactly once.
func TestCappedBufferStopsAtTheLimit(t *testing.T) {
	calls := 0
	b := &cappedBuffer{limit: 8, onLimit: func() { calls++ }}
	if n, err := b.Write([]byte("12345678")); n != 8 || err != nil {
		t.Fatalf("write up to the limit = %d, %v; want 8, nil", n, err)
	}
	if n, err := b.Write([]byte("9")); n != 0 || !errors.Is(err, errCLIOutputTooLarge) {
		t.Errorf("write past the limit = %d, %v; want 0, errCLIOutputTooLarge", n, err)
	}
	b.Write([]byte("more"))
	if calls != 1 || b.Len() != 8 {
		t.Errorf("onLimit calls = %d len = %d, want 1 and 8", calls, b.Len())
	}
}

// TestCappedBufferHasNoReadFrom: io.Copy prefers io.ReaderFrom over Write, so a
// promoted bytes.Buffer.ReadFrom would bypass the limit.
func TestCappedBufferHasNoReadFrom(t *testing.T) {
	if _, ok := any(&cappedBuffer{}).(io.ReaderFrom); ok {
		t.Error("cappedBuffer implements io.ReaderFrom; io.Copy would skip the limit")
	}
}
