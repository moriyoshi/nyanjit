package compile_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/compile"
)

func Test386LiteralPoolRebinding(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
				t.Run(fmt.Sprintf("%d/%d/%d", w, stack, flavor), func(t *testing.T) {
					b := n.NewBuilder()
					x := b.ParamBits(w)
					c := fpConstant(b, w, 1.25)
					b.Return(b.FAdd(b.FMul(x, c), c))
					p := finishCrossAssembly(t, b)
					fn := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack}}
					if stack == abi.StackPlatform {
						fn.Stack.BytesAvailable = 8192
					}
					external := fn
					external.Context = compile.ContextExternal
					emission, e := lowerTemplate(p, compile.TargetSpec{}, external)
					if e != nil {
						t.Fatal(e)
					}
					unbased, e := emission.LinkAt(nil, 0)
					if e != nil {
						t.Fatal(e)
					}
					original := bytes.Clone(unbased)
					var local []machine.Relocation
					for _, r := range emission.Relocations() {
						if r.NeedsCodeBase() {
							local = append(local, r)
						}
					}
					if len(local) != 1 || local[0].TargetOffset%16 != 0 {
						t.Fatalf("literal not shared and aligned: %+v", local)
					}
					if _, e := emission.Bytes(nil); !errors.Is(e, compile.ErrCodeBaseRequired) {
						t.Fatalf("unbased link: %v", e)
					}
					raw, e := lowerTemplate(p, compile.TargetSpec{}, external)
					if e != nil {
						t.Fatal(e)
					}
					if _, e := raw.Bytes(nil); !errors.Is(e, compile.ErrCodeBaseRequired) {
						t.Fatalf("raw emission: %v", e)
					}
					linked, e := emission.LinkAt(nil, 0x10000)
					if e != nil {
						t.Fatal(e)
					}
					r := local[0]
					if binary.LittleEndian.Uint32(linked[r.Offset+4:]) != uint32(0x10000+r.TargetOffset) {
						t.Fatal("LinkAt lost pool offset")
					}
					// A linked copy must not alias the template's code.
					clear(linked)
					tmpl, e := lowerTemplate(p, compile.TargetSpec{}, fn)
					if e != nil {
						t.Fatal(e)
					}
					a, e := tmpl.Bind(nil)
					if e != nil {
						t.Fatal(e)
					}
					defer a.Close()
					d, e := tmpl.Bind(nil)
					if e != nil {
						t.Fatal(e)
					}
					defer d.Close()
					for _, code := range []*execution.Code{a, d} {
						for _, x := range []float64{-3, 0, 2, 1.5} {
							got, e := callMathBits(code, w, fpBits(w, x))
							if e != nil || got != fpBits(w, x*1.25+1.25) {
								t.Fatalf("x%v got%#x %v", x, got, e)
							}
						}
					}
					after, e := emission.LinkAt(nil, 0)
					if e != nil || !bytes.Equal(after, original) {
						t.Fatal("link patched the source emission")
					}
				})
			}
		}
	}
}
func Test386LiteralPoolAndExternalSymbol(t *testing.T) {
	b := n.NewBuilder()
	b.Return(b.FAdd(b.Symbol("bits"), b.Float64(1.25)))
	p := finishCrossAssembly(t, b)
	tmpl, e := lowerTemplate(p, compile.TargetSpec{}, compile.Func{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := tmpl.Bind(nil); !errors.Is(e, compile.ErrUnboundSymbol) {
		t.Fatalf("missing external: %v", e)
	}
	for _, x := range []float64{1.5, -7} {
		code, e := tmpl.Bind(map[string]uint64{"bits": fpBits(64, x)})
		if e != nil {
			t.Fatal(e)
		}
		got, e := callMathBits(code, 64)
		code.Close()
		if e != nil || got != fpBits(64, x+1.25) {
			t.Fatalf("bound symbol %v got%#x %v", x, got, e)
		}
	}
}
