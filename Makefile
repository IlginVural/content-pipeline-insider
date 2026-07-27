.PHONY: run build test tidy fmt vet

run:
	go run ./cmd/renderd

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
