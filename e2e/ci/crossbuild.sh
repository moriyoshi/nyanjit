#!/bin/sh
set -eu
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo"
exec go -C e2e run ./cmd/nyanjit-e2e -root "$repo" \
    -jobs "${NYANJIT_E2E_JOBS:-4}" -deadline 45m \
    -junit "$repo/e2e/.build/results/crossbuild.xml" "$@" e2e/crossbuild.star
