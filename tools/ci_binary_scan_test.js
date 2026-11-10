// CI binary scan / SBOM / image scan test (RV-SEC-05).
//
// Run: node tools/ci_binary_scan_test.js
//
// The source-mode govulncheck covers this module only. The image also ships
// the drug-enforcement CLI, built from a pinned upstream commit, and the base
// system packages. This static check reads .github/workflows/deploy.yml and
// asserts that the build job, before the publish job:
//   (a) copies both binaries out of recallis-label-check:ci;
//   (b) scans the CLI and the server with govulncheck -mode=binary as two
//       SEPARATE steps, each reusing GOVULNCHECK_VERSION;
//   (c) checks the CLI's `go version -m` main module version against the
//       first 12 chars of PP_LIBRARY_COMMIT;
//   (d) makes an SPDX SBOM of the image and uploads it;
//   (e) scans the image, failing on fixed high+ findings;
//   (f) runs all of these after the label/non-root checks and before
//       "Build and export";
//   (g) keeps the build job free of write permissions.
"use strict";
const fs = require("fs");
const path = require("path");

const yml = fs.readFileSync(path.join(__dirname, "..", ".github", "workflows", "deploy.yml"), "utf8").replace(/\r/g, "");
const lines = yml.split("\n");

let failed = 0;
function check(label, ok) {
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}

const jobStart = (name) => lines.findIndex((l) => l === "  " + name + ":");
const buildStart = jobStart("build");
const publishStart = jobStart("publish");
if (buildStart < 0 || publishStart < 0) {
  console.error("FAIL: build or publish job not found - refusing to pass silently");
  process.exit(1);
}
const buildLines = lines.slice(buildStart, publishStart);

// Steps of the build job: { name, start, text }.
const steps = [];
buildLines.forEach((l, i) => {
  const m = /^      - name:\s*(.+)$/.exec(l);
  if (m) steps.push({ name: m[1].trim(), start: buildStart + i });
});
steps.forEach((s, i) => {
  const end = i + 1 < steps.length ? steps[i + 1].start : publishStart;
  s.text = lines.slice(s.start, end).join("\n");
});
const idx = (pred) => steps.findIndex(pred);

const iLabel = idx((s) => s.name === "Check the CLI commit label");
const iNonRoot = idx((s) => s.name === "Check the image runs as non-root");
const iExport = idx((s) => s.name === "Build and export");
check("anchor steps present (label, non-root, export)", iLabel >= 0 && iNonRoot >= 0 && iExport >= 0);
const between = (i) => i > Math.max(iLabel, iNonRoot) && i < iExport;

const iExtract = idx((s) => /docker create\b/.test(s.text) && /recallis-label-check:ci/.test(s.text) && /docker cp\b/.test(s.text));
const ex = iExtract >= 0 ? steps[iExtract].text : "";
check("(a) a step docker creates recallis-label-check:ci and docker cps", iExtract >= 0);
check("(a) copies /app/drug-enforcement-pp-cli", ex.includes(":/app/drug-enforcement-pp-cli"));
check("(a) copies /app/server", ex.includes(":/app/server"));
check("(f) extract step between checks and export", between(iExtract));

const scan = (bin) => idx((s) => /govulncheck\b[^\n]*-mode[= ]binary/.test(s.text) && s.text.includes(bin));
const iCli = scan("drug-enforcement-pp-cli");
const iSrv = idx((s) => /govulncheck\b[^\n]*-mode[= ]binary/.test(s.text) && /\bserver\b/.test(s.text) && !s.text.includes("drug-enforcement-pp-cli"));
check("(b) govulncheck -mode=binary step for the CLI", iCli >= 0);
check("(b) govulncheck -mode=binary step for the server", iSrv >= 0);
check("(b) CLI and server scans are SEPARATE steps", iCli >= 0 && iSrv >= 0 && iCli !== iSrv);
check("(b) separate step names", iCli >= 0 && iSrv >= 0 && steps[iCli].name !== steps[iSrv].name);
for (const [n, i] of [["CLI", iCli], ["server", iSrv]]) {
  const t = i >= 0 ? steps[i].text : "";
  check("(b) " + n + " scan reuses ${GOVULNCHECK_VERSION}", /govulncheck@\$\{?GOVULNCHECK_VERSION\}?/.test(t) || (/GOVULNCHECK_VERSION/.test(t)));
  check("(b) " + n + " scan does not use @latest", !/govulncheck@latest/.test(t));
  check("(f) " + n + " scan between checks and export", between(i));
}
check("GOVULNCHECK_VERSION defined once, pinned", (yml.match(/^\s*GOVULNCHECK_VERSION:\s*v\d+\.\d+\.\d+\s*$/gm) || []).length === 1);

const iIdent = idx((s) => /go version -m\b/.test(s.text) && s.text.includes("drug-enforcement-pp-cli"));
const id = iIdent >= 0 ? steps[iIdent].text : "";
check("(c) go version -m identity step on the CLI", iIdent >= 0);
check("(c) reads PP_LIBRARY_COMMIT from the Dockerfile", /PP_LIBRARY_COMMIT/.test(id) && /Dockerfile/.test(id));
check("(c) compares a 12-char prefix", /0:12\b|cut -c1-12|head -c ?12/.test(id));
check("(c) reads the main module line (mod)", /\bmod\b/.test(id));
check("(c) fails on mismatch (exit 1)", /exit 1/.test(id));
check("(f) identity step between checks and export", between(iIdent));

const iSbom = idx((s) => /uses:\s+anchore\/sbom-action@/.test(s.text));
const sb = iSbom >= 0 ? steps[iSbom].text : "";
check("(d) anchore/sbom-action step", iSbom >= 0);
check("(d) sbom image: recallis-label-check:ci", /image:\s*recallis-label-check:ci/.test(sb));
check("(d) sbom format: spdx-json", /format:\s*spdx-json/.test(sb));
check("(d) sbom upload-artifact: false", /upload-artifact:\s*false/.test(sb));
check("(d) sbom upload-release-assets: false", /upload-release-assets:\s*false/.test(sb));
check("(f) sbom between checks and export", between(iSbom));
const uploadSha = (/uses:\s+actions\/upload-artifact@([0-9a-f]{40})/.exec(yml) || [])[1];
const iSbomUp = idx((s) => /uses:\s+actions\/upload-artifact@/.test(s.text) && /sbom/i.test(s.text) && !/recallis-image\.tar/.test(s.text));
check("(d) SBOM uploaded with actions/upload-artifact", iSbomUp >= 0 && iSbomUp > iSbom);
check("(d) SBOM upload uses the existing pinned upload-artifact SHA",
  !!uploadSha && (yml.match(/uses:\s+actions\/upload-artifact@[0-9a-f]{40}/g) || []).every((u) => u.endsWith(uploadSha)));

const iScan = idx((s) => /uses:\s+anchore\/scan-action@/.test(s.text));
const sc = iScan >= 0 ? steps[iScan].text : "";
check("(e) anchore/scan-action step", iScan >= 0);
check("(e) scan image: recallis-label-check:ci", /image:\s*recallis-label-check:ci/.test(sc));
check("(e) fail-build: true", /fail-build:\s*true/.test(sc));
check("(e) severity-cutoff: high", /severity-cutoff:\s*high/.test(sc));
check("(e) only-fixed: true", /only-fixed:\s*true/.test(sc));
check("(f) image scan between checks and export", between(iScan));

for (const a of ["anchore/sbom-action", "anchore/scan-action"]) {
  const l = lines.find((x) => x.includes("uses: " + a + "@")) || "";
  check("pinned " + a + " @<40 hex> # vX.Y.Z", /@[0-9a-f]{40} # v\d+\.\d+\.\d+$/.test(l.trim()));
}

const writes = buildLines.filter((l) => /:\s*write\s*$/.test(l));
check("(g) build job has no write permissions (" + JSON.stringify(writes) + ")", writes.length === 0);
check("(g) build job keeps contents: read", buildLines.some((l) => /^      contents:\s*read\s*$/.test(l)));

if (failed) {
  console.error(failed + " check(s) failed");
  process.exit(1);
}
console.log("all CI binary scan checks pass");
