#!/bin/sh
# Fetches sljit at the pinned commit into ../.build/sljit-src and builds the
# harness as ../.build/sljit/bench.
set -e
SLJIT_REPO=https://github.com/zherczeg/sljit.git
SLJIT_COMMIT=39c508d1b8fbae332d60c7450db20236913089da
D=$(cd "$(dirname "$0")" && pwd)
B=$D/../.build
src=$B/sljit-src
if [ ! -d "$src/.git" ]; then
	git clone -q "$SLJIT_REPO" "$src"
fi
if [ "$(git -C "$src" rev-parse HEAD)" != "$SLJIT_COMMIT" ]; then
	git -C "$src" fetch -q origin
	git -C "$src" checkout -q --detach "$SLJIT_COMMIT"
fi
mkdir -p "$B/sljit"
${CC:-gcc} -O2 -I"$src/sljit_src" -o "$B/sljit/bench" "$D/bench.c" -lm
