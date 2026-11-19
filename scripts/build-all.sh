#!/bin/sh
# Cross-builds the windows and linux release targets into $1 with version $2.
# macOS is built natively by scripts/build-macos.sh: its window needs cgo.
set -eu
out=${1:?output dir}
version=${2:-dev}
mkdir -p "$out"
for os in windows linux; do
  for arch in amd64 arm64; do
    ext=""
    flags="-s -w -X main.version=$version"
    if [ "$os" = windows ]; then
      ext=".exe"
      # GUI subsystem: a double click opens no console window.
      flags="$flags -H=windowsgui"
    fi
    GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
      -ldflags "$flags" \
      -o "$out/kumiko-satellite-$os-$arch$ext" ./cmd/kumiko-satellite
  done
done
