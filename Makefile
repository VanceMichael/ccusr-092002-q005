.PHONY: migrate test run
DATABASE_PATH ?= data/app.json
migrate:
	DATABASE_PATH="$(DATABASE_PATH)" go run ./cmd/server -migrate-only
test:
	go test ./...
run:
	go run ./cmd/server
