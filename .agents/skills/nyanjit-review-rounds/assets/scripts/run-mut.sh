#!/bin/sh
# usage: D=<workdir> [CPUS=0-9] run-mut.sh <seed-base> [n-per-generator]
# Raw-IR mutation runs, pre vs post (and poison for zzmut9). Summarize with:
#   cd $D/out && python3 -I <skill>/assets/scripts/mutsum.py mut rand scope varfuzz work pscope
: ${D:?}; B=${1:-1640000000}; N=${2:-3000}
cd $D/post/compile/varfuzz
for g in rand scope varfuzz work pscope; do
  for t in pre post; do
    ZZMUT_OUT=$D/out/mut-$g-$t.txt ZZMUT_GEN=$g ZZMUT_FROM=$B ZZMUT_TO=$((B+N)) taskset -c ${CPUS:-0-9} $D/bin/$t-varfuzz.test -test.run 'TestZZMut$' -test.timeout 5h > $D/out/mut-$g-$t.log 2>&1
    ZZMA_OUT=$D/out/ma-$g-$t.txt ZZMA_GEN=$g ZZMA_FROM=$B ZZMA_TO=$((B+N)) taskset -c ${CPUS:-0-9} $D/bin/$t-varfuzz.test -test.run TestZZMutArgs -test.timeout 5h > $D/out/ma-$g-$t.log 2>&1
  done
  for t in pre post poison; do
    ZZM9_OUT=$D/out/m9-$g-$t.txt ZZM9_GEN=$g ZZM9_FROM=$B ZZM9_TO=$((B+N/3)) taskset -c ${CPUS:-0-9} $D/bin/$t-varfuzz.test -test.run 'TestZZMut9$' -test.v -test.timeout 8h > $D/out/m9-$g-$t.log 2>&1
    echo "m9 $g $t $(tail -1 $D/out/m9-$g-$t.log)" >> $D/out/mut-summary.txt
  done
done
echo MUTDONE >> $D/out/mut-summary.txt
