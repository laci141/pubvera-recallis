#!/usr/bin/env bash
# vendor-cli.sh — cross-compile the drug-enforcement CLI to
# bin/drug-enforcement-pp-cli-linux (linux/amd64), which the Docker image
# copies and runs.
#
# The default source is the monorepo under ~/printing-press-library, NOT the
# Desktop copy: there are two clones on this machine and they sit on different
# branches. Vendoring from a feature branch ships whatever that branch happens
# to hold, so check out main (or pass the path explicitly) before running. The
# script prints the branch it is building from — read that line every time.
#
# The default source path is specific to one workstation. That is acceptable
# because the check below refuses to run against a path that does not hold the
# CLI, so a wrong machine gets an error naming the path it tried, not a silent
# build from the wrong source. PP_LIBRARY_ROOT exists so a different machine can
# be configured once for every pubvera repo instead of editing each script.
#
# Resolution order: explicit argument, then PP_LIBRARY_ROOT, then the default.
#
# The cmd/ directory holds two binaries — the CLI and an MCP server. Only the
# CLI is built here; the Dockerfile copies only that one.
#
# USAGE (from the recallis repo, Git Bash):
#   ./vendor-cli.sh
#   ./vendor-cli.sh "/c/Users/LACI/printing-press-library/library/health/drug-enforcement"
#   PP_LIBRARY_ROOT="/path/to/printing-press-library" ./vendor-cli.sh
set -euo pipefail
PP_ROOT="${PP_LIBRARY_ROOT:-/c/Users/LACI/printing-press-library}"
CLI_SRC="${1:-$PP_ROOT/library/health/drug-enforcement}"
OUT="bin/drug-enforcement-pp-cli-linux"
if [ ! -f "$CLI_SRC/go.mod" ] || [ ! -d "$CLI_SRC/cmd" ]; then
  echo "ERROR: CLI source not found at: $CLI_SRC" >&2
  echo "" >&2
  echo "Expected a directory holding go.mod, cmd/ and internal/. Either:" >&2
  echo "  - pass the path:   ./vendor-cli.sh \"/path/to/library/health/drug-enforcement\"" >&2
  echo "  - or set the root: PP_LIBRARY_ROOT=\"/path/to/printing-press-library\"" >&2
  exit 1
fi
echo "Vendoring from: $CLI_SRC"
( cd "$CLI_SRC" && git rev-parse --abbrev-ref HEAD && git log --oneline -1 -- . )
rm -rf cli-src && mkdir -p cli-src
# go.sum only exists when the CLI has external dependencies.
cp "$CLI_SRC/go.mod" cli-src/
[ -f "$CLI_SRC/go.sum" ] && cp "$CLI_SRC/go.sum" cli-src/ || true
cp -r "$CLI_SRC/cmd" "$CLI_SRC/internal" cli-src/
echo "Cross-compiling -> $OUT"
mkdir -p bin
( cd cli-src && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "../$OUT" ./cmd/drug-enforcement-pp-cli )
# `file` is not present in every Git Bash; a missing one must not kill the
# script under set -e after a successful build.
command -v file >/dev/null && file "$OUT" || true
ls -la "$OUT"