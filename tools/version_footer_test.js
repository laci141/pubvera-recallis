// Footer version test.
//
// Run: node tools/version_footer_test.js
//
// The footer shows "Recallis v<version>" from GET /version. This static check
// reads index.html and asserts:
//   1. exactly one element has id="app-version";
//   2. the page fetches '/version' with cache 'no-store';
//   3. the text is written with textContent, never innerHTML;
//   4. a failed fetch leaves the element empty (no error text is shown).
"use strict";
const fs = require("fs");
const path = require("path");

const html = fs.readFileSync(path.join(__dirname, "..", "index.html"), "utf8").replace(/\r/g, "");

let failed = 0;
function check(label, ok) {
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}

const ids = html.match(/\bid=["']app-version["']/g) || [];
check("1 exactly one id=\"app-version\" (found " + ids.length + ")", ids.length === 1);

// The script block that does the fetch.
const scripts = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)].map((m) => m[1]);
const block = scripts.find((s) => /fetch\(\s*['"]\/version['"]/.test(s)) || "";
check("2 fetches '/version'", block !== "");
check("2 fetch uses cache: 'no-store'", /fetch\(\s*['"]\/version['"]\s*,\s*\{[^}]*cache:\s*['"]no-store['"]/.test(block));

check("3 writes with textContent", /\.textContent\s*=\s*['"]Recallis v['"]\s*\+/.test(block));
check("3 never uses innerHTML", block !== "" && !/innerHTML/.test(block));

// 4: the catch handler must not write anything.
const c = block.match(/\.catch\(\s*\(\)\s*=>\s*\{([^}]*)\}\s*\)/);
check("4 has a .catch handler", !!c);
check("4 catch handler writes nothing", !!c && !/textContent|innerHTML|innerText|append/.test(c[1]));

if (failed) {
  console.error(failed + " check(s) failed");
  process.exit(1);
}
console.log("all footer version checks pass");
