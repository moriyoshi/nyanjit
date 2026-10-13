//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestStreamAppendsSegmentFinalizers gives each segment of a two-block
// function its own Finalizer: the Unit's Codes run them all, last first.
func TestStreamAppendsSegmentFinalizers(t *testing.T) {
	p := finished(t, func(b *nyanjit.Builder) {
		x := b.ParamNative()
		done := b.NewLabel()
		b.Jump(done)
		b.Bind(done)
		b.Return(b.Add(x, b.ConstNative(1)))
	})
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		t.Run(fmt.Sprint(tier), func(t *testing.T) {
			target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			s, err := compile.NewStream(target, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			var order []int
			segments := 0
			err = compile.ProgramSegments(sealedView(p), func(seg *ir.Segment) error {
				id := segments
				segments++
				seg.Finalizers = backend.Finalizers{backend.FinalizerFunc(func() { order = append(order, id) })}
				return s.Segment(seg)
			})
			if err != nil || segments != 2 {
				t.Fatalf("%d segments, %v", segments, err)
			}
			unit, err := s.Unit()
			if err != nil {
				t.Fatal(err)
			}
			template, err := unit.Template()
			if err != nil {
				t.Fatal(err)
			}
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := code.Call(41); err != nil || got != 42 {
				t.Fatalf("got %d, %v", got, err)
			}
			_ = code.Close()
			if !slices.Equal(order, []int{1, 0}) {
				t.Fatalf("Finalizers ran %v", order)
			}
			if _, err := template.Bind(nil); !errors.Is(err, execution.ErrClosed) {
				t.Fatalf("Bind after Finalizers: %v", err)
			}
		})
	}
}

// TestLowerSharesProgramFinalizers lowers one raw program, with vectors at
// the baseline tier and with Constants, and checks that every Code shares
// one count.
func TestLowerSharesProgramFinalizers(t *testing.T) {
	sealed := finished(t, func(b *nyanjit.Builder) {
		x := b.ParamNative()
		k := b.Symbol("k")
		v := b.Splat(ir.Vec(4, 32), b.CastUnsigned(x, 32))
		lane := b.ExtractLane(b.Add(v, v), 1)
		b.Return(b.CastNative(b.Add(b.CastUnsigned(lane, 64), k)))
	})
	runs := 0
	program := sealed.Clone()
	program.Finalizers = backend.Finalizers{backend.FinalizerFunc(func() { runs++ })}
	f := compile.Func{ABI: abi.C, Constants: map[string]uint64{"k": 100}}
	var codes []*execution.Code
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []ir.Input{program, program.Clone()} {
			unit, err := compile.Lower(target, input, f)
			if err != nil {
				t.Fatal(err)
			}
			template, err := unit.Template()
			if err != nil {
				t.Fatal(err)
			}
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			codes = append(codes, code)
		}
	}
	for _, code := range codes {
		if got, err := code.Call(5); err != nil || got != 110 {
			t.Fatalf("got %d, %v", got, err)
		}
		if runs != 0 {
			t.Fatal("Finalizers ran with Codes open")
		}
		_ = code.Close()
	}
	if runs != 1 {
		t.Fatalf("Finalizers ran %d times", runs)
	}
}
