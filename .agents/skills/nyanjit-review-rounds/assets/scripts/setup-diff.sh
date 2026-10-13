#!/bin/sh
# usage: setup-diff.sh <workdir> <pre-tree> <post-tree>
# Copies both trees into <workdir>/{pre,post}, drops the reviewer harness into
# compile/varfuzz of each, and builds the test binaries into <workdir>/bin:
#   pre-varfuzz.test post-varfuzz.test poison-varfuzz.test (+ -race variants)
# The harness must be identical in pre and post; adapt it only where an API
# change forces it, and make the same edit on both sides.
set -e
D=$1; PRE=$2; POST=$3
H=$(cd "$(dirname "$0")/../harness" && pwd)
mkdir -p $D/bin $D/out
for t in pre post; do
  src=$PRE; [ $t = post ] && src=$POST
  rm -rf $D/$t; cp -a $src $D/$t
  cp $H/*_test.go $D/$t/compile/varfuzz/
done
export GOCACHE=$D/gocache GOTOOLCHAIN=local GOFLAGS=-mod=mod
(cd $D/pre/compile/varfuzz && go test -c -o $D/bin/pre-varfuzz.test .)
(cd $D/post/compile/varfuzz && go test -c -o $D/bin/post-varfuzz.test . \
  && go test -c -tags nyanjit_scratch_poison -o $D/bin/poison-varfuzz.test . \
  && go test -c -race -o $D/bin/post-varfuzz-race.test . \
  && go test -c -race -tags nyanjit_scratch_poison -o $D/bin/poison-varfuzz-race.test .)
echo SETUPDONE
