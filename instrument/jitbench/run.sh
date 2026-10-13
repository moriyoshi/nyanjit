#!/bin/sh
# run.sh [-c CPU] [-o DIR] [ENGINE...] — runs the JIT comparison harnesses,
# one kernel per process pinned to CPU, writes DIR/ENGINE.txt for each
# engine and DIR/summary.md with the merged tables.
#
# ENGINE is nyanjit, sljit, cranelift or tpde; the default is every engine
# whose tools are present (cranelift needs cargo; tpde needs docker and an
# arm64 host). CPU defaults to the first core with the highest
# cpu_capacity, so a big core on a heterogeneous host. DIR defaults to
# .build/results/<UTC timestamp>.
set -e
D=$(cd "$(dirname "$0")" && pwd)
cpu= out=
while getopts c:o: opt; do
	case $opt in
	c) cpu=$OPTARG ;;
	o) out=$OPTARG ;;
	*) echo "usage: $0 [-c CPU] [-o DIR] [nyanjit|sljit|cranelift|tpde]..." >&2; exit 2 ;;
	esac
done
shift $((OPTIND - 1))
if [ -z "$cpu" ]; then
	cpu=0 best=-1
	for f in /sys/devices/system/cpu/cpu[0-9]*/cpu_capacity; do
		[ -r "$f" ] || continue
		c=$(cat "$f") n=${f#/sys/devices/system/cpu/cpu}
		n=${n%%/*}
		if [ "$c" -gt "$best" ] || { [ "$c" -eq "$best" ] && [ "$n" -lt "$cpu" ]; }; then
			best=$c cpu=$n
		fi
	done
fi
out=${out:-$D/.build/results/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$out"
engines=$*
if [ -z "$engines" ]; then
	engines="nyanjit sljit"
	command -v cargo >/dev/null && engines="$engines cranelift"
	command -v docker >/dev/null && [ "$(uname -m)" = aarch64 ] && engines="$engines tpde"
fi
echo "CPU $cpu, results in $out" >&2
for e in $engines; do
	[ -x "$D/$e/run.sh" ] || { echo "unknown engine $e" >&2; exit 2; }
	echo "== $e" >&2
	"$D/$e/run.sh" "$cpu" >"$out/$e.txt"
done
files=
for e in $engines; do files="$files $out/$e.txt"; done
# shellcheck disable=SC2086
awk -f "$D/summarize.awk" $files | tee "$out/summary.md"
