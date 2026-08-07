.PHONY: run try build test tidy fmt vet migrate migrate-down db-up db-down db-status

run:
	go run ./cmd/renderd

# make try CURL="curl 'https://dummyjson.com/products/1'" ARGS="-map title:title:string"
try:
	go run ./cmd/pipelinetry -curl $(CURL) $(ARGS)

migrate:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

# Which Postgres am I actually talking to, and is it migrated?
db-status:
	@echo "DATABASE_URL: $${DATABASE_URL:-(unset — using the dev default on :5432)}"
	@psql "$${DATABASE_URL:-postgres://content_pipeline_insider:content_pipeline_insider@localhost:5432/content_pipeline_insider?sslmode=disable}" \
		-tAc "SELECT 'server   : ' || version();" 2>/dev/null \
		|| { echo "server   : UNREACHABLE — start one with 'make db-up' (container on :5433) or your native Postgres"; exit 1; }
	@psql "$${DATABASE_URL:-postgres://content_pipeline_insider:content_pipeline_insider@localhost:5432/content_pipeline_insider?sslmode=disable}" \
		-tAc "SELECT 'migration: version ' || version || CASE WHEN dirty THEN ' (DIRTY)' ELSE ' (clean)' END FROM schema_migrations;" 2>/dev/null \
		|| echo "migration: none applied — run 'make migrate'"
	@psql "$${DATABASE_URL:-postgres://content_pipeline_insider:content_pipeline_insider@localhost:5432/content_pipeline_insider?sslmode=disable}" \
		-tAc "SELECT 'pipelines: ' || count(*) FROM content_pipelines;" 2>/dev/null || true

# Only needed if you have no native Postgres. Publishes on 5433 to avoid
# colliding with one that does; set DATABASE_URL accordingly.
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
