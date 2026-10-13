#!/bin/sh
# usage: D=<workdir> [CPUS=0-9] run-diff.sh <gen> <from> <to> <tag> [wide] [post|poison]
#   gen: work | rand | varfuzz | scope | pscope ; wide: non-empty = all 19 configs per arch
# Writes out/<tag>-{pre,post}.txt, a (pins)-normalized diff and a strict diff
# that ignores error text, and appends one line to out/summary.txt.
: ${D:?set D to the work dir}
PB=${6:-post}
for t in pre $PB; do
  (cd $D/$t/compile/varfuzz 2>/dev/null || cd $D/post/compile/varfuzz
   ZZDIFF_OUT=$D/out/$4-$t.txt ZZDIFF_GEN=$1 ZZDIFF_FROM=$2 ZZDIFF_TO=$3 ZZDIFF_WIDE=$5 \
     taskset -c ${CPUS:-0-9} $D/bin/$t-varfuzz.test -test.run TestZZDiff -test.timeout 5h 2>&1 | tail -1)
done
sed -E 's/\(pins [^)]*\)/(pins)/' $D/out/$4-pre.txt > $D/out/$4-pre.norm
sed -E 's/\(pins [^)]*\)/(pins)/' $D/out/$4-$PB.txt > $D/out/$4-$PB.norm
diff $D/out/$4-pre.norm $D/out/$4-$PB.norm > $D/out/$4.diff
sed -E 's/ ERR .*/ ERR/' $D/out/$4-pre.txt > $D/out/$4-pre.strict
sed -E 's/ ERR .*/ ERR/' $D/out/$4-$PB.txt > $D/out/$4-$PB.strict
diff $D/out/$4-pre.strict $D/out/$4-$PB.strict > $D/out/$4.strictdiff
echo "$4 [$PB]: lines=$(wc -l < $D/out/$4-pre.txt) ok=$(grep -c ' OK ' $D/out/$4-pre.txt) err=$(grep -c ' ERR ' $D/out/$4-pre.txt) normdifflines=$(grep -c '^<' $D/out/$4.diff) strictdifflines=$(grep -c '^<' $D/out/$4.strictdiff)" | tee -a $D/out/summary.txt
