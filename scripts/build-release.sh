#!/bin/sh
set -eu

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$PROJECT_DIR"

: "${GOCACHE:=${TMPDIR:-/tmp}/weibo-exp-go-build}"
: "${GOMODCACHE:=${TMPDIR:-/tmp}/weibo-exp-go-mod}"
export GOCACHE GOMODCACHE CGO_ENABLED=0

for tool in go tar zip shasum; do
  command -v "$tool" >/dev/null || { echo "发行构建需要开发工具：$tool" >&2; exit 1; }
done
version=$(sed -n 's/^const Version = "\([^"]*\)"$/\1/p' internal/app/config.go)
case "$version" in
  ''|*[!0-9.]*) echo "无法读取发行版本号" >&2; exit 1 ;;
esac
mkdir -p dist/releases
release_stage=$(mktemp -d "${TMPDIR:-/tmp}/weibo-release.XXXXXX")
trap 'rm -r -- "$release_stage"' EXIT
# Disable AppleDouble resource-fork entries. tar's PAX xattrs are a separate
# mechanism and must also be disabled explicitly below.
export COPYFILE_DISABLE=1
# BSD tar and GNU tar use different owner-override options.
case "$(tar --version)" in
  *bsdtar*) tar_owner_options='--uid 0 --gid 0 --uname root --gname root' ;;
  *GNU*) tar_owner_options='--owner=root:0 --group=root:0' ;;
  *) echo "发行构建需要 BSD tar 或 GNU tar" >&2; exit 1 ;;
esac

build() {
  target_os=$1
  target_arch=$2
  output=$3
  echo "构建 $target_os/$target_arch -> $output"
  GOOS=$target_os GOARCH=$target_arch go build -trimpath -ldflags="-s -w" -o "$output" ./cmd/weibo-exp

  package="$release_stage/$target_os-$target_arch"
  mkdir -p "$package"
  entry=weibo-exp
  if [ "$target_os" = windows ]; then entry=weibo-exp.exe; fi
  cp "$output" "$package/$entry"
  cp README.md LICENSE THIRD_PARTY_NOTICES.md "$package/"
  mkdir -p "$package/assets/readme"
  cp assets/readme/logo.svg "$package/assets/readme/logo.svg"
  set -- "$entry" README.md LICENSE THIRD_PARTY_NOTICES.md assets/readme/logo.svg
  archive="weibo-exp-v$version-$target_os-$target_arch"
  if [ "$target_os" = windows ]; then
    (cd "$package" && zip -X -q "$release_stage/$archive.zip" "$@")
    mv "$release_stage/$archive.zip" "dist/releases/$archive.zip"
  else
    tar --no-xattrs --no-acls $tar_owner_options -czf "$release_stage/$archive.tar.gz" -C "$package" "$@"
    mv "$release_stage/$archive.tar.gz" "dist/releases/$archive.tar.gz"
  fi
}

build darwin arm64 dist/weibo-exp-darwin-arm64
build darwin amd64 dist/weibo-exp-darwin-amd64
build linux amd64 dist/weibo-exp-linux-amd64
build windows amd64 dist/weibo-exp-windows-amd64.exe

native_os=$(go env GOHOSTOS)
native_arch=$(go env GOHOSTARCH)
native_source="dist/weibo-exp-$native_os-$native_arch"
if [ "$native_os" = windows ]; then native_source="$native_source.exe"; fi
if [ -f "$native_source" ]; then
  if [ "$native_os" = windows ]; then
    cp "$native_source" dist/weibo-exp.exe
  else
    cp "$native_source" dist/weibo-exp
  fi
fi

shasum -a 256 \
  dist/weibo-exp-darwin-arm64 \
  dist/weibo-exp-darwin-amd64 \
  dist/weibo-exp-linux-amd64 \
  dist/weibo-exp-windows-amd64.exe > dist/SHA256SUMS.txt

echo "校验文件：dist/SHA256SUMS.txt"
(cd dist/releases && shasum -a 256 \
  "weibo-exp-v$version-darwin-arm64.tar.gz" \
  "weibo-exp-v$version-darwin-amd64.tar.gz" \
  "weibo-exp-v$version-linux-amd64.tar.gz" \
  "weibo-exp-v$version-windows-amd64.zip" > SHA256SUMS.txt)
echo "用户发行包及校验文件：dist/releases/"
