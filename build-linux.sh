#!/usr/bin/env sh
set -eu

: "${GOARCH:=amd64}"
: "${OUTPUT:=dist/focus-loader-report-upload-linux-${GOARCH}}"

mkdir -p "$(dirname "$OUTPUT")"
CGO_ENABLED=1 GOOS=linux GOARCH="$GOARCH" go build -buildvcs=false -trimpath -ldflags="-s -w" -o "$OUTPUT" .
sha256sum "$OUTPUT"
