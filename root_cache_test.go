package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The index page must never be stored, so a deploy is visible on the next load
// instead of whenever a browser's heuristic cache expires, and a cached copy
// cannot replay one CSP nonce to every visitor. Without a Cache-Control header
// neither Caddy nor Cloudflare adds one. no-store means no Last-Modified and no
// 304: every load gets a fresh page with a fresh nonce.

func getRoot(t *testing.T, h http.Handler, path, ifModifiedSince string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ifModifiedSince != "" {
		req.Header.Set("If-Modified-Since", ifModifiedSince)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var cspNonceRE = regexp.MustCompile(`'nonce-([A-Za-z0-9+/]{22})'`)

func TestIndexIsNeverStored(t *testing.T) {
	mux := newMux()
	for _, ims := range []string{"", "Mon, 01 Jan 2125 00:00:00 GMT"} {
		rec := getRoot(t, mux, "/", ims)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / (If-Modified-Since %q): status = %d, want 200", ims, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET / (If-Modified-Since %q): Cache-Control = %q, want %q", ims, cc, "no-store")
		}
		if lm := rec.Header().Get("Last-Modified"); lm != "" {
			t.Errorf("GET / (If-Modified-Since %q): Last-Modified = %q, want none", ims, lm)
		}
	}
}

func TestIndexCarriesFreshNonce(t *testing.T) {
	mux := newMux()
	seen := map[string]bool{}
	for range 2 {
		rec := getRoot(t, mux, "/", "")
		policy := rec.Header().Get(cspHeaderName)
		m := cspNonceRE.FindStringSubmatch(policy)
		if m == nil {
			t.Fatalf("GET /: %s = %q, want a 22-char nonce", cspHeaderName, policy)
		}
		body := rec.Body.String()
		if strings.Contains(body, nonceMarker) {
			t.Errorf("GET /: body still contains %s", nonceMarker)
		}
		if got := strings.Count(body, `nonce="`+m[1]+`"`); got != 4 {
			t.Errorf("GET /: nonce appears on %d script tags, want 4", got)
		}
		if seen[m[1]] {
			t.Errorf("GET /: nonce %q repeated", m[1])
		}
		seen[m[1]] = true
	}
}

func TestIndexFailsClosedWithoutNonce(t *testing.T) {
	orig := nonceRandRead
	nonceRandRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { nonceRandRead = orig })

	rec := getRoot(t, newMux(), "/", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("GET /: status = %d, want 500", rec.Code)
	}
	if p := rec.Header().Get(cspHeaderName); p != "" {
		t.Errorf("GET /: %s = %q, want none", cspHeaderName, p)
	}
	if strings.Contains(rec.Body.String(), "<script") {
		t.Errorf("GET /: body contains the page, want none")
	}
}

func TestIndexMissingIs404(t *testing.T) {
	t.Chdir(t.TempDir()) // no index.html to read here
	rec := getRoot(t, newMux(), "/", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /: status = %d, want 404", rec.Code)
	}
	if got := rec.Body.String(); got != "404 page not found\n" {
		t.Errorf("GET /: body = %q, want %q", got, "404 page not found\n")
	}
	if p := rec.Header().Get(cspHeaderName); p != "" {
		t.Errorf("GET /: %s = %q, want none", cspHeaderName, p)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "no-store" {
		t.Errorf("GET /: Cache-Control = %q, want anything but no-store", cc)
	}
}

func TestConfigStaysNoStore(t *testing.T) {
	rec := getRoot(t, newMux(), "/config.json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /config.json: status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET /config.json: Cache-Control = %q, want %q", cc, "no-store")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	if rec := getRoot(t, newMux(), "/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope: status = %d, want 404", rec.Code)
	}
}
