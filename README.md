# Gritual

Life-together app: small circles, shared rituals, kind competitions.

One Go process serves the REST API and an embedded Vite SPA. PostgreSQL 16 is the only sidecar.

## Local development

```bash
make dev
```

Starts compose Postgres, the Go API on `:8080`, and Vite on `:5173`. Vite proxies `/api`, `/media`, `/healthz`, `/readyz`, and `/mcp` to the API. `/metrics` is **not** proxied (`METRICS_ADDR` defaults to `127.0.0.1:9090`).

```bash
make up
```

Builds the production image and serves the embedded UI at `http://localhost:8080`.

```bash
make test
make vet
make build   # Vite → internal/webui/dist, then go build
```

Copy `.env.example` to `.env` for later PRs. `AUTH_DEV_LOGIN=1` is local-only and must never be set on `gritual.fit`.

## Production

Always self-hosted on a house Linux box:

1. docker compose (`app` + `postgres`)
2. Cloudflare Tunnel to `https://gritual.fit`
3. `APP_BASE_URL=https://gritual.fit`

TLS and DNS live at Cloudflare. No inbound 80/443 on the machine. Cloudflare Access is **off**.

```bash
cloudflared tunnel create gritual
cloudflared tunnel route dns gritual gritual.fit
```

Mount `deploy/cloudflared/config.yml` into a `cloudflared` sidecar (see comments in `docker-compose.yml`) or run `cloudflared` on the host. Bind-mount `/var/lib/gritual/media` (uid `65532`) and Postgres data under `/var/lib/gritual/pg`.
