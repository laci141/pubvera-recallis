package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildConcurrencyStubCLI compiles a child CLI that brackets its own lifetime in
// a shared log: "S" on entry, "E" on exit, appended with O_APPEND so the file
// records the true chronological order of every child.
func buildConcurrencyStubCLI(t *testing.T, logPath string, delay time.Duration) {
	t.Helper()
	dir := t.TempDir()
	src := `package main
import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)
func mark(p, s string) {
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintln(f, s)
		f.Close()
	}
}
func main() {
	p := os.Getenv("CONC_STUB_LOG")
	mark(p, "S")
	d, _ := time.ParseDuration(os.Getenv("CONC_STUB_DELAY"))
	time.Sleep(d)
	out, _ := json.Marshal(map[string]any{"results": []any{}, "count": 0})
	fmt.Println(string(out))
	mark(p, "E")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write stub source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module concstub\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write stub go.mod: %v", err)
	}
	bin := filepath.Join(dir, "concstub")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build concurrency stub CLI: %v\n%s", err, out)
	}
	t.Setenv("CLI_BIN", bin)
	t.Setenv("CONC_STUB_LOG", logPath)
	t.Setenv("CONC_STUB_DELAY", delay.String())
}

// peakConcurrency replays the stub log and reports the highest number of child
// processes alive at the same moment.
func peakConcurrency(t *testing.T, logPath string) int {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read stub log: %v", err)
	}
	cur, peak := 0, 0
	for _, line := range strings.Split(string(raw), "\n") {
		switch strings.TrimSpace(line) {
		case "S":
			cur++
			if cur > peak {
				peak = cur
			}
		case "E":
			cur--
		}
	}
	return peak
}

// postCheck drives the real handleCheck handler.
func postCheck(t *testing.T, drug string) (int, string, string) {
	t.Helper()
	body := fmt.Sprintf(`{"drug":%q}`, drug)
	req := httptest.NewRequest(http.MethodPost, "/api/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleCheck(rec, req)
	return rec.Code, rec.Body.String(), rec.Header().Get("Retry-After")
}

// fireDistinctChecks sends n concurrent requests with distinct drug names.
func fireDistinctChecks(t *testing.T, n int) []int {
	t.Helper()
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			code, _, _ := postCheck(t, fmt.Sprintf("drug-%d", i))
			codes[i] = code
		}(i)
	}
	close(start)
	wg.Wait()
	return codes
}

// useSemaphore swaps in a semaphore for one test and restores the real one.
func useSemaphore(t *testing.T, slots int, wait time.Duration) {
	t.Helper()
	prevSem, prevWait := cliSem, cliSlotWait
	cliSem, cliSlotWait = newCLISemaphore(slots), wait
	t.Cleanup(func() { cliSem, cliSlotWait = prevSem, prevWait })
}

// TestCLISemaphoreBoundsChildRuns measures peak concurrency with and without
// the bound. The unbounded assertion is not decoration: if the harness ever
// stops producing concurrency, the bounded result proves nothing.
func TestCLISemaphoreBoundsChildRuns(t *testing.T) {
	const requests = 12

	unboundedLog := filepath.Join(t.TempDir(), "unbounded.log")
	buildConcurrencyStubCLI(t, unboundedLog, 400*time.Millisecond)

	useSemaphore(t, 0, 10*time.Second)
	fireDistinctChecks(t, requests)
	unbounded := peakConcurrency(t, unboundedLog)

	boundedLog := filepath.Join(t.TempDir(), "bounded.log")
	t.Setenv("CONC_STUB_LOG", boundedLog)

	useSemaphore(t, 4, 10*time.Second)
	codes := fireDistinctChecks(t, requests)
	bounded := peakConcurrency(t, boundedLog)

	t.Logf("MEASURED peak concurrent child processes for %d distinct requests: unbounded=%d bounded=%d",
		requests, unbounded, bounded)

	if unbounded <= 4 {
		t.Fatalf("unbounded peak was %d, expected more than 4 — the harness is not "+
			"producing concurrency, so the bounded result proves nothing", unbounded)
	}
	if bounded > 4 {
		t.Errorf("bounded peak = %d, want at most 4", bounded)
	}
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("request %d got %d, want 200 — the grace period should absorb this burst", i, c)
		}
	}
}

// TestCLISemaphoreRejectsWithRetryAfter pins the overload response.
// 503 must be distinguishable from 502, and must carry Retry-After.
func TestCLISemaphoreRejectsWithRetryAfter(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "reject.log")
	buildConcurrencyStubCLI(t, logPath, 2*time.Second)

	useSemaphore(t, 1, 150*time.Millisecond)

	if err := cliSem.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	code, body, retryAfter := postCheck(t, "rejected-drug")
	cliSem.release()

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", code, body)
	}
	if retryAfter != "30" {
		t.Errorf("Retry-After = %q, want %q", retryAfter, "30")
	}
	if !strings.Contains(body, "busy") {
		t.Errorf("body = %q, want it to say the server is busy", strings.TrimSpace(body))
	}
}

// TestCLISemaphoreDisabledIsANoOp proves the escape hatch: CLI_MAX_CONCURRENT=0
// restores pre-semaphore behaviour without a deploy.
func TestCLISemaphoreDisabledIsANoOp(t *testing.T) {
	t.Setenv("CLI_MAX_CONCURRENT", "0")
	if got := cliSlotsFromEnv(); got != 0 {
		t.Fatalf("cliSlotsFromEnv() = %d, want 0", got)
	}
	s := newCLISemaphore(0)
	if got := s.capacity(); got != 0 {
		t.Errorf("capacity() = %d, want 0", got)
	}
	for i := 0; i < 50; i++ {
		if err := s.acquire(context.Background()); err != nil {
			t.Fatalf("acquire %d on a disabled semaphore returned %v", i, err)
		}
	}
	if got := s.inUse(); got != 0 {
		t.Errorf("inUse() = %d on a disabled semaphore, want 0", got)
	}

	t.Setenv("CLI_MAX_CONCURRENT", "")
	if got := cliSlotsFromEnv(); got != defaultCLISlots {
		t.Errorf("empty env gave %d, want the default %d", got, defaultCLISlots)
	}
	t.Setenv("CLI_MAX_CONCURRENT", "not-a-number")
	if got := cliSlotsFromEnv(); got != defaultCLISlots {
		t.Errorf("unparseable env gave %d, want the default %d", got, defaultCLISlots)
	}
}
