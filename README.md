# Recallis

Search FDA drug recalls and enforcement reports by drug, firm, date window or
recall number. Part of the Pubvera suite; live at `https://recallis.pubvera.com`.

Data source: the openFDA drug enforcement API, queried through the
`drug-enforcement-pp-cli` command-line tool. No API key is needed.

## How it works

- `main.go` is a small Go HTTP server. Each API request runs the CLI as a child
  process and returns its JSON output.
- `index.html` is the whole frontend: search modals, result tables, row badges
  and all four exports (BibTeX, Excel, CSV, JSON). Exports are built in the
  browser; the server only serves data. A change to an export is an
  `index.html` change and needs a new image to go live.
- The CLI is built from source in the Docker build, from the upstream
  `printing-press-library` repository at a pinned commit
  (`ARG PP_LIBRARY_COMMIT` in the `Dockerfile`).

## API

All four search endpoints take a JSON request body (max 64 KiB).

| Endpoint | Body fields | Defaults and limits |
|---|---|---|
| `/api/check` | `drug` (required), `class` (optional) | — |
| `/api/firm` | `firm` (required), `limit` | `limit` default 15, max 50 |
| `/api/recent` | `days`, `limit` | `days` default 30, max 365; `limit` default 15, max 50 |
| `/api/reference` | `recall_number` (required) | — |

Other routes:

| Route | Purpose |
|---|---|
| `/healthz` | Liveness: the server process answers |
| `/readyz` | Readiness: `200 ready` when the CLI binary exists, is a regular file and is executable; otherwise `503` |
| `/` | Serves `index.html` and `/config.json` |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8094` | Listen port (binds `0.0.0.0`) |
| `CLI_BIN` | `./drug-enforcement-pp-cli` | Path to the CLI binary (`/app/drug-enforcement-pp-cli` in the image) |
| `CLI_MAX_CONCURRENT` | `4` | Max CLI child processes at once; `0` or negative disables the bound (logged) |
| `SUPABASE_URL` | — | Enables Google sign-in when set together with the key below |
| `SUPABASE_PUBLISHABLE_KEY` | — | Supabase publishable key (not the secret key) |

Without both Supabase variables the page runs unauthenticated and Supabase is
never loaded.

## Timeouts

| Setting | Value |
|---|---|
| CLI run | 120 s |
| Read header | 10 s |
| Read | 30 s |
| Write | 150 s (CLI run + 30 s, so the server never cuts off a running CLI) |
| Idle | 120 s |

## Local development

```bash
go build ./... && go vet ./... && go test ./...
```

To run the server locally, build the CLI from `printing-press-library` at the
commit pinned in the `Dockerfile`, then:

```bash
CLI_BIN=<path-to-cli> PORT=8094 go run .
```

## Deploy

- CI (`.github/workflows/deploy.yml`) runs on pull requests and on `main`:
  build, vet, gofmt, race tests, govulncheck, and a Docker build.
- Pull requests only build; pushes to `main` publish the image to GHCR.
- On the server, Watchtower pulls the new image automatically.
- The container listens on `127.0.0.1:8094` only; Caddy terminates HTTPS in
  front of it.

Verify a deploy on the server:

```bash
docker inspect recallis --format '{{index .Config.Labels "org.opencontainers.image.revision"}}'
```

The output must equal the local `git rev-parse HEAD`.
