// Publish-gate test for .github/workflows/deploy.yml.
//
// Run: node tools/publish_gate_test.js
//
// Watchtower deploys whatever :latest points at, so only main may publish.
// This reads the REAL conditions that decide a publish (the publish job's if:,
// which gates the GHCR login and every push, and the Compute version step's
// if:, whose output the publish job refuses to run without) and evaluates them
// for each way the workflow can start. The evaluator understands only what
// these conditions need: github.event_name, github.ref, string literals,
// == != && || ! and parentheses. Anything else fails the test instead of
// being guessed at.
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

// The lines of one job, from its "  <name>:" line to the next job or the next
// top-level key. Comments and blank lines do not end it.
function job(name) {
  const lines = yml.split(/\r?\n/);
  const i = lines.findIndex(l => l === "  " + name + ":");
  if (i < 0) return null;
  const out = [lines[i]];
  for (let j = i + 1; j < lines.length; j++) {
    const l = lines[j];
    if (l.trim() !== "" && !/^\s*#/.test(l) && /^ {0,2}\S/.test(l)) break;
    out.push(l);
  }
  return out.join("\n");
}
const jobIf = jobText => { const m = /^    if:\s*(.+?)\s*$/m.exec(jobText || ""); return m ? m[1] : null; };

const publishJob = job("publish");
const buildJob = job("build");
const version = step("Compute version");
const login = step("Log in to GHCR");
check("publish job exists", !!publishJob);
check("build job exists", !!buildJob);
check("Compute version step exists", !!version);
const publishIf = jobIf(publishJob);
const versionIf = field(version, "if");
check("publish job has an if: condition", !!publishIf, String(publishIf));
check("Compute version has an if: condition", !!versionIf, String(versionIf));

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

// The publish job refuses an empty VERSION, so the version step must run on
// exactly the runs that publish: the same answer in every scenario.
for (const [label, expr] of [["publish job", publishIf], ["Compute version", versionIf]]) {
  if (!expr) continue;
  for (const s of scenarios) {
    let got;
    try { got = evaluate(expr, s.ctx); } catch (e) { check(label + ": " + s.name, false, e.message); continue; }
    check(label + ": " + s.name + " -> " + (s.want ? "publishes" : "does not publish"), got === s.want,
      "condition " + JSON.stringify(expr) + " evaluated to " + got);
  }
}

// The GHCR login is gated by the publish job's if:, so it lives in that job
// and nowhere else.
check("GHCR login step exists", !!login);
check("GHCR login is in the publish job", !!login && !!publishJob && publishJob.includes(login));
check("only one step logs in to a registry", (yml.match(/docker\/login-action@/g) || []).length === 1);

// The build job never pushes: both of its image builds have push: false, and
// every docker push is in the publish job.
const labelBuild = step("Build image for the label check");
const exportBuild = step("Build and export");
check("label-check build has push: false", field(labelBuild, "push") === "false", String(field(labelBuild, "push")));
check("Build and export has push: false", field(exportBuild, "push") === "false", String(field(exportBuild, "push")));
const code = yml.split(/\r?\n/).filter(l => !/^\s*#/.test(l));
const pushLines = code.filter(l => /\bdocker push\b/.test(l)).map(l => l.trim());
check("every docker push is in the publish job (" + pushLines.length + " lines)",
  pushLines.length > 0 && !!publishJob && pushLines.every(l => publishJob.includes(l)) && !(buildJob || "").includes("docker push"),
  JSON.stringify(pushLines));
const latestPush = pushLines.filter(l => /:latest\b/.test(l));
check(":latest is pushed only by the Load and push step",
  latestPush.length === 1 && !!step("Load and push") && step("Load and push").includes(latestPush[0]), JSON.stringify(latestPush));

if (failed) {
  console.log(failed + " check(s) failed");
  process.exit(1);
}
console.log("all publish-gate checks passed");
