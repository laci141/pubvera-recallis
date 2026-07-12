# ---- Stage 1: build the Go web server ----
FROM golang:1.26-alpine AS web-builder
WORKDIR /src

# Only go.mod (no external deps → no go.sum needed)
COPY go.mod ./
COPY main.go ./
COPY index.html ./

# Static build so it runs on a bare alpine runtime
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./main.go
# The homepage is served via http.ServeFile("index.html") — it MUST ship in the image
COPY index.html /out/

# ---- Stage 2: minimal runtime ----
FROM alpine:latest
WORKDIR /app

# CLI shells out over HTTPS to openFDA → needs root certs
RUN apk add --no-cache ca-certificates wget

COPY --from=web-builder /out/server ./server
COPY --from=web-builder /out/index.html ./index.html
# Prebuilt Linux CLI binary (shelled out to by the server)
COPY bin/drug-enforcement-pp-cli-linux ./drug-enforcement-pp-cli

RUN chmod +x ./server ./drug-enforcement-pp-cli

# Tell the server where the CLI lives
ENV CLI_BIN=/app/drug-enforcement-pp-cli
ENV PORT=8094

EXPOSE 8094

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8094/healthz || exit 1

CMD ["./server"]
