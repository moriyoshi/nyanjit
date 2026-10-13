#!/bin/sh
# Fetches TPDE at the pinned commit into ../.build/tpde-src, builds the
# nyanjit-bench-tpde:local image if it is missing, and builds the harness in
# it as ../.build/tpde/tpdebench. Set TPDE_BUILD_CPUS (a cpuset such as
# 10-17) to keep the build off the cores you benchmark on.
set -e
TPDE_REPO=https://github.com/tpde2/tpde
TPDE_COMMIT=9779acf4ada3736e779391da1e4b3369dba08024
D=$(cd "$(dirname "$0")" && pwd)
J=$(cd "$D/.." && pwd)
src=$J/.build/tpde-src
if [ ! -d "$src/.git" ]; then
	git clone -q "$TPDE_REPO" "$src"
fi
if [ "$(git -C "$src" rev-parse HEAD)" != "$TPDE_COMMIT" ]; then
	git -C "$src" fetch -q origin
	git -C "$src" checkout -q --detach "$TPDE_COMMIT"
fi
git -C "$src" submodule update -q --init --recursive
if ! docker image inspect nyanjit-bench-tpde:local >/dev/null 2>&1; then
	docker build -q -t nyanjit-bench-tpde:local "$D"
fi
docker run --rm --name "nyanjit-bench-tpde-build-$$" ${TPDE_BUILD_CPUS:+--cpuset-cpus "$TPDE_BUILD_CPUS"} \
	-u "$(id -u):$(id -g)" -v "$J:/work" -w /work nyanjit-bench-tpde:local sh -c '
		set -e
		cmake -S tpde/harness -B .build/tpde -G Ninja \
			-DCMAKE_BUILD_TYPE=Release \
			-DCMAKE_C_COMPILER=clang-19 -DCMAKE_CXX_COMPILER=clang++-19 \
			-DLLVM_DIR=/usr/lib/llvm-19/lib/cmake/llvm \
			-DTPDE_SRC=/work/.build/tpde-src
		ninja -C .build/tpde -j8 tpdebench
	'
