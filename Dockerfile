# syntax=docker/dockerfile:1
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
# output: /web/dist

FROM golang:1.25-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf internal/webui/dist && mkdir -p internal/webui/dist
COPY --from=web /web/dist/ internal/webui/dist/
RUN CGO_ENABLED=0 go build -o /gritual ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 65532 -H gritual \
    && mkdir -p /data/media && chown 65532:65532 /data/media
COPY --from=go /gritual /gritual
USER 65532:65532
ENV MEDIA_DIR=/data/media HTTP_ADDR=:8080
ENTRYPOINT ["/gritual"]
