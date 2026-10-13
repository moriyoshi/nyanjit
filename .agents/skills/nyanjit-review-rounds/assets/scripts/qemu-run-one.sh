#!/bin/sh
# usage: R=<dir containing post/ and xbin/> [LDP=/opt/nyanjit/sysroots/<arch>] [SWEEP=1] [RETAINED=1] \
#        qemu-run-one.sh <label> <image> <qemu-binary> <xbin-cfg> <QEMU_CPU> <cpuset>
# Runs every cross-built package test binary in xbin/<cfg>/ under qemu-user
# inside an existing e2e image. No --privileged: qemu is invoked directly with
# an absolute argv0, so host binfmt is neither needed nor touched.
# Images: nyanjit-e2e:linux-gha (386 amd64 riscv64 ppc64le), nyanjit-e2e:loong64-gc-full (loong64).
: ${R:?}; label=$1; img=$2; q=$3; cfg=$4; cpu=$5; cpus=$6
mkdir -p $R/qlogs; ldarg=""; [ -n "$LDP" ] && ldarg="-e QEMU_LD_PREFIX=$LDP"
dr() { wd=$1; shift; ea=$1; shift
  docker run --rm --cpuset-cpus "$cpus" --entrypoint sh -v $R:/w:ro -w $wd -e GOMAXPROCS=2 -e QEMU_CPU=$cpu $ldarg $ea $img -c "$*"; }
for bin in $R/xbin/$cfg/*.test; do
  n=$(basename $bin .test); p=$(echo $n | tr _ /); [ $n = root ] && p=.
  [ -d $R/post/$p ] || p=$(echo $n | sed "s,_,/,")
  B=/w/xbin/$cfg/$n.test
  dr /w/post/$p "" "/usr/bin/$q $B $B -test.count=1 -test.timeout 90m -test.v" > $R/qlogs/$label-$n.log 2>&1
  echo "$label $n rc=$?" >> $R/qsummary.txt
  if [ $n = compile_varfuzz ] && [ "$SWEEP" = 1 ]; then
    dr /w/post/$p "-e NYANJIT_VAR_SWEEP=500" "/usr/bin/$q $B $B -test.run '^TestScopedVarsSweep\$' -test.timeout 60m" > $R/qlogs/$label-sweep.log 2>&1
    echo "$label sweep rc=$?" >> $R/qsummary.txt
  fi
  if [ $n = compile ] && [ "$RETAINED" = 1 ]; then
    dr /w/post/$p "-e NYANJIT_PERF_RETAINED=1" "/usr/bin/$q $B $B -test.run '^TestScratchTotalBudget' -test.count=1" > $R/qlogs/$label-retained.log 2>&1
    echo "$label retained rc=$?" >> $R/qsummary.txt
  fi
done
echo "$label CFGDONE" >> $R/qsummary.txt
