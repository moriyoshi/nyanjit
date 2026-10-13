//go:build linux && arm64

package varfuzz

// Native report check (reviewer-owned, identical in pre and post): default
// target (auto vectors, zero CPU) on the host; full %+v reports from
// Unit.Report and an external-context template's Bytes (the old Analyze and
// Emit), from Template in the native, Go-managed and foreign contexts (the
// old Prepare/PrepareGo/PrepareForeign; tcode = unlinked template bytes) and
// bound code bytes (only without calls, since gateway addresses differ per
// binary).
// ZZNAT_OUT=file ZZNAT_FROM=a ZZNAT_TO=b

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

func zzNatHash(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:8]) }

func TestZZNative(t *testing.T) {
	out := os.Getenv("ZZNAT_OUT")
	if out == "" {
		t.Skip()
	}
	from, to := zzEnv("ZZNAT_FROM", 0), zzEnv("ZZNAT_TO", 10)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	ext := func(spec compile.TargetSpec, f compile.Func) zzOpt {
		f.Context = compile.ContextExternal
		return zzOpt{spec, f}
	}
	platStack := compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}
	cfgs := []zzCfg{
		{"zero", ext(compile.TargetSpec{}, compile.Func{})},
		{"C/Plat", ext(compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: platStack, AssumePointerFree: true})},
		{"Go/Plat", ext(compile.TargetSpec{}, compile.Func{ABI: abi.Go, Stack: platStack, AssumePointerFree: true})},
		{"C/NoUse", ext(compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})},
		{"Go/NoUse", ext(compile.TargetSpec{}, compile.Func{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})},
		{"arm64/C/Plat", ext(compile.TargetSpec{Architecture: "arm64"}, compile.Func{ABI: abi.C, Stack: platStack})},
	}
	for _, gen := range []string{"rand", "varfuzz", "scope", "work"} {
		for seed := from; seed < to; seed++ {
			b, err := zzBuild(gen, seed)
			if err != nil {
				continue
			}
			prog, err := b.Finish()
			if err != nil {
				continue
			}
			for _, c := range cfgs {
				key := fmt.Sprintf("%s %d %s", gen, seed, c.name)
				line := func() (s string) {
					defer func() {
						if r := recover(); r != nil {
							s = fmt.Sprintf("PANIC %v %s", r, zzFirst(string(debug.Stack())))
						}
					}()
					s = ""
					an, aerr := zzReport(prog, c.opt)
					s += fmt.Sprintf(" analyze[err=%v rep=%+v]", aerr, an)
					code, rep, eerr := zzEmit(prog, c.opt)
					s += fmt.Sprintf(" emit[err=%v code=%s/%d rep=%+v]", eerr, zzNatHash(code), len(code), rep)
					type zzTp struct {
						n  string
						tp *execution.Template
						e  error
					}
					tps := []zzTp{}
					for _, x := range []struct {
						n       string
						context compile.Context
					}{{"prepare", compile.ContextNative}, {"prepareGo", compile.ContextGoManaged}, {"prepareForeign", compile.ContextForeign}} {
						tp, e := zzTemplate(prog, c.opt, x.context)
						tps = append(tps, zzTp{x.n, tp, e})
					}
					for _, x := range tps {
						if x.e != nil {
							s += fmt.Sprintf(" %s[err=%v]", x.n, x.e)
							continue
						}
						if dk := os.Getenv("ZZNAT_DUMP"); dk != "" && dk == key+" "+x.n {
							os.WriteFile(os.Getenv("ZZNAT_DUMPFILE"), reflect.ValueOf(x.tp).Elem().FieldByName("code").Bytes(), 0644)
						}
						r := x.tp.Report().Snapshot()
						rel := x.tp.Relocations()
						cb := false
						for _, rr := range rel {
							if rr.NeedsCodeBase() {
								cb = true
							}
						}
						s += fmt.Sprintf(" %s[rep=%+v relocs=%+v tcode=%s", x.n, r, rel, zzNatMasked(reflect.ValueOf(x.tp).Elem().FieldByName("code").Bytes()))
						code, berr := x.tp.Bind(nil)
						if berr != nil {
							s += fmt.Sprintf(" bind=%v]", berr)
							continue
						}
						ent, _ := code.EntryAddress()
						if !cb && !r.HasFunctionCalls && r.CodeBytes > 0 {
							s += " bytes=" + zzNatHash(unsafe.Slice((*byte)(unsafe.Pointer(ent)), r.CodeBytes))
						}
						code.Close()
						s += "]"
					}
					return s
				}()
				fmt.Fprintln(w, key+line)
			}
		}
	}
}

// zzNatMasked hashes arm64 code after replacing MOVZ/MOVK sequences that
// materialize the entry of a Go function in this test binary (runtime bridge
// addresses differ per binary) with zero words; the function names are kept.
func zzNatMasked(code []byte) string {
	c := append([]byte(nil), code...)
	var names []string
	nw := len(c) / 4
	for i := 0; i < nw; i++ {
		w := binary.LittleEndian.Uint32(c[4*i:])
		if w&0xff800000 != 0xd2800000 {
			continue
		}
		rd := w & 31
		v := uint64((w>>5)&0xffff) << (16 * ((w >> 21) & 3))
		j := i + 1
		for ; j < nw; j++ {
			k := binary.LittleEndian.Uint32(c[4*j:])
			if k&0xff800000 != 0xf2800000 || k&31 != rd {
				break
			}
			sh := 16 * ((k >> 21) & 3)
			v = v&^(0xffff<<sh) | uint64((k>>5)&0xffff)<<sh
		}
		if v < 0x10000 {
			continue
		}
		f := runtime.FuncForPC(uintptr(v))
		if f == nil || f.Entry() != uintptr(v) {
			continue
		}
		names = append(names, f.Name())
		for x := i; x < j; x++ {
			binary.LittleEndian.PutUint32(c[4*x:], 0)
		}
	}
	return zzNatHash(c) + fmt.Sprint(names)
}
