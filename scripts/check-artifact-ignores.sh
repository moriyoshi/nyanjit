#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
for path in .agents-workspace/tmp/ignore-probe e2e/.build/ignore-probe compile.test; do
 git check-ignore --no-index "$path"
done
