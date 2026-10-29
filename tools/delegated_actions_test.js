// Delegated data-action / data-mirror listeners test (CSP phase 0).
//
// Run: node tools/delegated_actions_test.js
//
// index.html carries data-action="..." and data-mirror="..." instead of inline
// on*= handlers; two named listeners in the classic script act on them.
// no_inline_handlers_test.js proves the handlers are gone; this proves the
// replacement works:
//   T4 each listener function is defined once and registered on document once;
//   T5 every data-action value used in index.html has a branch in the listener,
//      and every branch is used (both directions); the data-mirror targets are
//      exactly the three range labels;
//   T6 the real listener functions run in a vm sandbox and call the real
//      function names with the old arguments, one case per action, per chip
//      and per range mirror.
// A listener that cannot be found is a FAIL, never a silent pass.
"use strict";
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const raw = fs.readFileSync(path.join(__dirname, "..", "index.html"), "utf8").replace(/\r\n/g, "\n");
// HTML comments are blanked character by character so line numbers still match.
const html = raw.replace(/<!--[\s\S]*?-->/g, m => m.replace(/[^\n]/g, " "));
const lineOf = i => html.slice(0, i).split("\n").length;

let ran = 0;
let failed = 0;
function check(label, ok) {
  ran++;
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}
const count = s => html.split(s).length - 1;

// A function runs from "function NAME(e) {" to the first "\n    }" after it;
// the opening must occur exactly once.
function cut(name) {
  const open = "function " + name + "(e) {";
  const n = count(open);
  if (n !== 1) return { found: n, code: "" };
  const at = html.indexOf(open);
  const end = html.indexOf("\n    }", at);
  if (end < 0) return { found: 1, code: "" };
  return { found: 1, code: html.slice(at, end + 6), line: lineOf(at) };
}

// ── T4: definitions and registrations ──
const fnOf = { click: "onActionClick", input: "onMirrorInput" };
const L = {};
for (const t of Object.keys(fnOf)) {
  L[t] = cut(fnOf[t]);
  check("T4 " + fnOf[t] + " defined exactly once (found " + L[t].found +
    (L[t].line ? ", L" + L[t].line : "") + ")", L[t].found === 1 && L[t].code !== "");
  const reg = "document.addEventListener('" + t + "', " + fnOf[t] + ");";
  check("T4 " + reg + " exactly once (found " + count(reg) + ")", count(reg) === 1);
}

// ── T5: used values vs handled branches ──
function usedValues(attr) {
  const used = new Map();
  const re = new RegExp(attr + "\\s*=\\s*\\\\?[\"']([^\"'\\\\]*)", "g");
  html.split("\n").forEach((ln, i) => {
    if (/^\s*\/\//.test(ln)) return;
    for (const m of ln.matchAll(re)) {
      if (!used.has(m[1])) used.set(m[1], []);
      used.get(m[1]).push(i + 1);
    }
  });
  return used;
}
const branches = [...new Set([...L.click.code.matchAll(/\ba === '([^']*)'/g)].map(m => m[1]))];
const used = usedValues("data-action");
console.log("      onActionClick branches: " + (branches.join(", ") || "(none)"));
console.log("      data-action used: " +
  ([...used].map(([v, ls]) => v + " (" + ls.length + "x)").join(", ") || "(none)"));
const missing = [...used.keys()].filter(v => !branches.includes(v));
const dead = branches.filter(v => !used.has(v));
check("T5 onActionClick has at least one branch (found " + branches.length + ")", branches.length > 0);
check("T5 every data-action has a branch (missing: " + (missing.join(", ") || "none") + ")", missing.length === 0);
check("T5 every onActionClick branch is used (dead: " + (dead.join(", ") || "none") + ")", dead.length === 0);
const MIRRORS = ["firmLimitVal", "recentDaysVal", "recentLimitVal"];
const mirrors = [...usedValues("data-mirror").keys()].sort();
check("T5 data-mirror targets are " + MIRRORS.join(", ") + " (found " + (mirrors.join(", ") || "none") + ")",
  JSON.stringify(mirrors) === JSON.stringify(MIRRORS));
for (const s of ["submit-check", "submit-firm", "submit-recent", "submit-reference", "sign-in"]) {
  const n = (used.get(s) || []).length;
  check("T5 data-action=\"" + s + "\" used exactly once (found " + n + ")", n === 1);
}
const MODALS = ["checkModal", "firmModal", "recentModal", "referenceModal"];
for (const a of ["open-modal", "close-modal"]) {
  for (const m of MODALS) {
    const tag = 'data-action="' + a + '" data-modal="' + m + '"';
    check("T5 " + tag + " exactly once (found " + count(tag) + ")", count(tag) === 1);
  }
}

// The chips: every prefill element with its data-arg, HTML entities decoded.
const decode = s => s.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, "<")
  .replace(/&gt;/g, ">").replace(/&amp;/g, "&");
const chips = [...html.matchAll(/data-action="(prefill-check|prefill-firm)" data-arg="([^"]*)"/g)]
  .map(m => ({ action: m[1], arg: decode(m[2]) }));
const nCheck = chips.filter(c => c.action === "prefill-check").length;
const nFirm = chips.filter(c => c.action === "prefill-firm").length;
check("T5 40 chips = 20 prefill-check + 20 prefill-firm (found " + nCheck + " + " + nFirm + ")",
  nCheck === 20 && nFirm === 20);

// ── T6: drive the real listeners in a vm sandbox (fresh sandbox per case) ──
function harness() {
  const calls = [];
  const rec = name => (...args) => { calls.push(name + "(" + args.map(a => (a === undefined ? "undefined" : JSON.stringify(a))).join(",") + ")"); };
  const sb = {
    prefillCheck: rec("prefillCheck"), prefillFirm: rec("prefillFirm"),
    openModal: rec("openModal"), closeModal: rec("closeModal"),
    submitCheck: rec("submitCheck"), submitFirm: rec("submitFirm"),
    submitRecent: rec("submitRecent"), submitReference: rec("submitReference"),
    pubveraSignIn: rec("pubveraSignIn"), updateLabel: rec("updateLabel"),
  };
  vm.createContext(sb);
  vm.runInContext(L.click.code + "\n" + L.input.code, sb);
  // The click target is the element inside the button; closest() walks up to it.
  const click = el => sb.onActionClick({ target: { closest: s => (s === "[data-action]" ? el : null) } });
  const input = (dataset, value) => sb.onMirrorInput({ target: { dataset, value } });
  return { calls, click, input };
}
function tcase(label, fn, want) {
  let got = null, why = "";
  try { got = fn(harness()); } catch (e) { why = " (threw: " + e.message + ")"; }
  const ok = JSON.stringify(got) === JSON.stringify(want);
  check("T6 " + label + why + (ok || why ? "" : " (got " + JSON.stringify(got) + ")"), ok);
}
const clickCase = (label, dataset, want) => tcase(label, h => { h.click({ dataset }); return h.calls; }, want);

for (const c of chips) {
  const fn = c.action === "prefill-check" ? "prefillCheck" : "prefillFirm";
  clickCase("chip " + c.action + " " + JSON.stringify(c.arg), { action: c.action, arg: c.arg },
    [fn + "(" + JSON.stringify(c.arg) + ")"]);
}
for (const m of MODALS) {
  clickCase("open-modal " + m, { action: "open-modal", modal: m }, ['openModal("' + m + '")']);
  clickCase("close-modal " + m, { action: "close-modal", modal: m }, ['closeModal("' + m + '")']);
}
clickCase("submit-check", { action: "submit-check" }, ["submitCheck()"]);
clickCase("submit-firm", { action: "submit-firm" }, ["submitFirm()"]);
clickCase("submit-recent", { action: "submit-recent" }, ["submitRecent()"]);
clickCase("submit-reference", { action: "submit-reference" }, ["submitReference()"]);
clickCase("sign-in", { action: "sign-in" }, ["pubveraSignIn()"]);
clickCase("unknown data-action does nothing", { action: "nope" }, []);
tcase("click outside any [data-action] does nothing", h => { h.click(null); return h.calls; }, []);
for (const id of MIRRORS) {
  tcase("range input mirrors into " + id, h => { h.input({ mirror: id }, "17"); return h.calls; },
    ['updateLabel("' + id + '","17")']);
}
tcase("input without data-mirror does nothing", h => { h.input({}, "99"); return h.calls; }, []);

if (ran === 0) {
  console.error("FAIL: zero checks ran - refusing to pass silently");
  process.exit(1);
}
console.log((ran - failed) + "/" + ran + " passed, " + failed + " failed");
process.exit(failed ? 1 : 0);
