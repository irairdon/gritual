# Gritual — Agent Instructions

## Work Style
- Work autonomously. Do not ask clarifying questions unless truly blocked.
- Prefer action over discussion. Simplest approach that satisfies the spec.
- If something fails, try a different approach before asking for help.
- Commit working increments when asked. Conventional Commits.

## Spec-Driven Development
- Source of truth: `SPEC.md` + this file. Architecture: the design doc that seeded them.
- Read `SPEC.md` before implementing.
- If the spec is ambiguous, make a reasonable interpretation and note it in a short comment.
- Do not gold-plate. Do not pull v1.1/v2 into v1.

## Stack (non-negotiable without a spec change)
- Go 1.25 (or current stable): `cmd/server/main.go` (`import _ "time/tzdata"`)
- chi/v5, PostgreSQL 16, pgx/v5
- REST JSON `/api/v1/` envelope `{ "error": { "code", "message" } }`
- Embedded SQL migrations: `//go:embed migrations/*.sql`; **one transaction per file**
- Vite + TS + functional React + Tailwind v4 in `web/` (`base: '/'`)
- Production UI: `web/dist` copied to `internal/webui/dist` (same path in Make and Docker) and `//go:embed all:dist`
- One process serves API + SPA. No Node in production.
- SpaceXAI: `XAI_API_KEY`, `https://api.x.ai/v1/chat/completions`, default `grok-4.5`. Never in the Vite bundle.
- Canonical type string: `workout` (not `strength`).
- MCP v1: PAT only. No OAuth AS.
- Table-driven tests. `gofmt -s`. `log/slog` JSON.

## Layout
cmd/server/main.go
internal/   # auth, circles, rituals, logs, meals, ai, mcp, scoring, webui, jobs
web/        # Vite SPA
internal/webui
mobile/     # v1.1 Capacitor; webDir ../internal/webui/dist
deploy/cloudflared/   # v1 prod: house Linux + compose; gritual.fit → app:8080

## Dev
- `make dev` — Vite :5173 proxies `/api`, `/media`, `/healthz`, `/readyz`, `/mcp` to Go :8080 + compose Postgres. Do not proxy `/metrics`.
- `make up` — embedded UI.
- `AUTH_DEV_LOGIN=1` local-only. Never on `gritual.fit`.
- Admin is `ADMIN_EMAIL` after email verify, not first-user.
- Prod: `APP_BASE_URL=https://gritual.fit`, SMTP for magic/verify, Cloudflare Tunnel. SMTP is not for invites.

## Security defaults
- Health logs default private. Challenge join grants matching-log share.
- SW never caches `/api` or `/media`.
- Strip EXIF; client+server JPEG re-encode.
- AI tools run as the session user; structured args.
- 18+; DOB frozen. DELETE /me implemented as specified.

## Git
- Conventional Commits
- Remote `git@github.com-irairdon:irairdon/gritual.git`
