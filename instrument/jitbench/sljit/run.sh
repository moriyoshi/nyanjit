#!/bin/sh
# Runs every kernel, one process each, pinned to CPU ${1:-0}. Builds the
# harness first if needed. Extra arguments go to the harness (its first
# argument is the per-round budget in ms).
set -e
D=$(cd "$(dirname "$0")" && pwd)
CPU=${1:-0}
[ $# -gt 0 ] && shift
BIN=$D/../.build/sljit/bench
[ -x "$BIN" ] || "$D/build.sh"
first=1
for k in tiny chain100 chain1000 loop pressure collatz fp; do
	if [ $first = 1 ]; then
		K=$k taskset -c "$CPU" "$BIN" "$@"
		first=0
	else
		K=$k taskset -c "$CPU" "$BIN" "$@" | tail -n +2
	fi
done
