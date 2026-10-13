#!/bin/sh
# phases.sh KERNEL [CPU] — profile the baseline compile of one kernel
# (BenchmarkPhase/KERNEL/all) and print each phase's share of the samples.
# Phases are cumulative under the named frames, so they can overlap with the
# malloc and GC lines.
set -e
k=${1:?usage: phases.sh KERNEL [CPU]}
cpu=${2:-0}
D=$(cd "$(dirname "$0")" && pwd)
O=$(cd "$D/.." && pwd)/.build/nyanjit
mkdir -p "$O"
go test -C "$D" -c -o "$O/phase.test" .
taskset -c "$cpu" "$O/phase.test" -test.run '^$' -test.bench "^BenchmarkPhase/$k/all\$" \
	-test.benchtime 3s -test.cpuprofile "$O/$k.prof" | grep '^Benchmark'
share() {
	v=$(go tool pprof -top -nodecount=100000 -nodefraction=0 -edgefraction=0 -focus="$2" ${3:+-ignore="$3"} \
		"$O/phase.test" "$O/$k.prof" 2>/dev/null | grep -oE 'accounting for [0-9.]+m?s, [0-9.]+%' | grep -oE '[0-9.]+%' || true)
	printf "  %-34s %6s\n" "$1" "${v:-0%}"
}
share "NewBuilderFor (setup)" 'nyanjit\.NewBuilderFor$'
share "kernel build (Builder calls)" 'jitbench/nyanjit\.(loop|fp|tiny|collatz|pressure)$' 'compile\.\(\*Stream\)\.Segment'
share "  of which Stream.Segment" 'compile\.\(\*Stream\)\.Segment$'
share "analyze (liveness)" 'baselineEngine\)\.analyze$'
share "allocate (+select/emit records)" 'baselineEngine\)\.allocate$'
share "finish (layout, encode)" 'baselineEngine\)\.finish$'
share "Template" 'Unit\)\.Template$'
share "Lower other (Stream.Unit glue)" 'nyanjit\.\(\*Builder\)\.Lower$' 'baselineEngine\)\.(analyze|allocate|finish)$'
share "-- malloc (all)" 'runtime\.mallocgc$'
share "-- GC (assist/bg/barriers)" 'gcAssistAlloc$|gcBgMarkWorker$|wbBufFlush$|gcWriteBarrier|bulkBarrierPreWrite'
echo "profile: $O/$k.prof (go tool pprof $O/phase.test $O/$k.prof)"
