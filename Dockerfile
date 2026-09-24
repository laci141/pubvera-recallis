# ---- Stage 1: build the drug-enforcement CLI from upstream source ----
# The CLI used to be cross-compiled on a workstation by vendor-cli.sh and
# committed as bin/drug-enforcement-pp-cli-linux. Nothing in the image said
# which upstream source that binary came from, and an upstream merge reached
# production only if someone remembered to re-vendor.
#
# Now the image builds it from one pinned upstream commit. Bumping the CLI is
# a one-line change to PP_LIBRARY_COMMIT, and the commit is stamped on the
# image as a label. Measured before switching (2026-09-23): the binary built
# this way reports the same version (2026.9.4) and returns byte-identical
# output to the vendored one for the same query.
#
# The module path matches the GitHub path, so go install resolves the
# subdirectory module straight from the commit; no clone of the monorepo.
# This layer only rebuilds when the commit changes, so the ~2 minutes it
# takes are paid on a CLI bump, not on every web change.
#
# PP_LIBRARY_COMMIT is declared before the first FROM so it is global.
# An ARG declared after a FROM exists only in that stage; each stage that
# needs the value re-declares it with a bare ARG and inherits this default.
# The earlier layout set the default inside cli-builder only, so the runtime
# stage saw an empty ARG and the label shipped as "" (measured on e67ee7c).
ARG PP_LIBRARY_COMMIT=58edea349ce3df8a301d4d8950119487c32604b8

FROM golang:1.26-alpine AS cli-builder
ARG PP_LIBRARY_COMMIT
RUN CGO_ENABLED=0 go install -trimpath \
    github.com/mvanhorn/printing-press-library/library/health/drug-enforcement/cmd/drug-enforcement-pp-cli@${PP_LIBRARY_COMMIT}

# ---- Stage 2: build the Go web server ----
FROM golang:1.26-alpine AS web-builder
WORKDIR /src

COPY go.mod ./
COPY main.go semaphore.go index.html ./

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/server .
COPY index.html /out/

# ---- Stage 3: minimal runtime ----
# Pinned to a minor release, not :latest. :latest moves on its own, so a
# rebuild with no code change could ship a different base system. 3.24 is the
# same line pubvera-grantvera runs; patch releases within it still arrive.
FROM alpine:3.24
WORKDIR /app

RUN apk add --no-cache ca-certificates wget

COPY --from=web-builder /out/server ./server
COPY --from=web-builder /out/index.html ./index.html
COPY --from=cli-builder /go/bin/drug-enforcement-pp-cli ./drug-enforcement-pp-cli

RUN chmod +x ./server ./drug-enforcement-pp-cli

# The upstream commit the CLI was built from, readable with docker inspect.
ARG PP_LIBRARY_COMMIT
LABEL org.pubvera.cli.commit=${PP_LIBRARY_COMMIT}

ENV CLI_BIN=/app/drug-enforcement-pp-cli
ENV PORT=8094

EXPOSE 8094

# The probe reads PORT instead of a hard-coded 8094. The server listens on
# PORT, so a probe with its own copy of the number would report a healthy
# server as down the moment the two disagreed. CMD in this form runs through
# a shell, which is what expands ${PORT}; the :-8094 fallback keeps the probe
# working if PORT is ever unset.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget -qO- "http://localhost:${PORT:-8094}/healthz" || exit 1

CMD ["./server"]