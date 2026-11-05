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
// Content-Security-Policy header. Style S2: the one <style> tag carries the same
// marker, and the style policy is part of that one enforcing header. connect-src
// PR B: connect-src is enforced too, appended to that same header, and no
// response carries a Content-Security-Policy-Report-Only header. All requests
// go through newMux in-process (httptest), like root_cache_test.go.

// wantPolicy is written out here rather than taken from contentSecurityPolicy,
// so a change to any directive in csp.go fails a test.
func wantPolicy(nonce string) string {
	return "script-src 'nonce-" + nonce + "' 'strict-dynamic' https: 'unsafe-inline'; script-src-attr 'none'; " + wantStylePolicy(nonce) + "; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
}

// wantNoSupabaseTail is the connect-src tail of the enforcing header when no
// Supabase origin is configured, written out for the same reason.
const wantNoSupabaseTail = "; connect-src 'self'; report-to csp; report-uri /csp-report"

// wantStylePolicy is the style part of the enforcing policy (S2), written out
// for the same reason.
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

// index.html has exactly one <style> tag; it carries the nonce too (style S1/S2).
const wantStyleTags = 1

var styleOpenRE = regexp.MustCompile(`<style\b[^>]*>`)

// Both 22-character nonces of the enforcing policy: script-src and style-src.
var bothNoncesRE = regexp.MustCompile(`^script-src 'nonce-([A-Za-z0-9+/]{22})' 'strict-dynamic' https: 'unsafe-inline'; script-src-attr 'none'; style-src 'nonce-([A-Za-z0-9+/]{22})'; style-src-attr 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; connect-src 'self'(?: https://[A-Za-z0-9.:-]+)?; report-to csp; report-uri /csp-report$`)

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
	t.Setenv("SUPABASE_URL", "")
	t.Setenv("SUPABASE_PUBLISHABLE_KEY", "")
	nonce, _, h := indexNonce(t)
	if got, want := h.Get(enforcingHeader), wantPolicy(nonce)+wantNoSupabaseTail; got != want {
		t.Errorf("%s = %q, want %q", enforcingHeader, got, want)
	}
	if cspHeaderName != enforcingHeader {
		t.Errorf("cspHeaderName = %q, want %q", cspHeaderName, enforcingHeader)
	}
}

// The enforcing policy is pinned as a full literal for one fixed nonce, so a
// one-character change anywhere in csp.go fails here.
func TestCSPEnforcingPolicyPinnedLiteral(t *testing.T) {
	const want = "script-src 'nonce-AAAAAAAAAAAAAAAAAAAAAA' 'strict-dynamic' https: 'unsafe-inline'; script-src-attr 'none'; style-src 'nonce-AAAAAAAAAAAAAAAAAAAAAA'; style-src-attr 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	if got := contentSecurityPolicy("AAAAAAAAAAAAAAAAAAAAAA"); got != want {
		t.Errorf("contentSecurityPolicy = %q, want %q", got, want)
	}
}

// TestCSPIndexSendsOneEnforcingHeader replaces S1's TestCSPIndexSendsBothHeaders
// on purpose: S1 required a Report-Only style header, S2 forbids it. connect-src
// PR A brought a Report-Only header back for connect-src; PR B enforces
// connect-src inside the one header and forbids the Report-Only header again.
// It does not use indexNonce, so a missing enforcing header does not hide the
// Report-Only check. Header().Set keeps only the last value, so two Set calls
// on one name would silently drop a policy; exactly one value is required.
func TestCSPIndexSendsOneEnforcingHeader(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://abc.supabase.co")
	t.Setenv("SUPABASE_PUBLISHABLE_KEY", "pk_test_123")
	rec := getRoot(t, newMux(), "/", "")
	if n := len(rec.Header().Values(enforcingHeader)); n != 1 {
		t.Errorf("GET /: %d %s headers, want 1", n, enforcingHeader)
	}
	if got := rec.Header().Values(reportOnlyHeader); len(got) != 0 {
		t.Errorf("GET /: %s headers %q, want none", reportOnlyHeader, got)
	}
	if enf := rec.Header().Get(enforcingHeader); !bothNoncesRE.MatchString(enf) {
		t.Errorf("GET /: %s = %q, want the enforcing policy with connect-src", enforcingHeader, enf)
	}
}

// One nonce per response: the script-src nonce, the style-src nonce and the
// served <style> tag all carry it.
func TestCSPStyleTagCarriesTheResponseNonce(t *testing.T) {
	nonce, body, h := indexNonce(t)
	enf := h.Get(enforcingHeader)
	m := bothNoncesRE.FindStringSubmatch(enf)
	if m == nil {
		t.Fatalf("GET /: %s = %q, want %q", enforcingHeader, enf, wantPolicy("<22-char nonce>"))
	}
	if m[1] != nonce || m[2] != nonce {
		t.Errorf("GET /: script nonce %q, style nonce %q, want both %q", m[1], m[2], nonce)
	}
	if !strings.Contains(enf, "; "+wantStylePolicy(nonce)+"; ") {
		t.Errorf("GET /: %s = %q does not contain %q", enforcingHeader, enf, wantStylePolicy(nonce))
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
