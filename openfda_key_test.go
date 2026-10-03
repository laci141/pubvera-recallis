package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testKey is a placeholder, never a real credential.
const testKey = "TESTKEY123"

// testKeyB64 is the base64 form that ends up in the Authorization header:
// openFDA takes the key as the Basic-auth username, so the encoded string is
// key + ":".
var testKeyB64 = base64.StdEncoding.EncodeToString([]byte(testKey + ":"))

// withOpenFDAKey runs the same setup main() runs at startup and tears the
// private directory down with the test.
func withOpenFDAKey(t *testing.T, key string) {
	t.Helper()
	cleanup, err := initOpenFDAKey(key)
	if err != nil {
		t.Fatalf("initOpenFDAKey: %v", err)
	}
	t.Cleanup(cleanup)
}

// envValue returns the value of name in an exec-style environment, or "" and
// false when it is absent. The last entry wins, as it does for a child process.
func envValue(env []string, name string) (string, bool) {
	val, found := "", false
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
			val, found = v, true
		}
	}
	return val, found
}

// buildKeyStubCLI compiles a child CLI that records what it was started with.
//   - STUB_DUMP: path of a file that receives argv and every *_BASE_URL and
//     DRUG_ENFORCEMENT_CONFIG variable, one per line.
//   - STUB_STDERR: text written to stderr.
//   - STUB_EXIT: "1" makes the stub exit non-zero after writing stderr.
//
// Same mechanism as the other stubs: a real child process, so the exec path
// under test is the one production runs.
func buildKeyStubCLI(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	src := `package main
import (
	"fmt"
	"os"
	"strings"
)
func main() {
	if p := os.Getenv("STUB_DUMP"); p != "" {
		var b strings.Builder
		for _, a := range os.Args {
			b.WriteString("ARG " + a + "\n")
		}
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if strings.HasSuffix(strings.ToUpper(k), "BASE_URL") || strings.EqualFold(k, "DRUG_ENFORCEMENT_CONFIG") {
				b.WriteString("ENV " + kv + "\n")
			}
		}
		os.WriteFile(p, []byte(b.String()), 0o600)
	}
	if s := os.Getenv("STUB_STDERR"); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	if os.Getenv("STUB_EXIT") == "1" {
		os.Exit(1)
	}
	fmt.Println(` + "`" + `{"results":[],"meta":{}}` + "`" + `)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write stub source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module keystub\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write stub go.mod: %v", err)
	}
	bin := filepath.Join(dir, "keystub")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build key stub CLI: %v\n%s", err, out)
	}
	t.Setenv("CLI_BIN", bin)
}

func TestOpenFDAKeyUnset_NoConfig(t *testing.T) {
	// Blank and whitespace-only both mean keyless.
	for _, key := range []string{"", "   "} {
		before, _ := os.ReadDir(os.TempDir())
		withOpenFDAKey(t, key)
		after, _ := os.ReadDir(os.TempDir())
		if len(after) > len(before) {
			t.Errorf("key %q: a file or directory was created in the temp dir", key)
		}
		if _, ok := envValue(cliEnv(), "DRUG_ENFORCEMENT_CONFIG"); ok {
			t.Errorf("key %q: DRUG_ENFORCEMENT_CONFIG is set in the child env", key)
		}
	}
}

func TestOpenFDAKeySet_WritesBasicAuthConfig(t *testing.T) {
	// An inherited value must be overridden, not left in front of ours.
	t.Setenv("DRUG_ENFORCEMENT_CONFIG", "/inherited/other.toml")
	withOpenFDAKey(t, testKey)

	path, ok := envValue(cliEnv(), "DRUG_ENFORCEMENT_CONFIG")
	if !ok || path == "" {
		t.Fatal("DRUG_ENFORCEMENT_CONFIG is not set in the child env")
	}
	if path == "/inherited/other.toml" {
		t.Fatal("the inherited DRUG_ENFORCEMENT_CONFIG won over the generated file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	// base_url is not needed: the CLI's default is already https://api.fda.gov
	// and a config file only overrides the fields it names.
	want := "auth_header = \"Basic " + testKeyB64 + "\"\n"
	if string(raw) != want {
		t.Errorf("config content = %q, want %q", raw, want)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("config mode = %o, want 600", perm)
		}
		di, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if perm := di.Mode().Perm(); perm != 0o700 {
			t.Errorf("config dir mode = %o, want 700", perm)
		}
	}
}

func TestOpenFDAKeySet_CleanupRemovesFile(t *testing.T) {
	cleanup, err := initOpenFDAKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := envValue(cliEnv(), "DRUG_ENFORCEMENT_CONFIG")
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config file still present after cleanup: %v", err)
	}
	if _, ok := envValue(cliEnv(), "DRUG_ENFORCEMENT_CONFIG"); ok {
		t.Error("cliEnv still points at a config after cleanup")
	}
}

func TestRunCLI_KeyNotInArgsOrURL(t *testing.T) {
	buildKeyStubCLI(t)
	dump := filepath.Join(t.TempDir(), "dump.txt")
	t.Setenv("STUB_DUMP", dump)
	// A base URL set by the operator must pass through untouched.
	t.Setenv("DRUG_ENFORCEMENT_BASE_URL", "https://api.fda.gov")
	withOpenFDAKey(t, testKey)

	code, _, _ := postCheck(t, "aspirin")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("stub did not write its dump: %v", err)
	}
	got := string(raw)
	for _, bad := range []string{testKey, testKeyB64} {
		if strings.Contains(got, bad) {
			t.Errorf("child argv/base URLs contain the key material %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "ENV DRUG_ENFORCEMENT_BASE_URL=https://api.fda.gov\n") {
		t.Errorf("the base URL was altered:\n%s", got)
	}
	if !strings.Contains(got, "ENV DRUG_ENFORCEMENT_CONFIG=") {
		t.Errorf("the child did not receive DRUG_ENFORCEMENT_CONFIG:\n%s", got)
	}
}

func TestRunCLI_StderrRedacted(t *testing.T) {
	for _, tc := range []struct {
		name string
		exit string
	}{{"failure", "1"}, {"success with warnings", "0"}} {
		t.Run(tc.name, func(t *testing.T) {
			buildKeyStubCLI(t)
			t.Setenv("STUB_EXIT", tc.exit)
			t.Setenv("STUB_STDERR", "GET /x?api_key="+testKey+" Authorization: Basic "+testKeyB64+" failed")
			withOpenFDAKey(t, testKey)

			var code int
			var body string
			logged := captureLog(t, func() {
				code, body, _ = postCheck(t, "aspirin")
			})
			for name, text := range map[string]string{"log": logged, "HTTP body": body} {
				if strings.Contains(text, testKey) || strings.Contains(text, testKeyB64) {
					t.Errorf("%s contains the key or its base64 form:\n%s", name, text)
				}
			}
			if !strings.Contains(logged, "[REDACTED]") {
				t.Errorf("log does not show [REDACTED]:\n%s", logged)
			}
			if tc.exit == "1" && code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502", code)
			}
		})
	}
}

func TestRedactKey(t *testing.T) {
	withOpenFDAKey(t, testKey)
	in := "a " + testKey + " b " + testKeyB64 + " c Basic " + testKeyB64
	got := redactKey(in)
	if strings.Contains(got, testKey) || strings.Contains(got, testKeyB64) {
		t.Errorf("redactKey left key material: %q", got)
	}
	// Unset: text passes through unchanged.
	withOpenFDAKey(t, "")
	if got := redactKey(in); got != in {
		t.Errorf("redactKey changed text with no key set: %q", got)
	}
}

// The handler must stay reachable on the same mux with no key at all.
func TestOpenFDAKeyUnset_HandlersUnchanged(t *testing.T) {
	withOpenFDAKey(t, "")
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d", rec.Code)
	}
}
