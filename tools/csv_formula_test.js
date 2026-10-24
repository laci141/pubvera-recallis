// CSV / XLSX formula-injection test.
//
// Run: node tools/csv_formula_test.js
//
// Loads the REAL toCSV(), neutralizeFormula() and xlsxCell() out of index.html
// (see html_functions.js) and checks what a spreadsheet would actually read
// from each exported cell. Same approach and expectations as
// pubvera-devicera/tools/csv_formula_test.js: a text cell whose first
// non-blank character is = + - or @, or that starts with a tab or CR, gets a
// leading apostrophe; real numbers and strictly numeric strings stay as they are.
"use strict";
const { html, load } = require("./html_functions");

let failed = 0;
function check(name, ok, detail) {
  if (ok) console.log("PASS " + name);
  else { failed++; console.log("FAIL " + name + (detail ? " :: " + detail : "")); }
}

const hasHelper = /\n    function neutralizeFormula\(/.test(html);
check("neutralizeFormula() is defined in index.html", hasHelper);
const { toCSV } = load({ functions: (hasHelper ? ["neutralizeFormula"] : []).concat(["toCSV"]) });

// Minimal RFC 4180 parser.
function parse(text) {
  const rows = []; let row = [], f = "", q = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (q) {
      if (c === '"') { if (text[i + 1] === '"') { f += '"'; i++; } else q = false; }
      else f += c;
    } else if (c === '"') q = true;
    else if (c === ",") { row.push(f); f = ""; }
    else if (c === "\r" && text[i + 1] === "\n") { row.push(f); rows.push(row); row = []; f = ""; i++; }
    else f += c;
  }
  row.push(f); rows.push(row);
  return rows;
}
const stripBOM = s => s.replace(/^﻿/, "");

// The cell value as a spreadsheet sees it: one row, one column.
function firstData(value) {
  const rows = parse(stripBOM(toCSV([{ A: value }])));
  return rows[2][0]; // rows[0] = sep=, rows[1] = header, rows[2] = data
}

// a) formula text is neutralised
[
  "=1+1", "+1+1", "-1+1", "@SUM(A1:A2)",
  " =1+1", "   =1+1", "\t=1+1", " \t=1+1", "\r=1+1", "\n=1+1",
  '=HYPERLINK("http://x","y")', "=cmd|'/c calc'!A1"
].forEach(s => {
  const got = firstData(s);
  check("a) neutralised " + JSON.stringify(s), got === "'" + s, "got " + JSON.stringify(got));
});

// b) a cell that starts with a tab or CR is neutralised even without a formula sign
["\tabc", "\rabc"].forEach(s => {
  const got = firstData(s);
  check("b) leading tab/CR neutralised " + JSON.stringify(s), got === "'" + s, "got " + JSON.stringify(got));
});

// c) the header line goes through the same guard
(function () {
  const term = "=cmd|'/c calc'!A1";
  const rows = parse(stripBOM(toCSV([{ A: "x" }], term)));
  check("c) header line neutralised", rows[1][0] === "'" + term, "got " + JSON.stringify(rows[1][0]));
})();

// d) real numbers and strictly numeric strings stay numeric
[42, -5, 3.14, 0, -0.5].forEach(n => {
  const got = firstData(n);
  check("d) number unchanged " + n, got === String(n), "got " + JSON.stringify(got));
});
["-5", "-0.5", "12", "3.14"].forEach(s => {
  const got = firstData(s);
  check("d) numeric string unchanged " + JSON.stringify(s), got === s, "got " + JSON.stringify(got));
});

// e) ordinary text, including Unicode and inner signs, is unchanged
["Pfizer Inc.", "Class II", "non-aspirin 81 mg", "a=b", "x@y.com", "2026-10-13",
 "Gyógyszer – ÁRVÍZTŰRŐ tükörfúrógép", "アスピリン 💊"].forEach(s => {
  const got = firstData(s);
  check("e) text unchanged " + JSON.stringify(s), got === s, "got " + JSON.stringify(got));
});

// f) null, undefined, empty and booleans
check("f) null -> empty", firstData(null) === "");
check("f) undefined -> empty", firstData(undefined) === "");
check("f) empty string -> empty", firstData("") === "");
check("f) false -> empty", firstData(false) === "");
check("f) true -> Yes", firstData(true) === "Yes");

// g) RFC 4180: quotes, commas and CR/LF are quoted and round-trip
['a"b', "a,b", "a\nb", "a\r\nb", 'He said "=1+1", twice'].forEach(s => {
  const raw = toCSV([{ A: s }]);
  check("g) quoted " + JSON.stringify(s), raw.endsWith('\r\n"' + s.replace(/"/g, '""') + '"'),
    "tail " + JSON.stringify(raw.slice(-24)));
  check("g) round-trip " + JSON.stringify(s), firstData(s) === s, "got " + JSON.stringify(firstData(s)));
});
(function () {
  const s = '=1+1,"x"\r\n';
  check("g) formula plus quoting " + JSON.stringify(s), firstData(s) === "'" + s,
    "got " + JSON.stringify(firstData(s)));
})();

// h) shape: every data row keeps its column count with hostile values
(function () {
  const rows = [
    { A: "=1+1", B: "a,b", C: 'a"b', D: "a\r\nb" },
    { A: "\t=x", B: "-5", C: 3.14, D: null },
    { A: "@SUM(A1:A2)", B: " +1", C: true, D: "" }
  ];
  const p = parse(stripBOM(toCSV(rows, "Recallis export")));
  const body = p.slice(2); // p[0] = sep=, p[1] = title
  check("h) every row has 4 columns", body.length === 4 && body.every(r => r.length === 4),
    JSON.stringify(body.map(r => r.length)));
  check("h) BOM present", toCSV(rows).charCodeAt(0) === 0xFEFF);
})();

// i) the source rows (lastData) are not mutated
(function () {
  const rows = [{ A: "=1+1", B: -5, C: "@x", D: null }];
  const before = JSON.stringify(rows);
  toCSV(rows, "=t");
  check("i) input rows unchanged", JSON.stringify(rows) === before, JSON.stringify(rows));
})();

// j) XLSX: the cell value handed to SheetJS goes through the same guard
const hasXlsxCell = /\n    function xlsxCell\(/.test(html);
check("j) xlsxCell() is defined in index.html", hasXlsxCell);
if (hasXlsxCell && hasHelper) {
  const { xlsxCell } = load({ functions: ["neutralizeFormula", "xlsxCell"] });
  ["=1+1", "+1+1", "-1+1", "@SUM(A1:A2)", " =1+1", "\t=1+1", "\r=1+1"].forEach(s => {
    check("j) xlsx neutralised " + JSON.stringify(s), xlsxCell(s) === "'" + s, "got " + JSON.stringify(xlsxCell(s)));
  });
  check("j) xlsx number stays a number", xlsxCell(42) === 42 && xlsxCell(-0.5) === -0.5);
  check("j) xlsx numeric string unchanged", xlsxCell("-5") === "-5");
  check("j) xlsx ISO date unchanged (date conversion still applies)", xlsxCell("2026-10-13") === "2026-10-13");
  check("j) xlsx text unchanged", xlsxCell("Árvíztűrő Pfizer") === "Árvíztűrő Pfizer");
  check("j) xlsx null/undefined/false -> empty", xlsxCell(null) === "" && xlsxCell(undefined) === "" && xlsxCell(false) === "");
  check("j) xlsx true -> Yes", xlsxCell(true) === "Yes");
  check("j) xlsx object -> JSON", xlsxCell({ a: 1 }) === '{"a":1}');
}

// k) full-width starters are neutralised in CSV and XLSX, also after leading whitespace
const FULLWIDTH = ["\uFF1D1+1", "\uFF0B1+1", "\uFF0D1+1", "\uFF20SUM(A1:A2)", " \uFF1D1+1", "\n\uFF1D1+1"];
FULLWIDTH.forEach(s => {
  const got = firstData(s);
  check("k) csv neutralised " + JSON.stringify(s), got === "'" + s, "got " + JSON.stringify(got));
});
if (hasXlsxCell && hasHelper) {
  const { xlsxCell: xlsxCellFw } = load({ functions: ["neutralizeFormula", "xlsxCell"] });
  FULLWIDTH.forEach(s => {
    check("k) xlsx neutralised " + JSON.stringify(s), xlsxCellFw(s) === "'" + s, "got " + JSON.stringify(xlsxCellFw(s)));
  });
}

// l) full-width characters that are not a leading starter stay unchanged
["a\uFF1Db", "Pfizer \uFF20 x", "\uFF41bc"].forEach(s => {
  const got = firstData(s);
  check("l) unchanged " + JSON.stringify(s), got === s, "got " + JSON.stringify(got));
});

if (failed) {
  console.log(failed + " check(s) failed");
  process.exit(1);
}
console.log("all CSV/XLSX formula checks passed");
