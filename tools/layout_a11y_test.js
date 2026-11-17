// Static checks for the layout, mobile chip and modal accessibility changes
// (package D, items 1-12). Reads index.html (or the file in LAYOUT_HTML, so the
// same checks can run against an older copy) and asserts the CSS and markup.
"use strict";
const fs = require("fs");
const path = require("path");

const file = process.env.LAYOUT_HTML || path.join(__dirname, "..", "index.html");
const html = fs.readFileSync(file, "utf8").replace(/\r\n/g, "\n");
const styleOpen = /<style\b[^>]*>/.exec(html);
const css = styleOpen ? html.slice(styleOpen.index + styleOpen[0].length, html.indexOf("</style>")).replace(/\/\*[^]*?\*\//g, "") : "";
const flat = css.replace(/\s+/g, " ");

let ran = 0, failed = 0;
function check(label, ok) {
  ran++;
  if (!ok) failed++;
  console.log((ok ? "PASS  " : "FAIL  ") + label);
}
// The body of the first top-level rule whose selector is exactly sel.
function rule(sel, src) {
  const s = src === undefined ? flat : src;
  const re = new RegExp("(?:^|[}\\s])" + sel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + " \\{([^}]*)\\}");
  const m = re.exec(s);
  return m ? m[1] : null;
}
// The inside of an @media block whose prelude contains q (balanced braces).
function media(q) {
  const out = [];
  let i = flat.indexOf("@media");
  while (i >= 0) {
    const open = flat.indexOf("{", i);
    if (flat.slice(i, open).includes(q)) {
      let d = 0, j = open;
      for (; j < flat.length; j++) { if (flat[j] === "{") d++; else if (flat[j] === "}" && --d === 0) break; }
      out.push(" " + flat.slice(open + 1, j));
    }
    i = flat.indexOf("@media", i + 1);
  }
  return out.join(" ");
}
const has = (body, decl) => !!body && new RegExp("(^|;)\\s*" + decl.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\ /g, "\\s*") + "\\s*(;|$)").test(body);

// 1 grid
check("1 .grid has 4 columns by default", has(rule(".grid"), "grid-template-columns: repeat(4, minmax(0, 1fr))"));
check("1 @media (max-width: 1199px) sets 2 columns", /\.grid \{ grid-template-columns: repeat\(2, minmax\(0, 1fr\)\);? \}/.test(media("max-width: 1199px")));
check("1 @media (max-width: 768px) sets 1 column", /\.grid \{ grid-template-columns: minmax\(0, 1fr\);? \}/.test(media("max-width: 768px")));
// 2 card flex column + button at the bottom
check("2 .card is a flex column", has(rule(".card"), "display: flex") && has(rule(".card"), "flex-direction: column"));
check("2 .card-btn has margin-top: auto", has(rule(".card-btn"), "margin-top: auto"));
// 3 sliders
check("3 range inputs use accent-color #ea580c", has(rule('input[type="range"]'), "accent-color: #ea580c"));
// 4 close button
check("4 .modal-close is at least 44x44", has(rule(".modal-close"), "min-width: 44px") && has(rule(".modal-close"), "min-height: 44px"));
check("4 close glyph unchanged (x, 1.5rem)", (html.match(/class="modal-close"[^>]*>×<\/button>/g) || []).length === 4 && has(rule(".modal-close"), "font-size: 1.5rem"));
// 5 card hover
check("5 .card has no cursor: pointer", !!rule(".card") && !/cursor\s*:\s*pointer/.test(rule(".card")));
check("5 .card:hover has no transform", !!rule(".card:hover") && !/transform/.test(rule(".card:hover")));
check("5 .card-btn:hover keeps its transform", /transform/.test(rule(".card-btn:hover") || ""));
// 6 disclaimer + footer
for (const s of [".disclaimer-panel", ".footer"]) {
  const r = rule(s);
  check("6 " + s + " max-width 1160px, width calc(100% - 40px), margin auto", has(r, "max-width: 1160px") &&
    has(r, "width: calc(100% - 40px)") && /margin: \d+px auto 0/.test(r || ""));
}
// 7 mobile chips
const chipToggles = [...html.matchAll(/<button\b[^>]*class="chip-toggle"[^>]*>([^<]*)<\/button>/g)];
check("7 two Show all buttons (type=button, aria-expanded=false, aria-controls -> a .hero-chips id)", chipToggles.length === 2 &&
  chipToggles.every(m => /type="button"/.test(m[0]) && /aria-expanded="false"/.test(m[0]) && m[1] === "Show all" &&
    new RegExp('class="hero-chips[^"]*" id="' + (/aria-controls="([^"]+)"/.exec(m[0]) || [])[1] + '"').test(html)));
check("7 .chip-toggle hidden on desktop", has(rule(".chip-toggle"), "display: none"));
const m768 = media("max-width: 768px");
check("7 mobile collapse uses max-height + overflow: hidden on .hero-chips:not(.chips-open)",
  /\.hero-chips:not\(\.chips-open\) \{[^}]*max-height:[^}]*overflow: hidden/.test(m768));
check("7 toggleChips toggles .chips-open and sets aria-expanded + Show less", /classList\.toggle\('chips-open'\)/.test(html) &&
  /setAttribute\('aria-expanded'/.test(html) && /'Show less'/.test(html));
// 8 modal markup
const MODALS = ["checkModal", "firmModal", "recentModal", "referenceModal"];
for (const id of MODALS) {
  const tag = new RegExp('<div class="modal" id="' + id + '" role="dialog" aria-modal="true" aria-labelledby="([^"]+)">').exec(html);
  check("8 #" + id + " role=dialog aria-modal=true aria-labelledby -> its h2", !!tag &&
    new RegExp('<h2 id="' + tag[1] + '">').test(html.slice(tag.index, html.indexOf("</h2>", tag.index) + 5)));
  check("8 #" + id + " close button type=button aria-label=Close",
    html.includes('<button type="button" aria-label="Close" class="modal-close" data-action="close-modal" data-modal="' + id + '">'));
}
// 9 openers are buttons
const openerTags = html.match(/<[a-z]+\b[^<>]*data-action="open-modal"[^<>]*>/g) || [];
check("9 the 4 open-modal openers are <button>s (found " + openerTags.length + ")", openerTags.length === 4 &&
  openerTags.every(t => t.startsWith("<button")));
// 10 focus handling
check("10 openModal remembers the opener and focuses .modal-close", /modalOpener = clickedOpener \|\| document\.activeElement/.test(html) &&
  /querySelector\('\.modal-close'\)[^]{0,40}close\.focus\(\)/.test(html));
check("10 closeModal returns focus to the opener", /function closeModal\(id\) \{[^]*?modalOpener\.focus\(\)[^]*?\n    \}/.test(html));
check("10 Tab trap skips hidden and tabindex=-1 and wraps both ways", /function trapModalTab\(e\)/.test(html) &&
  /getAttribute\('tabindex'\) !== '-1'/.test(html) && /closest\('\[hidden\]'\)/.test(html) && /e\.shiftKey && cur === first/.test(html));
// 11 Escape order unchanged
const kd = html.split("document.addEventListener('keydown'").length - 1;
check("11 three document keydown listeners (modal Escape, Tab trap, export menu) (found " + kd + ")", kd === 3);
check("11 modal Escape: popover first, then menu, then modals",
  /if \(closeFlagPopover\(\)\) return;\s*\/\/[^\n]*\n\s*if \(e\.defaultPrevented \|\| anyExportMenuOpen\(\)\) return;\s*\['checkModal', 'firmModal', 'recentModal', 'referenceModal'\]\.forEach\(closeModal\);/.test(html));
// 12 reduced motion
const rm = media("prefers-reduced-motion: reduce");
check("12 reduced motion: scroll-behavior auto", /html \{ scroll-behavior: auto;? \}/.test(rm));
check("12 reduced motion: transitions off", /transition: none !important/.test(rm));
check("12 reduced motion: hover transforms none", /:hover[^{]*\{ transform: none;? \}/.test(rm));
check("12 reduced motion: modal max-width transition off", /\.modal-content \{ transition: none;? \}/.test(rm));
check("12 spinner animation kept (not disabled)", !/animation\s*:\s*none/.test(rm) && /animation: spin/.test(flat));

if (ran === 0) { console.error("FAIL: zero checks ran"); process.exit(1); }
console.log((ran - failed) + "/" + ran + " passed, " + failed + " failed");
process.exit(failed ? 1 : 0);
