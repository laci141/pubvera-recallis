// Loads REAL function and const definitions out of index.html so the node
// tests exercise the shipped code, not a copy of it. Not a test itself: the CI
// step runs tools/*_test.js only.
//
// A definition is found by its 4-space indent inside the first inline <script>
// block: "    function name(" ends at the first line that is exactly "    }",
// and "    const name = ..." is taken as its single line.
"use strict";
const fs = require("fs");
const path = require("path");

const htmlPath = path.join(__dirname, "..", "index.html");
const html = fs.readFileSync(htmlPath, "utf8");

function functionSource(name) {
  const re = new RegExp("\\n    function " + name + "\\(");
  const m = re.exec(html);
  if (!m) throw new Error("function " + name + " not found in index.html");
  const start = m.index + 1;
  const end = /\r?\n    \}\r?\n/.exec(html.slice(start));
  if (!end) throw new Error("end of function " + name + " not found in index.html");
  return html.slice(start, start + end.index + end[0].length);
}

function constSource(name) {
  const re = new RegExp("\\n    const " + name + " = [^\\n]*\\n");
  const m = re.exec(html);
  if (!m) throw new Error("const " + name + " not found in index.html");
  return m[0];
}

// load({ functions: [...], consts: [...], globals: {...} }) evaluates the named
// definitions together and returns them by name. globals are passed in as
// parameters, e.g. a minimal document for esc().
function load(spec) {
  const fns = spec.functions || [];
  const consts = spec.consts || [];
  const globals = spec.globals || {};
  const src = consts.map(constSource).join("") + fns.map(functionSource).join("") +
    "\nreturn {" + fns.concat(consts).join(",") + "};";
  const names = Object.keys(globals);
  return new Function(...names, src)(...names.map(n => globals[n]));
}

// A minimal document for esc(): it sets div.textContent and reads div.innerHTML
// back. The HTML serializer escapes exactly &, <, > and U+00A0 in a text node
// (HTML Living Standard, "escaping a string" in text mode); quotes stay as they
// are, which is why index.html has escAttr() for attribute values.
const textDocument = {
  createElement() {
    let text = "";
    return {
      set textContent(v) { text = String(v); },
      get textContent() { return text; },
      get innerHTML() {
        return text.replace(/&/g, "&amp;").replace(/ /g, "&nbsp;")
          .replace(/</g, "&lt;").replace(/>/g, "&gt;");
      }
    };
  }
};

module.exports = { html, htmlPath, functionSource, load, textDocument };
