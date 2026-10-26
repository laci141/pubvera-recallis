// CalVer release workflow test.
//
// Run: node tools/release_workflow_test.js
//
// Every non-pull_request run on main gets a version YYYY.MM.DD.N, stamped into
// the binary, an image tag, the OCI label, a git tag and a GitHub Release. This
// static check reads .github/workflows/deploy.yml and the Dockerfile and
// asserts the pieces are wired together.
//
// The workflow is checked line by line, never with a regex that spans lines:
// one such regex hung with catastrophic backtracking in the Retractis pilot.
"use strict";
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "..");
const lines = fs.readFileSync(path.join(root, ".github", "workflows", "deploy.yml"), "utf8").split(/\r?\n/);
const docker = fs.readFileSync(path.join(root, "Dockerfile"), "utf8").split(/\r?\n/);

let failed = 0;
function check(label, ok) {
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}

// The lines of one step, from its "- name:" to the next step or end of file.
// start is the index of the "- name:" line, -1 when the step is missing.
function step(name) {
  const start = lines.findIndex((l) => l.trim() === "- name: " + name);
  if (start < 0) return { start: -1, lines: [] };
  const out = [lines[start]];
  for (let j = start + 1; j < lines.length && !/^\s*- name:/.test(lines[j]); j++) out.push(lines[j]);
  return { start, lines: out };
}
const has = (s, text) => s.lines.some((l) => l.includes(text));
const ifLine = (s) => s.lines.find((l) => /^\s*if:/.test(l)) || "";

// Every non-pull_request event on main (push and workflow_dispatch) is
// versioned: a workflow_dispatch run also pushes :latest, so it must not ship
// as "dev".
const NONPR_MAIN = /github\.event_name != 'pull_request' && github\.ref == 'refs\/heads\/main'/;
const nonPRMain = (cond) => cond !== "" && NONPR_MAIN.test(cond) && !/event_name\s*==\s*'push'/.test(cond);

const ver = step("Compute version");
check("version step exists", ver.start >= 0);
check("version step if: is non-PR main and not event_name == 'push'", nonPRMain(ifLine(ver)));
check("version step uses date -u +%Y.%m.%d", has(ver, "date -u +%Y.%m.%d"));
check("version step uses git ls-remote --tags", has(ver, "git ls-remote --tags"));
check("version step writes GITHUB_OUTPUT", has(ver, "GITHUB_OUTPUT"));

const push = step("Build and push");
check("Build and push step exists", push.start >= 0);
const argIdx = push.lines.findIndex((l) => /^\s*build-args:/.test(l));
check("pushing build has build-args with APP_VERSION",
  argIdx >= 0 && push.lines.slice(argIdx).some((l) => l.includes("APP_VERSION=")));
const tagLine = push.lines.find((l) => l.includes("steps.version.outputs.version") && l.includes("format(")) || "";
check("version image tag only on non-PR main and not event_name == 'push'", nonPRMain(tagLine));

const label = step("Build image for the label check");
check("load: true label-check build still exists", has(label, "load: true"));
check("label-check build passes APP_VERSION build-arg", has(label, "APP_VERSION="));
check("label-check build carries no version image tag", !has(label, "format("));
check("label-check build comes before Build and push", label.start >= 0 && label.start < push.start);

const rel = step("Create GitHub release");
check("release step exists", rel.start >= 0);
check("release step if: is non-PR main and not event_name == 'push'", nonPRMain(ifLine(rel)));
check("release step runs gh release create --target",
  rel.lines.some((l) => l.includes("gh release create") && l.includes("--target")));
check("release step comes after Build and push", push.start >= 0 && rel.start > push.start);

const perm = lines.findIndex((l) => l.trim() === "permissions:");
check("job has contents: write", perm >= 0 && (lines[perm + 1] || "").trim() === "contents: write");
check("job keeps packages: write", perm >= 0 && lines.slice(perm + 1, perm + 3).some((l) => l.trim() === "packages: write"));
const conc = lines.findIndex((l) => l.trim() === "concurrency:");
check("concurrency group exists", conc >= 0 && (lines[conc + 1] || "").trim().startsWith("group:"));
check("cancel-in-progress is not true", !lines.some((l) => /^\s*cancel-in-progress:\s*true/.test(l)));

check("Dockerfile has ARG APP_VERSION=dev twice", docker.filter((l) => l.trim() === "ARG APP_VERSION=dev").length === 2);
check("Dockerfile ldflags set -X main.version=", docker.some((l) => l.includes("-X main.version=")));
check("Dockerfile has org.opencontainers.image.version", docker.some((l) => l.includes("org.opencontainers.image.version")));

if (failed) {
  console.error(failed + " check(s) failed");
  process.exit(1);
}
console.log("all release workflow checks pass");
