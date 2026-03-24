.PHONY: proto up down build test seed lint

proto:
	protoc --go_out=proto/gen --python_out=ai_service/gen proto/analysis.proto

up:
	docker compose up --build -d

down:
	docker compose down -v

build:
	go build ./cmd/engine/...

test:
	go test ./... -race -count=1

seed:
	go run ./cmd/seed/main.go

lint:
	golangci-lint run ./...
