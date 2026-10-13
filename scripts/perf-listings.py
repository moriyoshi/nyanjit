#!/usr/bin/env python3
"""Capture immutable compiler inputs/listings with a temporary Go overlay.

Usage: perf-listings.py SOURCE_ROOT CORPUS_DIR [PACKAGE ...]
The source tree and production compiler are never instrumented on disk. Replay:
NYANJIT_LISTING_CORPUS=CORPUS_DIR go test ./compile -run TestPerformanceListingCorpus
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

root = Path(sys.argv[1]).resolve()
out = Path(sys.argv[2]).resolve()
out.mkdir(parents=True, exist_ok=True)
helper = r'''package compile
import (
 "compress/gzip"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "os"
 "path/filepath"
 "runtime"
 "sync"
 "github.com/moriyoshi/nyanjit/backend/abi"
 "github.com/moriyoshi/nyanjit/ir"
)
var perfListingSeen = struct{sync.Mutex; keys map[[32]byte]bool}{keys:make(map[[32]byte]bool)}
func perfCapture(input ir.Input,o abi.CompileOptions,mode string) {
 if input==nil {return}
 p:=input.Clone();if p==nil {return}
 // Deep predicates have separate stack-capped acceptance tests. JSON's recursive
 // encoder must not become part of those tests through this temporary overlay.
 if p.Control!=nil {
  type frame struct{p *ir.PredicateExpr; depth int}
  work:=[]frame{};count:=0
  for _,s:=range p.Control.Steps {if s.Predicate!=nil {work=append(work,frame{s.Predicate,1})}}
  for len(work)>0 {
   f:=work[len(work)-1];work=work[:len(work)-1];if f.p==nil {continue};count++
   if f.depth>128 || count>10000 {return}
   work=append(work,frame{f.p.Left,f.depth+1},frame{f.p.Right,f.depth+1})
  }
 }
 if mode=="PrepareGo" {o.ManagedCalls=true}
 if o.Architecture=="" {o.Architecture=runtime.GOARCH}
 fixtureInput:=struct{Program *ir.Program; Options abi.CompileOptions}{p,o}
 encoded,err:=json.Marshal(fixtureInput);if err!=nil {return}
 key:=sha256.Sum256(encoded)
 perfListingSeen.Lock();seen:=perfListingSeen.keys[key];perfListingSeen.keys[key]=true;perfListingSeen.Unlock()
 if seen {return}
 listing,err:=emitProgramAssemblyFromIR(p,o);if err!=nil {return}
 digest:=sha256.Sum256([]byte(listing))
 fixture:=struct{Program *ir.Program;Options abi.CompileOptions;Listing string;SHA256 string}{p,o,listing,hex.EncodeToString(digest[:])}
 directory:=os.Getenv("NYANJIT_LISTING_CORPUS")
 if directory=="" {panic("missing listing capture directory")}
 name:=hex.EncodeToString(key[:])+".json.gz"
 if _,err:=os.Stat(filepath.Join(directory,name));err==nil {return}
 f,err:=os.CreateTemp(directory,"capture-*.tmp");if err!=nil {panic(err)}
 defer os.Remove(f.Name())
 gz:=gzip.NewWriter(f);err=json.NewEncoder(gz).Encode(fixture)
 if err==nil {err=gz.Close()};if e:=f.Close();err==nil {err=e};if err!=nil {panic(err)}
 if err=os.Rename(f.Name(),filepath.Join(directory,name));err!=nil {panic(err)}
}
'''
with tempfile.TemporaryDirectory(prefix="capture-overlay-", dir=out) as temp:
    temp = Path(temp)
    source = (root / "compile/compile.go").read_text()
    pattern = r'(func (Analyze|EmitAssembly|Prepare|PrepareGo|PrepareForeign|EmitTemplate)\(program (?:\*ir.Program|ir.Input), options abi.CompileOptions\)[^{]+\{)'
    source, count = re.subn(pattern, lambda m: m[1] + '\nperfCapture(program,options,"' + m[2] + '")', source)
    if count != 6:
        raise SystemExit(f"Unexpected public wrapper layout: instrumented {count} functions")
    (temp / "compile.go").write_text(source)
    (temp / "capture.go").write_text(helper)
    replacements = {
        str(root / "compile/compile.go"): str(temp / "compile.go"),
        str(root / "compile/zz_perf_listing_capture.go"): str(temp / "capture.go"),
    }
    (temp / "overlay.json").write_text(json.dumps({"Replace": replacements}))
    env = os.environ.copy()
    env["NYANJIT_LISTING_CORPUS"] = str(out)
    command = ["go", "test", "-overlay", str(temp / "overlay.json"), *(sys.argv[3:] or ["./..."]), "-count=1", "-timeout=15m"]
    print("Capturing:", " ".join(command), flush=True)
    subprocess.run(command, cwd=root, env=env, check=True)
print("Captured", len(list(out.glob("*.json.gz"))), "unique successful compiler inputs")
