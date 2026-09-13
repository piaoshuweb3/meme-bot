#!/usr/bin/env bash
# meme-bot 一键验证脚本
#
# 用途：在干净环境中完成 依赖解析 → 静态检查 → 单元测试 → 编译。
# 用法：
#   bash scripts/verify.sh              # 全量验证
#   bash scripts/verify.sh --no-tidy    # 跳过 go mod tidy（依赖已就绪时更快）
#
# 注意：本机直连 proxy.golang.org 会 SSL 失败，因此默认走国内代理。

set -euo pipefail

cd "$(dirname "$0")/.."

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-off}"

SKIP_TIDY=0
for arg in "$@"; do
	case "$arg" in
		--no-tidy) SKIP_TIDY=1 ;;
		*) echo "未知参数: $arg" >&2; exit 2 ;;
	esac
done

echo "==> 环境"
go version
echo "GOPROXY=$GOPROXY  GOSUMDB=$GOSUMDB"

if [ "$SKIP_TIDY" -eq 0 ]; then
	echo "==> go mod tidy"
	go mod tidy
fi

echo "==> gofmt 检查"
unformatted="$(gofmt -l . | grep -v '^$' || true)"
if [ -n "$unformatted" ]; then
	echo "以下文件未格式化（运行 gofmt -w 修复）：" >&2
	echo "$unformatted" >&2
	exit 1
fi

echo "==> go vet"
go vet ./...

echo "==> go test"
go test ./... -count=1

echo "==> go build"
go build ./...

echo "==> 编译二进制"
mkdir -p bin
go build -trimpath -o bin/bot ./cmd/bot
go build -trimpath -o bin/migrator ./cmd/migrator
go build -trimpath -o bin/worker ./cmd/worker

echo
echo "✅ 全部验证通过。产物位于 ./bin/"
echo "   下一步：make infra && make migrate && make run"
