# ---- Stage 1: build the Go web server ----
FROM golang:1.26-alpine AS web-builder
WORKDIR /src

COPY go.mod ./
COPY main.go semaphore.go index.html ./

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/server .
COPY index.html /out/

# ---- Stage 2: minimal runtime ----
FROM alpine:latest
WORKDIR /app

RUN apk add --no-cache ca-certificates wget

COPY --from=web-builder /out/server ./server
COPY --from=web-builder /out/index.html ./index.html
COPY bin/drug-enforcement-pp-cli-linux ./drug-enforcement-pp-cli

RUN chmod +x ./server ./drug-enforcement-pp-cli

ENV CLI_BIN=/app/drug-enforcement-pp-cli
ENV PORT=8094

EXPOSE 8094

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8094/healthz || exit 1

CMD ["./server"]