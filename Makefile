.PHONY: web dev test vet build docker up

web:
	cd web && npm ci && npm run build
	rm -rf internal/webui/dist
	mkdir -p internal/webui/dist
	cp -a web/dist/. internal/webui/dist/

dev:
	docker compose up -d postgres
	@echo "API http://127.0.0.1:8080  UI http://127.0.0.1:5173  (Ctrl-C stops both; postgres stays up)"
	@bash -c 'set -e; trap "kill 0" EXIT INT TERM; \
	  AUTH_DEV_LOGIN=1 \
	  DATABASE_URL=postgres://gritual:gritual@127.0.0.1:5432/gritual?sslmode=disable \
	  APP_BASE_URL=http://localhost:8080 \
	  VITE_DEV_ORIGIN=http://localhost:5173 \
	  XAI_API_KEY="$${XAI_API_KEY}" \
	  XAI_VISION_MODEL="$${XAI_VISION_MODEL:-grok-4.5}" \
	  go run ./cmd/server & \
	  cd web && { [ -d node_modules ] || npm ci; } && npm run dev'

test:
	go test ./...

vet:
	go vet ./...
	gofmt -s -l .

build: web
	mkdir -p bin
	go build -o bin/gritual ./cmd/server

docker:
	docker build -t gritual:dev .

up:
	docker compose up --build
