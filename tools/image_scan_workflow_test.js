// Image vulnerability scan workflow test (Recallis).
//
// Run: node tools/image_scan_workflow_test.js [deploy.yml] [image-scan.yml]
//
// Only one Pubvera app used to scan its image in CI; a server grype run later
// found High CVEs on live images that no CI had caught. This static check
// asserts:
//   (a) deploy.yml has exactly one anchore/scan-action step, named "Scan the
//       image", pinned to a 40-hex SHA, scanning the label-check image before
//       "Build and export", failing only on high/critical findings that have
//       a fix;
//   (b) image-scan.yml scans the live :latest image on a schedule and on
//       workflow_dispatch only, with every action pinned to a 40-hex SHA and
//       the same scan-action SHA as deploy.yml.
// The source avoids backslashes: CR and LF come from String.fromCharCode.
"use strict";
const fs = require("fs");
const path = require("path");

const CR = String.fromCharCode(13);
const LF = String.fromCharCode(10);
const root = path.join(__dirname, "..");
const deployPath = process.argv[2] || path.join(root, ".github", "workflows", "deploy.yml");
const scanPath = process.argv[3] || path.join(root, ".github", "workflows", "image-scan.yml");

const SCAN_SHA = "27805bf3b4e84b4a5c980df22ed233c00390a439";
const SCAN_USES = "uses: anchore/scan-action@" + SCAN_SHA + " # v7.4.2";
const LABEL_IMAGE = "recallis-label-check:ci";
const LIVE_IMAGE = "ghcr.io/laci141/pubvera-recallis:latest";

let failed = 0;
function check(label, ok) {
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}

function readLines(p) {
  if (!fs.existsSync(p)) return null;
  return fs.readFileSync(p, "utf8").split(CR).join("").split(LF);
}

function isHex40(s) {
  if (s.length !== 40) return false;
  const hex = "0123456789abcdef";
  for (const c of s) {
    if (!hex.includes(c)) return false;
  }
  return true;
}

function indexOfTrim(lines, text) {
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].trim() === text) return i;
  }
  return -1;
}

// Lines of the step that starts at index i (a "- name:" line).
function stepBody(lines, i) {
  const out = [];
  const indent = lines[i].length - lines[i].trimStart().length;
  out.push(lines[i].trim());
  for (let j = i + 1; j < lines.length; j++) {
    const l = lines[j];
    if (l.trim() === "") continue;
    const ind = l.length - l.trimStart().length;
    if (ind <= indent) break;
    out.push(l.trim());
  }
  return out;
}

function checkScanSettings(prefix, body, image) {
  check(prefix + " scans image " + image, body.includes("image: " + image));
  check(prefix + " fail-build: true", body.includes("fail-build: true"));
  check(prefix + " severity-cutoff: high", body.includes("severity-cutoff: high"));
  check(prefix + " only-fixed: true", body.includes("only-fixed: true"));
}

// ---- deploy.yml
const deploy = readLines(deployPath);
check("deploy.yml exists", deploy !== null);
if (deploy) {
  const scanLines = [];
  deploy.forEach((l, i) => {
    if (l.includes("anchore/scan-action")) scanLines.push(i);
  });
  check("deploy.yml has exactly one anchore/scan-action line (found " + scanLines.length + ")", scanLines.length === 1);
  if (scanLines.length === 1) {
    const at = scanLines[0];
    check("deploy.yml scan-action pinned to " + SCAN_SHA, deploy[at].trim() === SCAN_USES);
    const exp = indexOfTrim(deploy, "- name: Build and export");
    check("deploy.yml scan step before Build and export", exp >= 0 && at < exp);
    const nameAt = at - 1;
    const isStep = nameAt >= 0 && deploy[nameAt].trim() === "- name: Scan the image";
    check("deploy.yml step is named Scan the image", isStep);
    if (isStep) checkScanSettings("deploy.yml", stepBody(deploy, nameAt), LABEL_IMAGE);
  }
}

// ---- image-scan.yml
const scan = readLines(scanPath);
check("image-scan.yml exists", scan !== null);
if (scan) {
  const trimmed = scan.map((l) => l.trim());
  check("image-scan.yml has cron 17 3 1,15 * *", trimmed.includes('- cron: "17 3 1,15 * *"'));
  check("image-scan.yml has workflow_dispatch", trimmed.includes("workflow_dispatch:"));
  check("image-scan.yml has no pull_request", !scan.some((l) => l.includes("pull_request")));
  check("image-scan.yml has no push: trigger", !trimmed.includes("push:"));
  check("image-scan.yml has packages: read", trimmed.includes("packages: read"));
  const uses = trimmed.filter((l) => l.startsWith("uses:") || l.startsWith("- uses:"));
  check("image-scan.yml has exactly 2 uses: lines (found " + uses.length + ")", uses.length === 2);
  for (const u of uses) {
    const at = u.split("@")[1] || "";
    const sha = at.split(" ")[0];
    check("image-scan.yml pinned to 40-hex SHA with version comment: " + u, isHex40(sha) && at.includes(" # v"));
  }
  const scanAt = scan.findIndex((l) => l.includes("anchore/scan-action"));
  check("image-scan.yml uses anchore/scan-action", scanAt >= 0);
  if (scanAt >= 0) {
    check("image-scan.yml scan-action SHA equals deploy.yml's", scan[scanAt].trim() === SCAN_USES);
    let start = scanAt;
    if (!scan[scanAt].trim().startsWith("- ") && scanAt > 0) start = scanAt - 1;
    checkScanSettings("image-scan.yml", stepBody(scan, start), LIVE_IMAGE);
  }
  check("image-scan.yml pulls " + LIVE_IMAGE, trimmed.includes("run: docker pull " + LIVE_IMAGE));
}

if (failed) {
  console.error(failed + " check(s) failed");
  process.exit(1);
}
console.log("all image scan workflow checks pass");
