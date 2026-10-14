// Publish-gate test for .github/workflows/deploy.yml.
//
// Run: node tools/publish_gate_test.js
//
// Watchtower deploys whatever :latest points at, so only main may publish.
// This reads the REAL conditions on the two publishing steps (the GHCR login's
// if: and the build-and-push step's push:) and evaluates them for each way the
// workflow can start. The evaluator understands only what these conditions
// need: github.event_name, github.ref, string literals, == != && || ! and
// parentheses. Anything else fails the test instead of being guessed at.
// String comparison ignores case, as GitHub's expression syntax does.
"use strict";
const fs = require("fs");
const path = require("path");

const yml = fs.readFileSync(path.join(__dirname, "..", ".github", "workflows", "deploy.yml"), "utf8");

let failed = 0;
function check(name, ok, detail) {
  if (ok) console.log("PASS " + name);
  else { failed++; console.log("FAIL " + name + (detail ? " :: " + detail : "")); }
}

// The lines of one step, from its "- name:" to the next step or end of file.
function step(name) {
  const lines = yml.split(/\r?\n/);
  const i = lines.findIndex(l => l.trim() === "- name: " + name);
  if (i < 0) return null;
  const out = [lines[i]];
  for (let j = i + 1; j < lines.length && !/^\s*- name:/.test(lines[j]); j++) out.push(lines[j]);
  return out.join("\n");
}

function field(stepText, key) {
  const m = new RegExp("^\\s*" + key + ":\\s*(.+?)\\s*$", "m").exec(stepText || "");
  // A trailing " # comment" is not part of the value, as in YAML itself. The
  // conditions here never contain '#' inside a quoted string.
  return m ? m[1].replace(/\s+#[^']*$/, "") : null;
}

function unwrap(expr) {
  const m = /^\$\{\{([\s\S]*)\}\}$/.exec(expr.trim());
  return (m ? m[1] : expr).trim();
}

// Tokenise and evaluate one expression against { event_name, ref }.
function evaluate(expr, ctx) {
  const toks = [];
  const re = /\s*(github\.event_name|github\.ref|'(?:[^']|'')*'|==|!=|&&|\|\||!|\(|\)|true|false)/y;
  let src = unwrap(expr), pos = 0;
  while (pos < src.length) {
    if (/^\s*$/.test(src.slice(pos))) break;
    re.lastIndex = pos;
    const m = re.exec(src);
    if (!m) throw new Error("unsupported expression at: " + src.slice(pos));
    toks.push(m[1]);
    pos = re.lastIndex;
  }
  let i = 0;
  const peek = () => toks[i], next = () => toks[i++];
  function primary() {
    const t = next();
    if (t === "(") { const v = or(); if (next() !== ")") throw new Error("missing )"); return v; }
    if (t === "!") return !truthy(primary());
    if (t === "github.event_name") return ctx.event_name;
    if (t === "github.ref") return ctx.ref;
    if (t === "true") return true;
    if (t === "false") return false;
    if (t && t[0] === "'") return t.slice(1, -1).replace(/''/g, "'");
    throw new Error("unexpected token " + t);
  }
  function cmp() {
    let l = primary();
    while (peek() === "==" || peek() === "!=") {
      const op = next(), r = primary();
      const eq = String(l).toLowerCase() === String(r).toLowerCase();
      l = op === "==" ? eq : !eq;
    }
    return l;
  }
  function and() { let l = cmp(); while (peek() === "&&") { next(); const r = cmp(); l = truthy(l) ? r : l; } return l; }
  function or() { let l = and(); while (peek() === "||") { next(); const r = and(); l = truthy(l) ? l : r; } return l; }
  const truthy = v => !(v === false || v === "" || v === 0 || v == null);
  const v = or();
  if (i !== toks.length) throw new Error("trailing tokens: " + toks.slice(i).join(" "));
  return truthy(v);
}

const login = step("Log in to GHCR");
const publish = step("Build and push");
check("GHCR login step exists", !!login);
check("build-and-push step exists", !!publish);
const loginIf = field(login, "if");
const pushExpr = field(publish, "push");
check("GHCR login has an if: condition", !!loginIf, String(loginIf));
check("build-and-push has a push: expression", !!pushExpr, String(pushExpr));

const scenarios = [
  { name: "push to main", ctx: { event_name: "push", ref: "refs/heads/main" }, want: true },
  { name: "workflow_dispatch from main", ctx: { event_name: "workflow_dispatch", ref: "refs/heads/main" }, want: true },
  { name: "workflow_dispatch from feature branch", ctx: { event_name: "workflow_dispatch", ref: "refs/heads/feature-x" }, want: false },
  { name: "workflow_dispatch from fix/main-like branch", ctx: { event_name: "workflow_dispatch", ref: "refs/heads/fix/main" }, want: false },
  { name: "workflow_dispatch from tag", ctx: { event_name: "workflow_dispatch", ref: "refs/tags/v1" }, want: false },
  { name: "pull_request to main", ctx: { event_name: "pull_request", ref: "refs/pull/7/merge" }, want: false },
  // Not a ref GitHub sends for a PR; it proves the PR ban does not lean on the ref.
  { name: "pull_request even with ref main", ctx: { event_name: "pull_request", ref: "refs/heads/main" }, want: false },
  { name: "push to non-main branch", ctx: { event_name: "push", ref: "refs/heads/feature-x" }, want: false }
];

for (const [label, expr] of [["GHCR login", loginIf], ["build-and-push push", pushExpr]]) {
  if (!expr) continue;
  for (const s of scenarios) {
    let got;
    try { got = evaluate(expr, s.ctx); } catch (e) { check(label + ": " + s.name, false, e.message); continue; }
    check(label + ": " + s.name + " -> " + (s.want ? "publishes" : "does not publish"), got === s.want,
      "condition " + JSON.stringify(expr) + " evaluated to " + got);
  }
}

// :latest is only ever written by the gated step; the label-check build never pushes.
const labelBuild = step("Build image for the label check");
check("label-check build has push: false", field(labelBuild, "push") === "false", String(field(labelBuild, "push")));
const code = yml.split(/\r?\n/).filter(l => !/^\s*#/.test(l)).join("\n");
const latestSteps = code.split(/\n\s*- name: /).filter(s => /:latest\b/.test(s)).map(s => s.split(/\r?\n/)[0]);
check(":latest is tagged only by the build-and-push step",
  latestSteps.length === 1 && /Build and push/.test(latestSteps[0]), JSON.stringify(latestSteps));
check("only one step logs in to a registry", (yml.match(/docker\/login-action@/g) || []).length === 1);

if (failed) {
  console.log(failed + " check(s) failed");
  process.exit(1);
}
console.log("all publish-gate checks passed");
