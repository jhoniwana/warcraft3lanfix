#!/usr/bin/env bash
#
# Builds the wc3lan binaries for every supported platform.
#
# The result is a single static file per platform: the players only need to
# replace Game.exe, they never have to install anything.
#
set -euo pipefail

cd "$(dirname "$0")/.."
OUT=${OUT:-dist}
VERSION=$(cat VERSION 2>/dev/null || echo "0.1.0")

mkdir -p "$OUT"

build() {
  local goos=$1 goarch=$2
  local name="wc3lan-${goos}-${goarch}"
  echo "==> $name"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
    go build -trimpath -ldflags "-s -w" -o "$OUT/$name" ./cmd/wc3lan
  # Windows builds get a .exe suffix.
  if [[ $goos == windows ]]; then
    mv "$OUT/$name" "$OUT/$name.exe"
    name="$name.exe"
  fi
  echo "    $OUT/$name"
}

build windows amd64
build windows 386
build linux   amd64
build linux   arm64
build darwin  amd64
build darwin  arm64

echo
echo "Done. The files are in $OUT"
ls -la "$OUT"
