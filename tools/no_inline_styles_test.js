// No inline style attributes test (CSP style phase S0).
//
// Run: node tools/no_inline_styles_test.js
//
// A Content-Security-Policy with style-src-attr 'none' blocks every style
// attribute, also in HTML built by script (innerHTML) and setAttribute('style').
// Script that assigns el.style.x / style.setProperty keeps working, so those are
// not checked here. index.html therefore carries none, and the classes that
// replaced them must all exist and all be used:
//   S1 no style="..." / style='...' anywhere, script strings included;
//   S2 no setAttribute('style', ...);
//   S3 each managed class is defined exactly once, in the doubled form
//      .x.x { } (specificity 0,2,0, so it wins over component rules such as
//      .card p as the inline style did), and is used at least once;
//   S4 no u-/tone- class is used that is not a managed class.
"use strict";
const fs = require("fs");
const path = require("path");

const raw = fs.readFileSync(path.join(__dirname, "..", "index.html"), "utf8").replace(/\r\n/g, "\n");
if (!/<style\b/.test(raw) || !/<script\b/.test(raw)) {
  console.error("FAIL: no <style> or <script> found in index.html - refusing to pass silently");
  process.exit(1);
}
// HTML comments are blanked character by character so line numbers still match.
const html = raw.replace(/<!--[\s\S]*?-->/g, m => m.replace(/[^\n]/g, " "));
const lineOf = i => html.slice(0, i).split("\n").length;
const css = (html.match(/<style\b[^>]*>([\s\S]*?)<\/style>/) || [, ""])[1];
const cssNoComments = css.replace(/\/\*[\s\S]*?\*\//g, "");

let ran = 0;
let failed = 0;
function check(label, ok) {
  ran++;
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}

// ── S1: no style attribute (an escaped quote \" counts too) ──
const attrs = [...html.matchAll(/\sstyle\s*=\s*\\?["'`]/gi)];
check("S1 no style attribute in index.html (found " + attrs.length + ")", attrs.length === 0);
for (const m of attrs) console.log("      L" + lineOf(m.index) + ": " + m[0].trim());

// ── S2: no setAttribute('style', ...) ──
const setAttr = [...html.matchAll(/setAttribute\s*\(\s*["'`]style["'`]/gi)];
check("S2 no setAttribute('style') in index.html (found " + setAttr.length + ")", setAttr.length === 0);

// ── S3/S4: the managed classes ──
const MANAGED = [
  "firm-chip-row", "recall-number-input", "recall-na", "hl-mark",
  "negated-notice", "table-hint", "summary-box",
];
const body = html.replace(/<style\b[^>]*>[\s\S]*?<\/style>/, m => m.replace(/[^\n]/g, " "));   // markup + scripts only
const classTokens = new Set();
for (const m of body.matchAll(/\bclass\s*=\s*\\?["']([^"'\\]*)/g)) {
  for (const t of m[1].split(/\s+/)) if (t && !t.includes("${")) classTokens.add(t);
}

for (const c of MANAGED) {
  const doubled = new RegExp("(^|[\\s,}])\\." + c + "\\." + c + "\\s*\\{", "g");
  const n = (cssNoComments.match(doubled) || []).length;
  const single = new RegExp("(^|[\\s,}])\\." + c + "\\s*\\{", "g");
  const nSingle = (cssNoComments.match(single) || []).length;
  check("S3 ." + c + " defined once as .x.x (found " + n + " doubled, " + nSingle + " single)", n === 1 && nSingle === 0);
  check("S3 ." + c + " is used", classTokens.has(c));
}
const undefinedUsed = [...classTokens].filter(t => /^(u|tone)-/.test(t) && !MANAGED.includes(t));
check("S4 every u-/tone- class used is a managed class (undefined: " + (undefinedUsed.join(", ") || "none") + ")",
  undefinedUsed.length === 0);

if (ran === 0) {
  console.error("FAIL: zero checks ran - refusing to pass silently");
  process.exit(1);
}
console.log((ran - failed) + "/" + ran + " passed, " + failed + " failed");
process.exit(failed ? 1 : 0);
