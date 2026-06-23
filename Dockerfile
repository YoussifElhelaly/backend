# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o server ./cmd/server

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/server .

# whatsmeow needs write access for its SQLite WAL (if using file-based store).
# The store path is set via DB env vars; this is a safe default.
RUN mkdir -p /data && chown nobody:nobody /data

USER nobody

EXPOSE 8080

ENV GIN_MODE=release

ENTRYPOINT ["./server"]
