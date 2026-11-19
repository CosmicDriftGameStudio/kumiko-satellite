#!/bin/sh
# Builds the macOS binary for one arch ($2: amd64 or arm64) on a Mac, with cgo for the WKWebView window.
set -eu
out=${1:?output dir}
arch=${2:?arch amd64|arm64}
version=${3:-dev}
mkdir -p "$out"
GOOS=darwin GOARCH=$arch CGO_ENABLED=1 go build -trimpath \
  -ldflags "-s -w -X main.version=$version" \
  -o "$out/kumiko-satellite-darwin-$arch" ./cmd/kumiko-satellite
