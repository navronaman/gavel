FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o engine ./cmd/engine/...

# ── Runtime image ─────────────────────────────────────────────────────────────
FROM alpine:3.20

WORKDIR /app
COPY --from=builder /app/engine .

CMD ["./engine"]
