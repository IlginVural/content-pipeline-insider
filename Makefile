.PHONY: run build test tidy fmt vet migrate migrate-down db-up db-down

run:
	go run ./cmd/renderd

migrate:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

build:
	go build -o bin/renderd ./cmd/renderd

test:
	go test ./...

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...
