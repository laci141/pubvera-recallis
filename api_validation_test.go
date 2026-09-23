package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Input-boundary tests for the four API handlers, modelled on grantvera's.
//
// CLI_BIN points at a path that does not exist, so any request that survives
// validation fails in exec and comes back 502. The status code is therefore a
// direct readout of where the request died:
//
//	400 or 413 → rejected by validation, no process was spawned
//	502        → validation passed and the CLI was attempted

// oversizeBody is fixed, not derived from maxBodyBytes, so a mutation raising
// the constant cannot make the test allocate unbounded memory.
const oversizeBody = 128 << 10

func missingCLI(t *testing.T) {
	t.Helper()
	t.Setenv("CLI_BIN", filepath.Join(t.TempDir(), "no-such-cli"))
}

type apiEndpoint struct {
	name string
	path string
	fn   func(http.ResponseWriter, *http.Request)
	// valid is a minimal body that passes validation.
	valid string
}

func apiEndpoints() []apiEndpoint {
	return []apiEndpoint{
		{"check", "/api/check", handleCheck, `{"drug":"aspirin"}`},
		{"firm", "/api/firm", handleFirm, `{"firm":"Pfizer"}`},
		{"recent", "/api/recent", handleRecent, `{}`},
		{"reference", "/api/reference", handleReference, `{"recall_number":"D-0001-2024"}`},
	}
}

func post(fn func(http.ResponseWriter, *http.Request), path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// Sanity: the valid bodies really do reach the CLI, so a 400 in the other
// tests is caused by the thing each case changes.
func TestValidBodiesReachCLI(t *testing.T) {
	missingCLI(t)
	for _, ep := range apiEndpoints() {
		t.Run(ep.name, func(t *testing.T) {
			rec := post(ep.fn, ep.path, ep.valid)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502 (CLI attempted); body=%q", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBodyTooLarge(t *testing.T) {
	if maxBodyBytes >= oversizeBody {
		t.Fatalf("maxBodyBytes (%d) >= oversizeBody (%d): raise oversizeBody", maxBodyBytes, oversizeBody)
	}
	missingCLI(t)
	for _, ep := range apiEndpoints() {
		t.Run(ep.name, func(t *testing.T) {
			body := `{"drug":"` + strings.Repeat("a", oversizeBody) + `"}`
			rec := post(ep.fn, ep.path, body)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", rec.Code)
			}
		})
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	missingCLI(t)
	for _, ep := range apiEndpoints() {
		t.Run(ep.name, func(t *testing.T) {
			body := strings.TrimSuffix(ep.valid, "}")
			if body != "{" {
				body += ","
			}
			body += `"bogus":1}`
			rec := post(ep.fn, ep.path, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("body %s: status = %d, want 400", body, rec.Code)
			}
		})
	}
}

func TestTrailingDataRejected(t *testing.T) {
	missingCLI(t)
	for _, ep := range apiEndpoints() {
		t.Run(ep.name, func(t *testing.T) {
			rec := post(ep.fn, ep.path, ep.valid+`{}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "trailing data") {
				t.Fatalf("body = %q, want trailing-data message", rec.Body.String())
			}
		})
	}
}

func TestNumericCeilings(t *testing.T) {
	missingCLI(t)
	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		body string
		want int
	}{
		{"firm limit 51", handleFirm, `{"firm":"Pfizer","limit":51}`, http.StatusBadRequest},
		{"recent limit 51", handleRecent, `{"limit":51}`, http.StatusBadRequest},
		{"recent days 366", handleRecent, `{"days":366}`, http.StatusBadRequest},
		// At the ceiling: not rejected, so the CLI is attempted.
		{"firm limit 50", handleFirm, `{"firm":"Pfizer","limit":50}`, http.StatusBadGateway},
		{"recent limit 50", handleRecent, `{"limit":50}`, http.StatusBadGateway},
		{"recent days 365", handleRecent, `{"days":365}`, http.StatusBadGateway},
		// Zero, negative and null keep the default path.
		{"firm limit 0", handleFirm, `{"firm":"Pfizer","limit":0}`, http.StatusBadGateway},
		{"recent days -1", handleRecent, `{"days":-1}`, http.StatusBadGateway},
		{"recent null", handleRecent, `{"days":null,"limit":null}`, http.StatusBadGateway},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := post(c.fn, "/", c.body)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d; body=%q", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

type textCase struct {
	name  string
	fn    func(http.ResponseWriter, *http.Request)
	field string
}

var textFields = []textCase{
	{"check", handleCheck, "drug"},
	{"firm", handleFirm, "firm"},
	{"reference", handleReference, "recall_number"},
}

func TestWhitespaceTextRejected(t *testing.T) {
	missingCLI(t)
	for _, c := range textFields {
		t.Run(c.name, func(t *testing.T) {
			rec := post(c.fn, "/", `{"`+c.field+`":"   \t "}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "missing "+c.field) {
				t.Fatalf("body = %q", rec.Body.String())
			}
		})
	}
}

func TestLeadingDashRejected(t *testing.T) {
	missingCLI(t)
	for _, c := range textFields {
		for _, v := range []string{"--help", "-json", "  --help"} {
			t.Run(c.name+" "+v, func(t *testing.T) {
				rec := post(c.fn, "/", `{"`+c.field+`":"`+v+`"}`)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", rec.Code)
				}
				if !strings.Contains(rec.Body.String(), "must not start with '-'") {
					t.Fatalf("body = %q", rec.Body.String())
				}
			})
		}
	}
}
