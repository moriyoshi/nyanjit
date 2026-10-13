#!/bin/sh
# usage: M=<meas dir> [CPU=17] [TAG=focus] focus.sh <rounds> <tree>...
# One process per sample: count=5 x 300ms, keep the per-process minimum.
# Trees run in the given order each round, so pass "a b" for one run and
# "b a" for the reversed run. Binaries: $M/bin-<tree>-compile.test, built
# from $M/<tree>/compile with `go test -c`. Compare with cmp3.py.
: ${M:?}; n=$1; shift; tag=${TAG:-focus}; mkdir -p $M/logs
for r in $(seq 1 $n); do
  for t in "$@"; do
    cd $M/$t/compile
    echo "== proc $r" >> $M/logs/$tag-$t.log
    GOMAXPROCS=1 taskset -c ${CPU:-17} $M/bin-$t-compile.test -test.run '^$' \
      -test.bench '^Benchmark(Prepare|Build|BuildScopes)$' -test.benchtime 300ms -test.count 5 \
      -test.benchmem -test.timeout 2h >> $M/logs/$tag-$t.log 2>&1
  done
done
echo "$tag done" >> $M/logs/bench.progress
