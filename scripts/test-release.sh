#!/bin/sh
# Verify actual release archives, not just the build script's source text.
set -eu
PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$PROJECT_DIR"
version=$(sed -n 's/^const Version = "\([^"]*\)"$/\1/p' internal/app/config.go)
test_stage=$(mktemp -d "${TMPDIR:-/tmp}/weibo-release-test.XXXXXX")
trap 'rm -r -- "$test_stage"' EXIT
(cd dist/releases && shasum -a 256 -c SHA256SUMS.txt)
go test ./scripts -run '^TestReleaseMetadata$' -count=1 -args \
  "$PROJECT_DIR/dist/releases/weibo-exp-v$version-darwin-arm64.tar.gz" \
  "$PROJECT_DIR/dist/releases/weibo-exp-v$version-darwin-amd64.tar.gz" \
  "$PROJECT_DIR/dist/releases/weibo-exp-v$version-linux-amd64.tar.gz" \
  "$PROJECT_DIR/dist/releases/weibo-exp-v$version-windows-amd64.zip"

for target in darwin-arm64 darwin-amd64 linux-amd64 windows-amd64; do
  package="$test_stage/$target"
  mkdir -p "$package"
  archive="$PROJECT_DIR/dist/releases/weibo-exp-v$version-$target"
  entry=weibo-exp
  raw="$PROJECT_DIR/dist/weibo-exp-$target"
  if [ "$target" = windows-amd64 ]; then
    entry=weibo-exp.exe
    raw="$raw.exe"
    unzip -q "$archive.zip" -d "$package"
  else
    tar -xzf "$archive.tar.gz" -C "$package"
  fi
  # Exact manifest: no other platform binaries, source code or private data.
  set -- "$entry" README.md LICENSE THIRD_PARTY_NOTICES.md assets/readme/logo.svg
  expected=$(printf '%s\n' "$@" | LC_ALL=C sort)
  actual=$(cd "$package" && find . ! -type d -print | sed 's|^./||' | LC_ALL=C sort)
  [ "$actual" = "$expected" ] || { echo "发行包内容不符：$target" >&2; exit 1; }
  cmp "$raw" "$package/$entry"
  for document in README.md LICENSE THIRD_PARTY_NOTICES.md assets/readme/logo.svg; do
    [ -f "$package/$document" ] && [ ! -L "$package/$document" ]
    cmp "$document" "$package/$document"
  done
  [ -x "$package/$entry" ]
  echo "发行包内容与入口通过：$target"
done

native_target="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
if [ -d "$test_stage/$native_target" ]; then
  entry=weibo-exp
  case "$native_target" in windows-*) entry=weibo-exp.exe ;; esac
  cmp "dist/$entry" "$test_stage/$native_target/$entry"
  result=$("$test_stage/$native_target/$entry" --data-dir "$test_stage/data" version)
  case "$result" in
    "weibo-exp $version ("*) echo "解压后本机入口运行通过：$result" ;;
    *) echo "解压后入口版本不符：$result" >&2; exit 1 ;;
  esac
fi
