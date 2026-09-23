# ---- Stage 1: build the Go web server ----
FROM golang:1.26-alpine AS web-builder
WORKDIR /src

COPY go.mod ./
COPY main.go semaphore.go index.html ./

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/server .
COPY index.html /out/

# ---- Stage 2: minimal runtime ----
# Pinned to a minor release, not :latest. :latest moves on its own, so a
# rebuild with no code change could ship a different base system. 3.24 is the
# same line pubvera-grantvera runs; patch releases within it still arrive.
FROM alpine:3.24
WORKDIR /app

RUN apk add --no-cache ca-certificates wget

COPY --from=web-builder /out/server ./server
COPY --from=web-builder /out/index.html ./index.html
COPY bin/drug-enforcement-pp-cli-linux ./drug-enforcement-pp-cli

RUN chmod +x ./server ./drug-enforcement-pp-cli

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