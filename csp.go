package main

import "crypto/rand"

// Content-Security-Policy, phase 2: every <script> tag in index.html
// carries nonce="__CSP_NONCE__", and each index response replaces that marker
// with a fresh nonce named in the policy header. The header is enforcing, so
// violations are blocked. Phase 1 (Report-Only) ran on this same policy.
const (
	nonceMarker   = "__CSP_NONCE__"
	cspHeaderName = "Content-Security-Policy"
	// Style S1: the style policy is sent as Report-Only next to the enforced
	// script policy. It blocks nothing; a violation shows up in the console.
	styleHeaderName = "Content-Security-Policy-Report-Only"
)

// nonceRandRead is the random source for the CSP nonce. It is a variable only
// so a test can make it fail.
var nonceRandRead = rand.Read

// contentSecurityPolicy is the policy for the index page. 'unsafe-inline' and
// https: are the backward-compatibility fallback for browsers that predate
// nonces and 'strict-dynamic'; browsers that understand nonce plus
// 'strict-dynamic' (Chrome 52+, Firefox 52+, Safari 15.4+) ignore both.
func contentSecurityPolicy(nonce string) string {
	return "script-src 'nonce-" + nonce + "' 'strict-dynamic' https: 'unsafe-inline'; script-src-attr 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
}

// styleSecurityPolicy is the style policy for the index page (S1, Report-Only).
// The one <style> block carries the same nonce; style="" attributes are not
// allowed at all (style-src-attr 'none'), as the page has none (S0). Script
// that sets el.style.x is CSSOM, not an attribute, and is not affected.
func styleSecurityPolicy(nonce string) string {
	return "style-src 'nonce-" + nonce + "'; style-src-attr 'none'"
}
