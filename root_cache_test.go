package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The index page must be revalidated on every load, so a deploy is visible
// immediately instead of whenever a browser's heuristic cache expires. Without
// a Cache-Control header neither Caddy nor Cloudflare adds one, and browsers
// fall back to heuristic freshness from Last-Modified. no-cache (not no-store)
// keeps the 304 path, so a revalidation stays cheap.

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

func TestIndexIsRevalidatedOnEveryLoad(t *testing.T) {
	mux := newMux()

	first := getRoot(t, mux, "/", "")
	if first.Code != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", first.Code)
	}
	if cc := first.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("GET /: Cache-Control = %q, want %q", cc, "no-cache")
	}

	lastMod := first.Header().Get("Last-Modified")
	if lastMod == "" {
		t.Fatalf("GET /: no Last-Modified header, so a revalidation cannot be tested")
	}
	second := getRoot(t, mux, "/", lastMod)
	if second.Code != http.StatusNotModified {
		t.Errorf("GET / with If-Modified-Since: status = %d, want 304", second.Code)
	}
	if cc := second.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("GET / with If-Modified-Since: Cache-Control = %q, want %q", cc, "no-cache")
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
