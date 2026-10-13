#!/bin/sh
# Runs every kernel x {speed, fast}, one process each, pinned to CPU ${1:-0}.
# Builds the harness into ../.build/cranelift first if needed. Extra
# arguments after the CPU are passed to clbench (e.g. --bind shared).
set -e
D=$(cd "$(dirname "$0")" && pwd)
CPU=${1:-0}
[ $# -gt 0 ] && shift
export CARGO_TARGET_DIR="$D/../.build/cranelift"
BIN=$CARGO_TARGET_DIR/release/clbench
[ -x "$BIN" ] || cargo build --manifest-path "$D/Cargo.toml" --release --locked -q
$BIN -k none "$@" # header only
for k in tiny chain100 chain1000 loop pressure collatz fp; do
	for c in speed fast; do
		taskset -c "$CPU" "$BIN" --config "$c" -k "$k" --no-header "$@"
	done
done
