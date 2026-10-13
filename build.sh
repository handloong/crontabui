#!/usr/bin/env bash
# 交叉编译 Linux/amd64 静态二进制。可在 Windows(WSL)/macOS/Linux 上运行。
set -euo pipefail

APP="crontabui"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="dist/${APP}-linux-amd64"

mkdir -p dist

echo "==> 运行测试"
go test -count=1 ./...

echo "==> 构建 ${OUT}（version=${VERSION}）"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "${OUT}" "./cmd/${APP}"

# 把 Debian 安装脚本一起放进 dist/，方便直接 scp 到服务器
if [ -f deploy/install-corntab.sh ]; then
  install -m 0755 deploy/install-corntab.sh dist/install-corntab.sh
  echo "==> 安装脚本：dist/install-corntab.sh"
fi

SIZE=$(du -h "${OUT}" | cut -f1)
echo "==> 完成：${OUT}（${SIZE}）"
echo ""
echo "  部署（Debian 12）："
echo "    scp dist/${APP}-linux-amd64 dist/install-corntab.sh user@host:/tmp/"
echo "    ssh user@host 'sudo /tmp/install-corntab.sh --binary /tmp/${APP}-linux-amd64'"
