// Runtime apk upgrade-by-name test (zlib CVE-2026-4738, OpenSSL 3.5.9-r1 CVEs).
//
// Run: node tools/dockerfile_apk_upgrade_test.js [path/to/Dockerfile]
//
// Grype --only-fixed flagged libcrypto3/libssl3 3.5.9-r0 (fixed in 3.5.9-r1)
// and zlib in the runtime image. Packages are upgraded by name, never with a
// bare apk upgrade. This static check reads the Dockerfile (comment lines
// skipped) and asserts:
//   (a) exactly one line contains "apk upgrade";
//   (b) that line contains --no-cache, zlib, libcrypto3 and libssl3;
//   (c) no line ends in "apk upgrade" (bare upgrade, no package names);
//   (d) no "apk upgrade" appears without --no-cache.
"use strict";
const fs = require("fs");
const path = require("path");

const file = process.argv[2] || path.join(__dirname, "..", "Dockerfile");
const src = fs.readFileSync(file, "utf8").split("\r").join("");
const lines = src.split("\n").filter((l) => !l.trim().startsWith("#"));

let failed = 0;
function check(label, ok) {
  if (!ok) failed++;
  console.log((ok ? "PASS" : "FAIL") + "  " + label);
}

if (!lines.some((l) => l.trim().startsWith("FROM "))) {
  console.error("FAIL: no FROM line in " + file + " - refusing to pass silently");
  process.exit(1);
}

const upg = lines.filter((l) => l.includes("apk upgrade"));
check("(a) exactly one line contains apk upgrade (found " + upg.length + ")", upg.length === 1);

const line = upg[0] || "";
const words = line.split(" ").filter((w) => w !== "");
for (const tok of ["--no-cache", "zlib", "libcrypto3", "libssl3"]) {
  check("(b) apk upgrade line contains " + tok, words.includes(tok));
}

const bare = upg.filter((l) =>
  l.split("&&").join(";").split(";").some((seg) => {
    const w = seg.trim().split(" ").filter((x) => x !== "");
    return w.length >= 2 && w[w.length - 2] === "apk" && w[w.length - 1] === "upgrade";
  })
);
check("(c) no bare apk upgrade as last words (found " + bare.length + ")", bare.length === 0);

const noCache = upg.filter((l) =>
  l.split("apk upgrade").slice(1).some((rest) => !rest.split("&&")[0].split(";")[0].includes("--no-cache"))
);
check("(d) no apk upgrade without --no-cache (found " + noCache.length + ")", noCache.length === 0);

if (failed) {
  console.error(failed + " check(s) failed");
  process.exit(1);
}
console.log("all apk upgrade checks pass");
