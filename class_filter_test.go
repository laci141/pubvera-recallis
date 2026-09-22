package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// The page and the CLI name the device class differently: the <select> sends
// "I"/"II"/"III", the CLI wants 1/2/3. cliClassArg is the only place that
// translation happens, so these tests pin both the vocabulary it accepts and
// what it rejects.
func TestCLIClassArg(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		// "Any class": no flag at all, which is what an empty result means.
		{in: "", want: "", ok: true},
		// What the frontend sends, including sloppy casing and padding.
		{in: "I", want: "1", ok: true},
		{in: "ii", want: "2", ok: true},
		{in: " III ", want: "3", ok: true},
		// What the CLI itself documents.
		{in: "1", want: "1", ok: true},
		{in: "2", want: "2", ok: true},
		{in: "3", want: "3", ok: true},
		// Neither vocabulary.
		{in: "IV", ok: false},
		{in: "0", ok: false},
		{in: "4", ok: false},
		{in: "Class I", ok: false},
		{in: "-1", ok: false},
		{in: "--help", ok: false},
	} {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			got, ok := cliClassArg(tc.in)
			if ok != tc.ok {
				t.Fatalf("cliClassArg(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("cliClassArg(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// postCheckClass drives the real handler with a drug and a class.
func postCheckClass(t *testing.T, drug, class string) (int, string) {
	t.Helper()
	body := fmt.Sprintf(`{"drug":%q,"class":%q}`, drug, class)
	req := httptest.NewRequest(http.MethodPost, "/api/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleCheck(rec, req)
	return rec.Code, rec.Body.String()
}

// TestCheckRejectsUnknownClassWithoutRunningTheCLI proves the rejection happens
// before the child process. CLI_BIN points at a path that does not exist, so a
// spawn would fail and surface as 502; a 400 can therefore only mean the
// handler never got that far.
func TestCheckRejectsUnknownClassWithoutRunningTheCLI(t *testing.T) {
	t.Setenv("CLI_BIN", filepath.Join(t.TempDir(), "no-such-cli"))

	code, body := postCheckClass(t, "ibuprofen", "IV")

	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (a 502 means the CLI was spawned)\nbody: %s", code, body)
	}
	if !strings.Contains(body, "class must be one of I, II, III") {
		t.Errorf("body = %q, want it to name the accepted classes", body)
	}
}
