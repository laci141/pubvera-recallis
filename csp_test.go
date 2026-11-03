package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// CSP phase 2: every <script> tag in index.html carries nonce="__CSP_NONCE__",
// and each index response replaces that marker with a fresh nonce named in the
// Content-Security-Policy header. Style S1: the one <style> tag carries the same
// marker, and the style policy is sent as Content-Security-Policy-Report-Only. All requests go through newMux
// in-process (httptest), like root_cache_test.go.

// wantPolicy is written out here rather than taken from contentSecurityPolicy,
// so a change to any directive in csp.go fails a test.
func wantPolicy(nonce string) string {
	return "script-src 'nonce-" + nonce + "' 'strict-dynamic' https: 'unsafe-inline'; script-src-attr 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
}

// wantStylePolicy is the S1 Report-Only style policy, written out for the same
// reason.
func wantStylePolicy(nonce string) string {
	return "style-src 'nonce-" + nonce + "'; style-src-attr 'none'"
}

// The header names and the marker are spelled out so a renamed constant cannot
// agree with itself.
const (
	reportOnlyHeader = "Content-Security-Policy-Report-Only"
	enforcingHeader  = "Content-Security-Policy"
	nonceMarkerText  = "__CSP_NONCE__"
)

// index.html has 4 <script> tags: the external xlsx-js-style bundle, two
// classic inline scripts and one module.
const wantScriptTags = 4

// index.html has exactly one <style> tag; it carries the nonce too (style S1).
const wantStyleTags = 1

var styleOpenRE = regexp.MustCompile(`<style\b[^>]*>`)

// 22-character nonce in the Report-Only style policy.
var styleNonceRE = regexp.MustCompile(`^style-src 'nonce-([A-Za-z0-9+/]{22})'; style-src-attr 'none'$`)

// 16 random bytes in raw standard base64 are exactly 22 characters.
var cspNonceRE = regexp.MustCompile(`^script-src 'nonce-([A-Za-z0-9+/]{22})' `)

var scriptOpenRE = regexp.MustCompile(`<script\b[^>]*>`)

var bodyNonceRE = regexp.MustCompile(`nonce="([^"]*)"`)

// indexNonce fetches / and returns the header nonce, body and headers. It
// stops the test when there is no nonce to compare against; the header checks
// themselves live in the tests below.
func indexNonce(t *testing.T) (string, string, http.Header) {
	t.Helper()
	rec := getRoot(t, newMux(), "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", rec.Code)
	}
	csp := rec.Header().Get(enforcingHeader)
	m := cspNonceRE.FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("GET /: %s = %q, want a policy with a 22-character nonce", enforcingHeader, csp)
	}
	return m[1], rec.Body.String(), rec.Header()
}

func TestCSPMarkerReplacedInResponse(t *testing.T) {
	nonce, body, _ := indexNonce(t)
	if strings.Contains(body, nonceMarkerText) {
		t.Errorf("GET /: body still contains %s", nonceMarkerText)
	}
	if n := strings.Count(body, `nonce="`+nonce+`"`); n != wantScriptTags+wantStyleTags {
		t.Errorf("GET /: %d tags carry the header nonce, want %d", n, wantScriptTags+wantStyleTags)
	}
}

func TestCSPNonceIsFreshPerResponse(t *testing.T) {
	a, _, _ := indexNonce(t)
	b, _, _ := indexNonce(t)
	if a == b {
		t.Errorf("two responses carried the same nonce %q", a)
	}
}

func TestCSPHeaderNonceMatchesEveryBodyNonce(t *testing.T) {
	nonce, body, _ := indexNonce(t)
	tags := scriptOpenRE.FindAllString(body, -1)
	if len(tags) != wantScriptTags {
		t.Errorf("GET /: %d <script> tags, want %d", len(tags), wantScriptTags)
	}
	for _, tag := range tags {
		if !strings.Contains(tag, ` nonce="`+nonce+`"`) {
			t.Errorf("GET /: tag %s does not carry the header nonce %q", tag, nonce)
		}
	}
	for _, m := range bodyNonceRE.FindAllStringSubmatch(body, -1) {
		if m[1] != nonce {
			t.Errorf("GET /: body nonce %q differs from header nonce %q", m[1], nonce)
		}
	}
}

func TestCSPHeaderIsEnforcingWithExactPolicy(t *testing.T) {
	nonce, _, h := indexNonce(t)
	if got := h.Get(enforcingHeader); got != wantPolicy(nonce) {
		t.Errorf("%s = %q, want %q", enforcingHeader, got, wantPolicy(nonce))
	}
	if cspHeaderName != enforcingHeader {
		t.Errorf("cspHeaderName = %q, want %q", cspHeaderName, enforcingHeader)
	}
}

// The enforcing policy is pinned as a full literal for one fixed nonce, so a
// one-character change anywhere in csp.go fails here.
func TestCSPEnforcingPolicyPinnedLiteral(t *testing.T) {
	const want = "script-src 'nonce-AAAAAAAAAAAAAAAAAAAAAA' 'strict-dynamic' https: 'unsafe-inline'; script-src-attr 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	if got := contentSecurityPolicy("AAAAAAAAAAAAAAAAAAAAAA"); got != want {
		t.Errorf("contentSecurityPolicy = %q, want %q", got, want)
	}
}

// TestCSPIndexSendsBothHeaders does not use indexNonce, so a missing enforcing
// header does not hide the Report-Only check. Style S1: the Report-Only header
// carries only the style policy, with the enforcing header's nonce.
func TestCSPIndexSendsBothHeaders(t *testing.T) {
	rec := getRoot(t, newMux(), "/", "")
	enf := rec.Header().Get(enforcingHeader)
	if enf == "" {
		t.Errorf("GET /: %s = %q, want a policy", enforcingHeader, enf)
	}
	if n := len(rec.Header().Values(reportOnlyHeader)); n != 1 {
		t.Errorf("GET /: %d %s headers, want 1", n, reportOnlyHeader)
	}
	ro := rec.Header().Get(reportOnlyHeader)
	m := styleNonceRE.FindStringSubmatch(ro)
	if m == nil {
		t.Fatalf("GET /: %s = %q, want %q", reportOnlyHeader, ro, wantStylePolicy("<22-char nonce>"))
	}
	if want := wantStylePolicy(m[1]); ro != want {
		t.Errorf("GET /: %s = %q, want %q", reportOnlyHeader, ro, want)
	}
	if e := cspNonceRE.FindStringSubmatch(enf); e == nil || e[1] != m[1] {
		t.Errorf("GET /: Report-Only nonce %q differs from the enforcing header %q", m[1], enf)
	}
}

// One nonce per response: enforcing header, Report-Only header and the served
// <style> tag all carry it.
func TestCSPStyleTagCarriesTheResponseNonce(t *testing.T) {
	nonce, body, h := indexNonce(t)
	if got, want := h.Get(reportOnlyHeader), wantStylePolicy(nonce); got != want {
		t.Errorf("%s = %q, want %q", reportOnlyHeader, got, want)
	}
	tags := styleOpenRE.FindAllString(body, -1)
	if len(tags) != wantStyleTags {
		t.Errorf("GET /: %d <style> tags, want %d", len(tags), wantStyleTags)
	}
	for _, tag := range tags {
		if !strings.Contains(tag, ` nonce="`+nonce+`"`) {
			t.Errorf("GET /: tag %s does not carry the header nonce %q", tag, nonce)
		}
	}
}

func TestCSPIndexFileMarksEveryScript(t *testing.T) {
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	tags := scriptOpenRE.FindAll(data, -1)
	if len(tags) != wantScriptTags {
		t.Errorf("index.html: %d <script> tags, want %d", len(tags), wantScriptTags)
	}
	for _, tag := range tags {
		if !strings.Contains(string(tag), ` nonce="`+nonceMarkerText+`"`) {
			t.Errorf("index.html: tag %s has no nonce marker", tag)
		}
	}
	styles := styleOpenRE.FindAll(data, -1)
	if len(styles) != wantStyleTags {
		t.Errorf("index.html: %d <style> tags, want %d", len(styles), wantStyleTags)
	}
	for _, tag := range styles {
		if !strings.Contains(string(tag), ` nonce="`+nonceMarkerText+`"`) {
			t.Errorf("index.html: tag %s has no nonce marker", tag)
		}
	}
	if n := strings.Count(string(data), nonceMarkerText); n != wantScriptTags+wantStyleTags {
		t.Errorf("index.html: %s appears %d times, want %d", nonceMarkerText, n, wantScriptTags+wantStyleTags)
	}
}

// Fail closed: no nonce means no page and no policy.
func TestCSPRandFailureIs500(t *testing.T) {
	orig := nonceRandRead
	nonceRandRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	defer func() { nonceRandRead = orig }()
	rec := getRoot(t, newMux(), "/", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("GET /: status = %d, want 500", rec.Code)
	}
	for _, name := range []string{reportOnlyHeader, enforcingHeader} {
		if got := rec.Header().Get(name); got != "" {
			t.Errorf("GET /: %s = %q, want none", name, got)
		}
	}
	if strings.Contains(rec.Body.String(), "<script") {
		t.Errorf("GET /: body contains <script despite nonce failure")
	}
}

// A missing index behaves as it did under ServeFile: a plain 404, with no
// policy and no no-store.
func TestCSPUnreadableIndexIs404WithoutPolicy(t *testing.T) {
	t.Chdir(t.TempDir()) // no index.html to read here
	rec := getRoot(t, newMux(), "/", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /: status = %d, want 404", rec.Code)
	}
	for _, name := range []string{reportOnlyHeader, enforcingHeader} {
		if got := rec.Header().Get(name); got != "" {
			t.Errorf("GET /: %s = %q, want none", name, got)
		}
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "no-store" {
		t.Errorf("GET /: Cache-Control = %q, want anything but %q", cc, "no-store")
	}
}

func TestCSPOnlyOnTheIndexPage(t *testing.T) {
	for _, path := range []string{"/nope", "/config.json"} {
		rec := getRoot(t, newMux(), path, "")
		for _, name := range []string{reportOnlyHeader, enforcingHeader} {
			if got := rec.Header().Get(name); got != "" {
				t.Errorf("GET %s: %s = %q, want none", path, name, got)
			}
		}
	}
	rec := httptest.NewRecorder()
	handleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	for _, name := range []string{reportOnlyHeader, enforcingHeader} {
		if got := rec.Header().Get(name); got != "" {
			t.Errorf("GET /healthz: %s = %q, want none", name, got)
		}
	}
}

// The web-builder stage copies Go files by name. A new .go file missing from
// that COPY line passes go test but breaks the image build (Praxvera CI,
// 2026-10-29). Every non-test .go file must be on the line.
func TestDockerfileCopiesEveryGoFile(t *testing.T) {
	df, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	var copyLine string
	for _, l := range strings.Split(strings.ReplaceAll(string(df), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "COPY main.go ") {
			copyLine = l
		}
	}
	if copyLine == "" {
		t.Fatalf("Dockerfile has no `COPY main.go …` line")
	}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob *.go: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		if !strings.Contains(" "+copyLine+" ", " "+f+" ") {
			t.Errorf("Dockerfile COPY line %q is missing %s", copyLine, f)
		}
	}
}
