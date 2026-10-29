// No inline event handlers test (CSP phase 0).
//
// Run: node tools/no_inline_handlers_test.js
//
// A Content-Security-Policy with script-src-attr 'none' blocks every inline
// event handler attribute (onclick="..."), including ones inserted through
// innerHTML, so index.html must carry none, neither in markup nor in HTML
// built inside JS strings. Clicks and range input go through delegated
// listeners that read data-action / data-mirror instead. Source
// check over the whole file (HTML comments blanked first, line numbers kept):
//   T1 no on*= attribute anywhere, script strings included;
//   T2 no "javascript:" URL;
//   T3 no setAttribute('on...') (a string handler set from JS).
"use strict";
const fs = require("fs");
const path = require("path");

const raw = fs.readFileSync(path.join(__dirname, "..", "index.html"), "utf8").replace(/\r\n/g, "\n");
if (!/<script\b/.test(raw)) {
  console.error("FAIL: no <script> found in index.html - refusing to pass silently");
  process.exit(1);
}
// HTML comments are blanked character by character so reported line numbers
// still match the file.
const html = raw.replace(/<!--[\s\S]*?-->/g, m => m.replace(/[^\n]/g, " "));
const lineOf = i => html.slice(0, i).split("\n").length;

let ran = 0;
let failed = 0;
function check(label, ok) {
  ran++;
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}

// ── T1: no inline handler attribute (an escaped quote \" counts too) ──
const inline = [...html.matchAll(/\son[a-z]+\s*=\s*\\?["'`]/gi)];
check("T1 no on*= attribute in index.html (found " + inline.length + ")", inline.length === 0);
for (const m of inline) console.log("      L" + lineOf(m.index) + ": " + m[0].trim());

// ── T2: no javascript: URL ──
const jsUrls = [...html.matchAll(/javascript\s*:/gi)];
check("T2 no javascript: URL in index.html (found " + jsUrls.length + ")", jsUrls.length === 0);
for (const m of jsUrls) console.log("      L" + lineOf(m.index) + ": " + m[0]);

// ── T3: no setAttribute('on...') ──
const setAttr = [...html.matchAll(/setAttribute\s*\(\s*["'`]on/gi)];
check("T3 no setAttribute('on...') in index.html (found " + setAttr.length + ")", setAttr.length === 0);
for (const m of setAttr) console.log("      L" + lineOf(m.index) + ": " + m[0]);

if (ran === 0) {
  console.error("FAIL: zero checks ran - refusing to pass silently");
  process.exit(1);
}
console.log((ran - failed) + "/" + ran + " passed, " + failed + " failed");
process.exit(failed ? 1 : 0);
