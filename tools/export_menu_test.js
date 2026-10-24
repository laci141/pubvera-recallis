// Export menu test (E1-E15): every result toolbar has ONE "Export" menu button
// (WAI-ARIA APG menu button pattern) instead of four separate download buttons,
// and Escape is spent on the topmost layer: the badge popover first, then an
// open Export menu, then the modal around it.
//
// Run: node tools/export_menu_test.js
//
// Behaviour test without a browser. esc(), escAttr() and metaRow() are cut out
// of index.html by a brace-balanced scan, the block between `// ── Export menu`
// and `// ── end Export menu` (the menu functions and the delegated click and
// keydown listeners) is cut out as a slice, and so is the existing document
// keydown listener that closes the modals on Escape and maps Enter to the
// submit functions. They run inside node:vm against a tiny fake DOM and the
// real listeners are driven with fake click and keydown events.
// closeFlagPopover() is a stub that reports whether a popover was open; the
// download functions are stubs that click a synthetic anchor, like
// triggerDownload() does. Layout (343-1366 px, the open panel inside the
// viewport) and the real-browser listener order are NOT checked here: that is
// the Playwright script's job.
"use strict";
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const { textDocument } = require("./html_functions");

const html = fs.readFileSync(path.join(__dirname, "..", "index.html"), "utf8").replace(/\r\n/g, "\n");

// balanced returns the index just past the "}" that closes the first "{" at or
// after `from`. The code read here holds no unbalanced braces in strings, so a
// plain depth count is enough.
function balanced(from) {
  const open = html.indexOf("{", from);
  if (open < 0) return -1;
  let depth = 0;
  for (let i = open; i < html.length; i++) {
    if (html[i] === "{") depth++;
    else if (html[i] === "}") {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  return -1;
}
// extractFunction returns { start, end } of "function <name>(...){ ... }".
function extractFunction(name) {
  const start = html.indexOf("function " + name + "(");
  if (start < 0) return null;
  const end = balanced(start);
  return end < 0 ? null : { start: start, end: end };
}

let ran = 0;
let failed = 0;
function check(label, ok) {
  ran++;
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}
function refuse(what) {
  console.error("FAIL: " + what + " not found in index.html - refusing to pass silently");
  process.exit(1);
}

// ── Source blocks ──
const escFn = extractFunction("esc");
const escAttrFn = extractFunction("escAttr");
const meta = extractFunction("metaRow");
if (!escFn) refuse("function esc");
if (!escAttrFn) refuse("function escAttr");
if (!meta) refuse("function metaRow");
const escSrc = html.slice(escFn.start, escFn.end) + "\n" + html.slice(escAttrFn.start, escAttrFn.end);
const metaSrc = html.slice(meta.start, meta.end);
// The menu block is new: when it is missing the checks still run (and fail)
// so the output shows what is absent, but the test exits 1 regardless.
const a = html.indexOf("// ── Export menu");
const b = html.indexOf("// ── end Export menu");
let menuSrc = "";
let menuMissing = false;
if (a < 0 || b < a || html.slice(meta.end, a).trim() !== "") {
  menuMissing = true;
  console.error("FAIL: the `// ── Export menu` .. `// ── end Export menu` block right after metaRow " +
    "not found in index.html - refusing to pass silently");
} else {
  menuSrc = html.slice(a, b);
}
// The existing modal Escape listener: the one document keydown listener outside
// the menu block that calls closeModal.
const modalKeys = [];
for (let i = html.indexOf("document.addEventListener('keydown'"); i >= 0;
  i = html.indexOf("document.addEventListener('keydown'", i + 1)) {
  if (!menuMissing && i > a && i < b) continue;
  const close = balanced(i);
  if (close < 0) continue;
  const tail = html.slice(close).match(/^\s*\);?/);
  const src = html.slice(i, close) + (tail ? tail[0] : ");");
  if (src.includes("closeModal")) modalKeys.push({ start: i, src: src });
}
if (modalKeys.length !== 1) refuse("exactly one document keydown listener that calls closeModal (" + modalKeys.length + " found)");
const modalEsc = modalKeys[0];
if (!modalEsc.src.includes("closeFlagPopover")) refuse("closeFlagPopover() in the modal keydown listener");
const styleStart = html.indexOf("<style>");
const styleEnd = html.indexOf("</style>");
if (styleStart < 0 || styleEnd < styleStart) refuse("the <style> block");
const style = html.slice(styleStart, styleEnd).replace(/\/\*[^]*?\*\//g, "");

// ── tiny DOM: enough for the selectors the toolbar and menu code use ──
let currentDoc = null; // the document new elements belong to
class El {
  constructor(tag, attrs) { this.tag = tag; this.attrs = attrs || {}; this.children = []; this.parent = null; this.doc = currentDoc; }
  getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
  setAttribute(n, v) { this.attrs[n] = String(v); }
  removeAttribute(n) { delete this.attrs[n]; }
  hasAttribute(n) { return n in this.attrs; }
  get hidden() { return this.hasAttribute("hidden"); }
  set hidden(v) { if (v) this.attrs.hidden = ""; else delete this.attrs.hidden; }
  get id() { return this.attrs.id || ""; }
  get dataset() {
    const d = {};
    for (const k in this.attrs) if (k.startsWith("data-")) d[k.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase())] = this.attrs[k];
    return d;
  }
  get classList() { const c = (this.attrs.class || "").split(/\s+/); return { contains: x => c.includes(x) }; }
  focus() { this.doc.activeElement = this; }
  contains(o) { for (; o; o = o.parent) if (o === this) return true; return false; }
  all() { const out = []; const walk = e => e.children.forEach(c => { out.push(c); walk(c); }); walk(this); return out; }
  matches(sel) { return sel.split(",").some(s => matchChain(this, s.trim().split(/\s+/).filter(x => x !== ">"))); }
  closest(sel) { for (let e = this; e; e = e.parent) if (e.matches && e.matches(sel)) return e; return null; }
  querySelectorAll(sel) { return this.all().filter(e => e.matches(sel)); }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}
function matchSimple(e, s) {
  if (!e || !(e instanceof El)) return false;
  const parts = s.match(/:not\(\[[^\]]+\]\)|\[[^\]]+\]|\.[\w-]+|^[a-z]+/g) || [];
  return parts.length > 0 && parts.every(p => {
    if (p.startsWith(":not(")) return !matchSimple(e, p.slice(5, -1));
    if (p[0] === ".") return e.classList.contains(p.slice(1));
    if (p[0] === "[") {
      const m = p.match(/^\[([\w-]+)(?:="([^"]*)")?\]$/);
      return m[2] === undefined ? e.hasAttribute(m[1]) : e.getAttribute(m[1]) === m[2];
    }
    return e.tag === p;
  });
}
function matchChain(e, parts) {
  if (!matchSimple(e, parts[parts.length - 1])) return false;
  if (parts.length === 1) return true;
  for (let p = e.parent; p; p = p.parent) if (matchChain(p, parts.slice(0, -1))) return true;
  return false;
}
function parse(src, root) {
  let cur = root;
  for (const m of src.matchAll(/<(\/?)(\w+)([^>]*)>/g)) {
    if (m[1]) { cur = cur.parent; continue; }
    const attrs = {};
    for (const at of m[3].matchAll(/([\w-]+)(?:="([^"]*)")?/g)) attrs[at[1]] = at[2] === undefined ? "" : at[2];
    const el = new El(m[2], attrs);
    el.parent = cur;
    cur.children.push(el);
    if (!/^(input|br|img)$/.test(m[2])) cur = el;
  }
}

// The four toolbars as the modules render them: check, firm and recent with
// the quick filter and the Only Class I toggle, reference (a single record)
// with opts.noFilter.
const specs = [
  { args: '"25 of 38 recalls matched", "check", "recallis-recalls.json"', key: "check", base: "recallis-recalls", filter: true },
  { args: '"12 recalls", "firm", "recallis-firm-recalls.json"', key: "firm", base: "recallis-firm-recalls", filter: true },
  { args: '"50 recalls", "recent", "recallis-recent-recalls.json"', key: "recent", base: "recallis-recent-recalls", filter: true },
  { args: '"Recall D-0001-2026", "reference", "recallis-recall.json", { noFilter: true }', key: "reference", base: "recallis-recall", filter: false },
];
const MODAL_IDS = ["checkModal", "firmModal", "recentModal", "referenceModal"];
const BIB_TITLE = "BibTeX citations (@misc, keyed by recall number)";

// makeWorld builds one page: fake document, the extracted code and the four
// toolbars. reversed registers the menu listeners before the modal keydown
// listener instead of in source order (the modal listener comes first).
function makeWorld(reversed) {
  const w = { listeners: { click: [], keydown: [], input: [], change: [] }, calls: [], closed: [], submits: [], popOpen: false, popClosed: 0 };
  w.doc = {
    activeElement: null, body: null,
    createElement: textDocument.createElement,
    querySelector: s => w.body.querySelector(s), querySelectorAll: s => w.body.querySelectorAll(s),
    getElementById: id => w.body.all().find(e => e.id === id) || null,
    addEventListener: (t, f) => { if (w.listeners[t]) w.listeners[t].push(f); },
  };
  currentDoc = w.doc;
  w.body = new El("body");
  w.doc.body = w.body;
  w.doc.activeElement = w.body;
  w.fire = (type, target, trusted, key) => {
    const e = { type: type, target: target, key: key, isTrusted: trusted !== false, isComposing: false, defaultPrevented: false,
      preventDefault() { this.defaultPrevented = true; }, stopPropagation() {}, stopImmediatePropagation() {} };
    w.listeners[type].forEach(f => f(e));
    return e;
  };
  // The synthetic download anchor: triggerDownload() appends it to body and clicks it.
  w.anchor = new El("a");
  w.anchor.parent = w.body;
  w.body.children.push(w.anchor);
  // The search input of the check module, for the Enter-to-submit check.
  w.checkDrug = new El("input", { id: "checkDrug" });
  w.checkDrug.parent = w.body;
  w.body.children.push(w.checkDrug);
  const exporter = fmt => (k, f) => { w.calls.push(fmt + " " + k + " " + f); w.fire("click", w.anchor, false); };
  const submitter = name => () => { w.submits.push(name); };
  w.sandbox = {
    document: w.doc, console: console,
    closeModal: id => { w.closed.push(id); },
    closeFlagPopover: () => { if (!w.popOpen) return false; w.popOpen = false; w.popClosed++; return true; },
    submitCheck: submitter("submitCheck"), submitFirm: submitter("submitFirm"), submitReference: submitter("submitReference"),
    downloadXLSX: exporter("xlsx"), downloadCSV: exporter("csv"), downloadJSON: exporter("json"), downloadBibTeX: exporter("bib"),
  };
  vm.createContext(w.sandbox);
  vm.runInContext(escSrc + "\n" + metaSrc, w.sandbox);
  const parts = [{ at: a, src: menuSrc }, { at: modalEsc.start, src: modalEsc.src }]
    .filter(p => p.src).sort((x, y) => x.at - y.at);
  if (reversed) parts.reverse();
  parts.forEach(p => vm.runInContext(p.src, w.sandbox));
  w.order = parts.map(p => p.src === menuSrc ? "menu" : "modal").join(" -> ");
  w.bars = specs.map(s => {
    const src = vm.runInContext("metaRow(" + s.args + ")", w.sandbox);
    const holder = new El("div");
    holder.parent = w.body;
    w.body.children.push(holder);
    parse(src, holder);
    const trigger = holder.querySelector('[aria-haspopup="menu"]');
    const menu = trigger ? w.doc.getElementById(trigger.getAttribute("aria-controls")) : null;
    return Object.assign({ src: src, holder: holder, row: holder.querySelector(".meta-row"), trigger: trigger, menu: menu,
      items: menu ? menu.querySelectorAll('[role="menuitem"]') : [], triggers: holder.querySelectorAll('[aria-haspopup="menu"]') }, s);
  });
  w.reset = () => {
    w.bars.forEach(x => { if (x.menu) x.menu.hidden = true; if (x.trigger) x.trigger.setAttribute("aria-expanded", "false"); });
    w.doc.activeElement = w.body;
    w.calls.length = 0;
    w.closed.length = 0;
    w.submits.length = 0;
    w.popOpen = false;
    w.popClosed = 0;
  };
  return w;
}

const W = makeWorld(false);
const doc = W.doc, body = W.body, anchor = W.anchor, bars = W.bars, fire = W.fire, reset = W.reset;
const A = bars[0];
const allSrc = bars.map(x => x.src).join("\n");
// A step that throws (old markup, missing function) is a FAIL, not a crash.
function attempt(f) { try { return !!f(); } catch (e) { console.log("      note: " + e.message); return false; } }
function isOpen(x) { return !!x.menu && !x.menu.hidden && x.trigger.getAttribute("aria-expanded") === "true"; }
function isClosed(x) { return !!x.menu && x.menu.hidden && x.trigger.getAttribute("aria-expanded") === "false"; }

// ── markup ──
check("E1 one trigger \"⬇ Export ▾\" with aria-haspopup=menu, aria-expanded=false, aria-controls -> hidden role=menu panel (" +
  bars.map(x => x.triggers.length).join(",") + " triggers per toolbar)",
  bars.every(x => x.triggers.length === 1 && x.trigger.tag === "button" && x.trigger.getAttribute("type") === "button" &&
    x.trigger.getAttribute("aria-expanded") === "false" && />⬇ Export ▾<\/button>/.test(x.src) &&
    !!x.menu && x.holder.contains(x.menu) && x.menu.getAttribute("role") === "menu" && x.menu.hidden));

const want = { Excel: ["xlsx", ".xlsx"], CSV: ["csv", ".csv"], JSON: ["json", ".json"], BibTeX: ["bib", ".bib"] };
// Buttons outside the panel other than the trigger and the Only Class I toggle
// would be leftover download buttons.
const outside = bars.map(x => x.holder.querySelectorAll("button")
  .filter(e => e !== x.trigger && !e.closest(".dl-menu") && !e.classList.contains("qclass")).length +
  x.holder.querySelectorAll("[data-dl]").filter(e => !e.closest(".dl-menu")).length);
const filters = bars.map(x => x.row ? x.row.querySelectorAll(".dl-group input.qfilter").length : 0);
const toggles = bars.map(x => x.row ? x.row.querySelectorAll(".dl-group button.qclass").length : 0);
// key and filename go into attributes, so they must be escaped, quotes included.
const nasty = attempt(() => {
  const src = vm.runInContext("metaRow('x', 'k\"<1', 'a\"&b.json')", W.sandbox);
  return /data-key="k&quot;&lt;1"/.test(src) && /data-filename="a&quot;&amp;b\.json"/.test(src) && !/data-key="k"/.test(src);
});
check("E2 the panel holds four role=menuitem buttons Excel, CSV, JSON, BibTeX with data-dl, data-key, data-filename " +
  "(escaped: " + nasty + "); no download button outside the panel; the quick filter and Only Class I only without " +
  "opts.noFilter (" + bars.map(x => x.items.length).join(",") + " items, " + outside.join(",") + " outside, " +
  filters.join(",") + " filters, " + toggles.join(",") + " Class I toggles)",
  nasty && bars.every((x, n) => {
    const labels = x.menu ? [...x.src.matchAll(/role="menuitem"[^>]*>([^<]*)</g)].map(m => m[1]) : [];
    return x.items.length === 4 && outside[n] === 0 && labels.join(",") === "Excel,CSV,JSON,BibTeX" &&
      filters[n] === (x.filter ? 1 : 0) && toggles[n] === (x.filter ? 1 : 0) &&
      (!x.filter || />Only Class I<\/button>/.test(x.src)) &&
      x.items.every((it, i) => {
        const w = want[labels[i]];
        return it.tag === "button" && it.getAttribute("type") === "button" && it.getAttribute("data-dl") === w[0] &&
          it.getAttribute("data-key") === x.key && it.getAttribute("data-filename") === x.base + w[1];
      }) &&
      x.items[3].getAttribute("title") === BIB_TITLE;
  }));

const onAttr = (allSrc.match(/\son[a-z]+\s*=/gi) || []).length;
const styleAttr = (allSrc.match(/\sstyle\s*=/gi) || []).length;
check("E3 the toolbar markup has no on*= and no style= attribute (" + onAttr + " on*=, " + styleAttr + " style=)",
  onAttr === 0 && styleAttr === 0);

const ids = bars.map(x => x.menu ? x.menu.id : "");
check("E4 toolbars rendered one after another get different panel ids (" + ids.join(", ") + ")",
  ids.every(id => id !== "") && new Set(ids).size === ids.length &&
  bars.every(x => x.trigger.getAttribute("aria-controls") === x.menu.id));

// ── behaviour ──
reset();
check("E5 a trigger click opens: aria-expanded=true, hidden removed, first item focused", attempt(() => {
  fire("click", A.trigger);
  return isOpen(A) && doc.activeElement === A.items[0];
}));

check("E6 ArrowDown/ArrowUp cycle with wrap, Home/End jump to first/last", attempt(() => {
  reset();
  fire("click", A.trigger);
  const last = A.items[A.items.length - 1];
  fire("keydown", doc.activeElement, true, "ArrowDown");
  const d1 = doc.activeElement === A.items[1];
  fire("keydown", doc.activeElement, true, "End");
  const end = doc.activeElement === last;
  fire("keydown", doc.activeElement, true, "ArrowDown");
  const wrapDown = doc.activeElement === A.items[0];
  fire("keydown", doc.activeElement, true, "ArrowUp");
  const wrapUp = doc.activeElement === last;
  fire("keydown", doc.activeElement, true, "Home");
  return d1 && end && wrapDown && wrapUp && doc.activeElement === A.items[0] && isOpen(A);
}));

check("E7 Escape closes (aria-expanded=false, hidden back), returns focus to the trigger and calls preventDefault", attempt(() => {
  reset();
  fire("click", A.trigger);
  const e = fire("keydown", doc.activeElement, true, "Escape");
  return isClosed(A) && doc.activeElement === A.trigger && e.defaultPrevented;
}));

check("E8 ArrowDown, Enter and Space on the trigger open and focus the first item; Tab closes without preventDefault", attempt(() => {
  const opened = ["ArrowDown", "Enter", " "].map(k => {
    reset();
    A.trigger.focus();
    const e = fire("keydown", A.trigger, true, k);
    const ok = isOpen(A) && doc.activeElement === A.items[0] && e.defaultPrevented && W.submits.length === 0;
    if (!ok) console.log("      note: key " + JSON.stringify(k) + " did not open the menu");
    return ok;
  });
  reset();
  fire("click", A.trigger);
  const tab = fire("keydown", doc.activeElement, true, "Tab");
  return opened.every(Boolean) && isClosed(A) && !tab.defaultPrevented;
}));

check("E9 an outside trusted click closes; an untrusted click (the download anchor) is ignored", attempt(() => {
  reset();
  fire("click", A.trigger);
  fire("click", anchor, false);
  const keptOpen = isOpen(A);
  fire("click", body, true);
  return keptOpen && isClosed(A);
}));

const got = [];
check("E10 an item click runs its download function once with (key, filename), closes the menu and returns focus to the trigger", attempt(() => {
  const ok = A.items.length === 4 && A.items.every(it => {
    reset();
    fire("click", A.trigger);
    doc.activeElement = body; // WebKit does not focus a button on mouse click
    fire("click", it, true);
    got.push(W.calls.join("+"));
    return W.calls.length === 1 && isClosed(A) && doc.activeElement === A.trigger;
  });
  const exp = ["xlsx", "csv", "json", "bib"].map(f => f + " check recallis-recalls." + f);
  console.log("      calls: " + (got.join(" | ") || "none"));
  return ok && got.join(",") === exp.join(",");
}));

check("E11 opening a second menu closes the first", attempt(() => {
  reset();
  const B = bars[1];
  fire("click", A.trigger);
  const first = isOpen(A);
  fire("click", B.trigger);
  return first && isClosed(A) && isOpen(B) && doc.activeElement === B.items[0];
}));
reset();

// ── CSS ──
const used = new Set([...allSrc.matchAll(/class="([^"]+)"/g)].flatMap(m => m[1].split(/\s+/)));
const missing = [...used].filter(c => !new RegExp("\\." + c + "(?![\\w-])").test(style));
check("E12 every class used by the toolbar and menu markup exists in the CSS (missing: " + (missing.join(",") || "none") + ")",
  used.size > 0 && missing.length === 0);

const rules = [...style.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  .map(m => ({ sel: m[1].split(",").map(s => s.trim()), body: m[2].replace(/\s+/g, "") }));
const declsOf = sel => rules.filter(r => r.sel.includes(sel)).map(r => r.body).join(";");
const panel = declsOf(".dl-menu");
const panelAll = rules.filter(r => r.sel.some(s => /^\.dl-menu(?![\w-])/.test(s))).map(r => r.body).join(";");
const bg = (panel.match(/(?:^|;)background(?:-color)?:[^;]+/g) || []).pop() || "";
// The surface under the toolbar is .modal-content, rgba(35, 28, 40, 0.82):
// the panel takes that colour, solid at alpha 0.90.
const bgM = bg.match(/:rgba\((\d+),(\d+),(\d+),(0?\.\d+)\)$/);
const zM = panel.match(/(?:^|;)z-index:(\d+)/);
const trigCss = declsOf(".dl-trigger");
const itemCss = declsOf(".dl-item");
check("E13 .dl-menu: one solid rgba(35,28,40) background alpha 0.90, no gradient; absolute, right:0, z-index below the " +
  "popover's 1100, max-width:calc(100vw - 32px); trigger min-height/min-width 44px; items min-height 44px (background \"" +
  bg.replace(/^;/, "") + "\", z-index " + (zM ? zM[1] : "none") + ")",
  !!bgM && [bgM[1], bgM[2], bgM[3]].join(",") === "35,28,40" && Math.abs(parseFloat(bgM[4]) - 0.9) < 1e-9 &&
  !/gradient/.test(panelAll) &&
  /(^|;)position:absolute/.test(panel) && /(^|;)right:0/.test(panel) && !!zM && +zM[1] < 1100 &&
  /(^|;)max-width:calc\(100vw-32px\)/.test(panel) &&
  /(^|;)min-height:44px/.test(trigCss) && /(^|;)min-width:44px/.test(trigCss) && /(^|;)min-height:44px/.test(itemCss));

// ── Escape: popover, menu, modal ──
// Both listener orders: the source order (modal listener first), and the
// reverse, so the result does not depend on which listener is registered first.
const R = makeWorld(true);
const escNotes = [];
check("E14 Escape in an open menu closes the menu and calls closeModal 0 times; with no menu and no popover open Escape " +
  "still closes all four modals (orders: " + W.order + " | " + R.order + ")", [W, R].every(w => attempt(() => {
  const X = w.bars[0];
  w.reset();
  w.fire("click", X.trigger);
  const opened = isOpen(X);
  w.fire("keydown", w.doc.activeElement, true, "Escape");
  const menuEsc = opened && isClosed(X) && w.doc.activeElement === X.trigger && w.closed.length === 0;
  const afterMenu = w.closed.join(",");
  w.reset();
  w.fire("keydown", w.body, true, "Escape");
  const plain = w.closed.join(",");
  escNotes.push(w.order + ": menu Escape closeModal [" + afterMenu + "], plain Escape closeModal [" + plain + "]");
  return menuEsc && plain === MODAL_IDS.join(",");
})));
console.log("      " + escNotes.join(" | "));

const popNotes = [];
check("E15 Escape with the badge popover open closes only the popover (menus and modals untouched), in both orders; " +
  "Enter in #checkDrug still calls submitCheck", [W, R].every(w => attempt(() => {
  w.reset();
  w.popOpen = true;
  const e = w.fire("keydown", w.body, true, "Escape");
  const popOnly = w.popClosed === 1 && !w.popOpen && w.closed.length === 0 &&
    w.bars.every(x => !x.menu || isClosed(x));
  w.reset();
  const enter = w.fire("keydown", w.checkDrug, true, "Enter");
  const submitted = w.submits.join(",");
  popNotes.push(w.order + ": popover closed " + (popOnly ? "alone" : "NOT alone") + " (defaultPrevented " + e.defaultPrevented +
    "), Enter in #checkDrug -> [" + submitted + "]");
  return popOnly && submitted === "submitCheck" && enter.defaultPrevented;
})));
console.log("      " + popNotes.join(" | "));

if (ran === 0) {
  console.error("FAIL: zero checks ran - refusing to pass silently");
  process.exit(1);
}
console.log((ran - failed) + "/" + ran + " passed, " + failed + " failed");
process.exit(failed || menuMissing ? 1 : 0);
