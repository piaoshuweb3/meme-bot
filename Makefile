# meme-bot Makefile —— 常用开发与验证命令
#
# 注意：本机直连 proxy.golang.org 不可达，默认使用国内代理。
GOPROXY ?= https://goproxy.cn,direct
GOSUMDB ?= off

GO ?= go
BIN_DIR := bin
CONFIG ?= configs/config.yaml

export GOPROXY
export GOSUMDB

.PHONY: help
help:
	@echo "meme-bot 可用命令："
	@echo "  make tidy      依赖解析（首次必须执行）"
	@echo "  make build     编译全部二进制到 ./bin"
	@echo "  make vet       静态检查"
	@echo "  make test      单元测试"
	@echo "  make fmt       格式化代码"
	@echo "  make verify    tidy + fmt-check + vet + test（一键验证）"
	@echo "  make run       启动主程序"
	@echo "  make worker    启动后台任务"
	@echo "  make migrate   执行数据库迁移"
	@echo "  make infra     启动本地 Postgres + Redis（docker compose）"
	@echo "  make web       安装并启动前端 Dashboard"
	@echo "  make clean     清理构建产物"

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: build
build:
	mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -o $(BIN_DIR)/bot ./cmd/bot
	$(GO) build -trimpath -o $(BIN_DIR)/migrator ./cmd/migrator
	$(GO) build -trimpath -o $(BIN_DIR)/worker ./cmd/worker

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: test
test:
	$(GO) test ./... -count=1

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check:
	@out=$$(gofmt -l . | grep -v '^$$' || true); \
	if [ -n "$$out" ]; then echo "以下文件未格式化:"; echo "$$out"; exit 1; fi; \
	echo "gofmt 检查通过"

.PHONY: verify
verify: tidy fmt-check vet test
	@echo "✅ 验证完成"

.PHONY: run
run:
	$(GO) run ./cmd/bot -config $(CONFIG)

.PHONY: worker
worker:
	$(GO) run ./cmd/worker -config $(CONFIG)

.PHONY: migrate
migrate:
	$(GO) run ./cmd/migrator -config $(CONFIG) -dir migrations

.PHONY: infra
infra:
	cd deploy && docker compose up -d postgres redis

.PHONY: infra-down
infra-down:
	cd deploy && docker compose down

.PHONY: web
web:
	cd web && npm install && npm run dev

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
