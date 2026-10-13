#!/bin/sh
# Runs every kernel x {alloca, ssa} of the TPDE-LLVM harness, one process
# each, inside nyanjit-bench-tpde:local pinned to host CPU ${1:-0}. Builds
# the harness first if needed. Extra harness flags go in TPDE_BENCH_FLAGS
# (e.g. "-t 300").
set -e
D=$(cd "$(dirname "$0")" && pwd)
J=$(cd "$D/.." && pwd)
CPU=${1:-0}
[ -x "$J/.build/tpde/tpdebench" ] || "$D/build.sh"
exec docker run --rm --name "nyanjit-bench-tpde-run-$CPU-$$" --cpuset-cpus "$CPU" \
	-u "$(id -u):$(id -g)" -v "$J/.build/tpde:/work:ro" -w /work nyanjit-bench-tpde:local sh -c '
		./tpdebench -header
		for k in tiny chain100 chain1000 loop pressure collatz fp; do
			for v in alloca ssa; do
				taskset -c '"$CPU"' ./tpdebench -k $k -v $v '"${TPDE_BENCH_FLAGS:-}"' 2>/dev/null
			done
		done
	'
