package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The index page must never be stored, so a deploy is visible on the next load
// instead of whenever a browser's heuristic cache expires, and a cached copy
// cannot replay one CSP nonce to every visitor. ServeFile is gone (see
// main.go): no Last-Modified, and an If-Modified-Since request still gets the
// full page with a fresh nonce, never a 304.

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

func TestIndexIsNeverStored(t *testing.T) {
	mux := newMux()

	first := getRoot(t, mux, "/", "")
	if first.Code != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", first.Code)
	}
	if cc := first.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET /: Cache-Control = %q, want %q", cc, "no-store")
	}
	if lm := first.Header().Get("Last-Modified"); lm != "" {
		t.Errorf("GET /: Last-Modified = %q, want none (it enables a 304)", lm)
	}

	second := getRoot(t, mux, "/", "Mon, 01 Jan 2035 00:00:00 GMT")
	if second.Code != http.StatusOK {
		t.Errorf("GET / with If-Modified-Since: status = %d, want 200, never 304", second.Code)
	}
	if cc := second.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET / with If-Modified-Since: Cache-Control = %q, want %q", cc, "no-store")
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
