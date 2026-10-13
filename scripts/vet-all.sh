#!/bin/sh
set -eu
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo"
for arch in amd64 386 arm64 riscv64 ppc64 ppc64le; do
    echo "go vet: linux/$arch"
    GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go vet ./...
done
(cd e2e && go vet ./...)
