#!/bin/sh
# usage: D=<workdir> [CPUS=0-9] run-conc.sh <seed-base>
# Serial vs 16-goroutine listings, normal/poison/-race/poison+race.
# Panics are recorded by message only, so goroutine numbers don't show up as mismatches.
: ${D:?}; B=${1:-1620000000}
cd $D/post/compile/varfuzz
r() { log=$1; bin=$2; shift 2; env ZZCONC=1 "$@" taskset -c ${CPUS:-0-9} $D/bin/$bin -test.run TestZZConc -test.v -test.timeout 5h > $D/out/$log 2>&1
      echo "$log: $(grep -E 'comparisons|DATA RACE|FAIL|PASS|^ok' $D/out/$log | sort | uniq -c | tr '\n' ' ')" >> $D/out/conc-summary.txt; }
r conc-post.log post-varfuzz.test ZZCONC_N=4000 ZZCONC_SEED=$B
r conc-poison.log poison-varfuzz.test ZZCONC_N=4000 ZZCONC_SEED=$((B+1000000))
r conc-post-race.log post-varfuzz-race.test ZZCONC_N=400 ZZCONC_ROUNDS=2 ZZCONC_SEED=$((B+2000000))
r conc-poison-race.log poison-varfuzz-race.test ZZCONC_N=400 ZZCONC_ROUNDS=2 ZZCONC_SEED=$((B+3000000))
echo CONCDONE >> $D/out/conc-summary.txt
