
.PHONY: migrate test run fmt vet
DATABASE_PATH ?= data/app.log

# 运行时持久化为仅追加哈希链日志，服务启动时自动创建与校验；
# 此目标仅确保数据目录存在。migrations/001_bootstrap.sql 是规范关系模式。
migrate:
	mkdir -p $$(dirname "$(DATABASE_PATH)")

test:
	go test ./...

run:
	DATABASE_PATH="$(DATABASE_PATH)" go run ./cmd/server

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...
