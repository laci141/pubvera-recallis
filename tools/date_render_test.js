// Date rendering test.
//
// Run: node tools/date_render_test.js
//
// openFDA dates arrive as YYYYMMDD. isoDate()/fmtDate() return PLAIN TEXT (the
// exports use them too), so every place that puts a date into HTML must escape
// it there. This loads the REAL functions out of index.html and checks:
//   1. fmtDate() still returns plain, unescaped text;
//   2. rowCommon() renders a valid date and renders an invalid one as text;
//   3. no template in index.html interpolates fmtDate()/isoDate() unescaped —
//      this covers the reference detail view, which builds its HTML inline.
"use strict";
const { html, load, textDocument } = require("./html_functions");

let failed = 0;
function check(name, ok, detail) {
  if (ok) console.log("PASS " + name);
  else { failed++; console.log("FAIL " + name + (detail ? " :: " + detail : "")); }
}

const f = load({
  functions: [
    "esc", "escAttr", "isoDate", "fmtDate", "getClassBadge", "recallSearchUrl",
    "recallCell", "clipCell", "isMalformedQuantity", "isWhitespaceOnlyQuantityDefect",
    "malformedQuantityText", "isNegatedRow", "rowFlags", "rowCommon"
  ],
  consts: ["NEG_PREFIX", "NEG_SUFFIX"],
  globals: { document: textDocument }
});

const PAYLOAD = "<img src=x onerror=1>";
const SHORT = "<b>x</b>"; // fits in the 10 characters isoDate keeps

// 1) the formatter stays plain text
check("1) fmtDate valid 20261013", f.fmtDate("20261013") === "2026-10-13", f.fmtDate("20261013"));
check("1) isoDate valid 20261013", f.isoDate("20261013") === "2026-10-13");
check("1) fmtDate empty -> N/A", f.fmtDate("") === "N/A" && f.fmtDate(null) === "N/A" && f.fmtDate(undefined) === "N/A");
check("1) fmtDate invalid kept as text (first 10 chars)", f.fmtDate("2026-10-13T00:00") === "2026-10-13");
check("1) fmtDate HTML payload returned unescaped", f.fmtDate(PAYLOAD) === PAYLOAD.slice(0, 10),
  JSON.stringify(f.fmtDate(PAYLOAD)));

// The <td> rowCommon() renders for the date: the second cell.
function dateCell(rec) {
  const cells = f.rowCommon(rec, "").match(/<td\b[^>]*>[\s\S]*?<\/td>/g) || [];
  return cells[1] || "";
}
const base = { recall_number: "D-0001-2026", classification: "Class II", product_description: "Aspirin 81 mg", reason_for_recall: "CGMP" };

// 2) rowCommon(), the table view
check("2) rowCommon valid date renders", dateCell({ ...base, recall_initiation_date: "20261013" }) === '<td class="nowrap">2026-10-13</td>',
  dateCell({ ...base, recall_initiation_date: "20261013" }));
check("2) rowCommon missing date renders N/A", dateCell({ ...base }) === '<td class="nowrap">N/A</td>', dateCell({ ...base }));
[
  [PAYLOAD, "&lt;img src=x"],
  [SHORT, "&lt;b&gt;x&lt;/b&gt;"],
  ["2026&10<13", "2026&amp;10&lt;13"],
  ['"><svg/x', '"&gt;&lt;svg/x']
].forEach(([raw, want]) => {
  const got = dateCell({ ...base, recall_initiation_date: raw });
  check("2) rowCommon invalid date as text " + JSON.stringify(raw), got === '<td class="nowrap">' + want + "</td>", got);
});
(function () {
  const row = f.rowCommon({ ...base, recall_initiation_date: PAYLOAD }, "");
  check("2) rowCommon output holds no <img element", !/<img\b/i.test(row), row);
  const row2 = f.rowCommon({ ...base, recall_initiation_date: SHORT }, "");
  check("2) rowCommon output holds no <b> element", !/<b>/i.test(row2), row2);
})();

// 3) every date interpolated into a template literal is escaped. The reference
//    detail view builds its HTML inline in submitReference(), so it is checked
//    at the source: any ${fmtDate(...)} or ${isoDate(...)} without esc() fails.
(function () {
  const sites = [...html.matchAll(/\$\{\s*(esc\(\s*)?(fmtDate|isoDate)\(([^)]*)\)/g)];
  check("3) at least two date insertion points found (table + detail)", sites.length >= 2, "found " + sites.length);
  sites.forEach(m => {
    const line = html.slice(0, m.index).split("\n").length;
    check("3) index.html:" + line + " escapes ${" + m[2] + "(" + m[3] + ")}", !!m[1], m[0]);
  });
  check("3) detail view escapes the initiation date",
    /Date Initiated:<\/strong> \$\{esc\(fmtDate\(rec\.recall_initiation_date\)\)\}/.test(html));
})();

if (failed) {
  console.log(failed + " check(s) failed");
  process.exit(1);
}
console.log("all date rendering checks passed");
