#!/bin/sh
# Runs every kernel at both tiers, one process each, pinned to CPU ${1:-0}.
# Extra arguments go to the harness (-t budget per round, -r rounds).
set -e
D=$(cd "$(dirname "$0")" && pwd)
CPU=${1:-0}
[ $# -gt 0 ] && shift
BIN=$D/../.build/nyanjit/bench
mkdir -p "$(dirname "$BIN")"
go build -C "$D" -o "$BIN" .
first=1
for m in baseline optimized; do
	for k in tiny chain100 chain1000 loop pressure collatz fp; do
		if [ $first = 1 ]; then
			taskset -c "$CPU" "$BIN" -mode $m -k $k "$@"
			first=0
		else
			taskset -c "$CPU" "$BIN" -mode $m -k $k "$@" | tail -n +2
		fi
	done
done
