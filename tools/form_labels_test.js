// Static checks for form label association and the slider hit area.
// Reads index.html, or the HTML file given as the first argument (mutation run).
"use strict";
const fs = require("fs");
const path = require("path");

const file = process.argv[2] || path.join(__dirname, "..", "index.html");
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
const count = (re) => (html.match(re) || []).length;

const ids = ["checkDrug", "checkClass", "firmName", "firmLimit", "recentDays", "recentLimit", "recallNumber"];
for (const id of ids) {
  check("label for=" + id + " appears exactly once", count(new RegExp("<label[^>]*[ ]for=\"" + id + "\"", "g")) === 1);
  check("element id=" + id + " exists exactly once", count(new RegExp("[ ]id=\"" + id + "\"", "g")) === 1);
}

// Every <label> has for= unless it wraps its control.
const labels = html.match(/<label[^>]*>[^]*?<[/]label>/g) || [];
const bad = labels.filter((l) => !/^<label[^>]*[ ]for="/.test(l) && !/<(input|select|textarea)[ >]/.test(l));
check("no <label> without for= (found " + bad.length + ")", labels.length > 0 && bad.length === 0);

// Slider hit area.
const range = /input.type="range". [{]([^}]*)[}]/.exec(flat);
check("range rule has height: 24px", !!range && /(^|;)[ ]*height: 24px[ ]*(;|$)/.test(range[1]));
const coarse = /@media [(]pointer: coarse[)] [{] input.type="range". [{]([^}]*)[}]/.exec(flat);
check("pointer: coarse block has range height: 44px", !!coarse && /(^|;)[ ]*height: 44px[ ]*(;|$)/.test(coarse[1]));

if (ran === 0) { console.error("FAIL: zero checks ran"); process.exit(1); }
console.log((ran - failed) + "/" + ran + " passed, " + failed + " failed");
process.exit(failed ? 1 : 0);
