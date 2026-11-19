#!/bin/sh
# Cross-builds every release target into $1 with version $2.
set -eu
out=${1:?output dir}
version=${2:-dev}
mkdir -p "$out"
for os in windows linux darwin; do
  for arch in amd64 arm64; do
    ext=""
    [ "$os" = windows ] && ext=".exe"
    GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=$version" \
      -o "$out/kumiko-satellite-$os-$arch$ext" ./cmd/kumiko-satellite
  done
done
