package main

import "crypto/rand"

// Content-Security-Policy, phase 3 (style S2): every <script> tag and the one
// <style> tag in index.html carry nonce="__CSP_NONCE__", and each index
// response replaces that marker with a fresh nonce named in the one policy
// header. The header is enforcing for scripts and styles alike, so violations
// are blocked. Phase 1 (Report-Only) ran on the script policy and style S1
// (Report-Only) on the style policy before each was enforced. connect-src runs
// in Report-Only in a separate header, set up in csp_report.go.
const (
	nonceMarker   = "__CSP_NONCE__"
	cspHeaderName = "Content-Security-Policy"
)

// nonceRandRead is the random source for the CSP nonce. It is a variable only
// so a test can make it fail.
var nonceRandRead = rand.Read

// contentSecurityPolicy is the policy for the index page. 'unsafe-inline' and
// https: are the backward-compatibility fallback for browsers that predate
// nonces and 'strict-dynamic'; browsers that understand nonce plus
// 'strict-dynamic' (Chrome 52+, Firefox 52+, Safari 15.4+) ignore both.
func contentSecurityPolicy(nonce string) string {
	return "script-src 'nonce-" + nonce + "' 'strict-dynamic' https: 'unsafe-inline'; script-src-attr 'none'; " + styleSecurityPolicy(nonce) + "; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
}

// styleSecurityPolicy is the style part of the index page policy (S2, enforced).
// The one <style> block carries the same nonce; style="" attributes are refused
// outright (style-src-attr 'none'), as the page has none (S0). Script that sets
// el.style.x is CSSOM, not an attribute, and is not affected.
func styleSecurityPolicy(nonce string) string {
	return "style-src 'nonce-" + nonce + "'; style-src-attr 'none'"
}
