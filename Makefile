.PHONY: proto up down build test seed lint

proto:
	mkdir -p proto/gen ai_service/gen
	protoc \
		--go_out=proto/gen --go_opt=paths=source_relative \
		--go-grpc_out=proto/gen --go-grpc_opt=paths=source_relative \
		proto/analysis.proto
	python -m grpc_tools.protoc \
		-I. \
		--python_out=ai_service/gen \
		--grpc_python_out=ai_service/gen \
		proto/analysis.proto

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
