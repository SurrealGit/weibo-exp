#!/bin/sh
set -eu

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$PROJECT_DIR"

: "${GOCACHE:=${TMPDIR:-/tmp}/weibo-exp-go-build}"
: "${GOMODCACHE:=${TMPDIR:-/tmp}/weibo-exp-go-mod}"
export GOCACHE GOMODCACHE

unformatted=$(gofmt -l cmd internal scripts)
if [ -n "$unformatted" ]; then
  echo "以下文件需要执行 gofmt："
  echo "$unformatted"
  exit 1
fi
go vet ./...
go test -shuffle=on ./...
go test -race ./...
go build -trimpath -ldflags="-s -w" -o dist/weibo-exp ./cmd/weibo-exp

echo "检查通过：dist/weibo-exp（仅本机构建；发行包请运行 build-release.sh 后再运行 test-release.sh）"
