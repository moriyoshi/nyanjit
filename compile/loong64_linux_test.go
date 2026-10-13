//go:build linux && loong64

package compile_test

import (
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"

	"math/bits"
	"math/rand/v2"
	"testing"
)

func TestLoong64WideArithmetic(t *testing.T) {
	rng := rand.New(rand.NewPCG(27, 64))
	for _, op := range []string{"add", "sub", "mul"} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			b := n.NewBuilder()
			x, y := b.ParamBits(128), b.ParamBits(128)
			r := b.Add(x, y)
			if op == "sub" {
				r = b.Sub(x, y)
			}
			if op == "mul" {
				r = b.Mul(x, y)
			}
			b.Return(r)
			p, e := testFinishRaw(b)
			if e != nil {
				t.Fatal(e)
			}
			fn := compile.Func{Stack: compile.StackPolicy{Practice: stack}}
			if stack == abi.StackPlatform {
				fn.Stack.BytesAvailable = 8192
			}
			c, e := n.Compile(p, compile.TargetSpec{}, fn)
			if e != nil {
				t.Fatal(e)
			}
			for k := 0; k < 256; k++ {
				a, b, x, y := rng.Uint64(), rng.Uint64(), rng.Uint64(), rng.Uint64()
				if k == 0 {
					a, b, x, y = ^uint64(0), ^uint64(0), 1, 0
				}
				var lo, hi, carry uint64
				switch op {
				case "add":
					lo, carry = bits.Add64(a, x, 0)
					hi, _ = bits.Add64(b, y, carry)
				case "sub":
					lo, carry = bits.Sub64(a, x, 0)
					hi, _ = bits.Sub64(b, y, carry)
				case "mul":
					hi, lo = bits.Mul64(a, x)
					hi += a*y + b*x
				}
				l, h, e := c.CallPair(uintptr(a), uintptr(b), uintptr(x), uintptr(y))
				if e != nil || uint64(l) != lo || uint64(h) != hi {
					t.Fatalf("%s stack%d got%#x:%#x want%#x:%#x err%v", op, stack, h, l, hi, lo, e)
				}
			}
			c.Close()
		}
	}
}
func TestLoong64LeafGoFunc(t *testing.T) {
	b := n.NewBuilder()
	a, c := b.ParamBits(64), b.ParamBits(32)
	b.Return(b.Add(a, b.CastUnsigned(c, 64)))
	p, e := testFinishRaw(b)
	if e != nil {
		t.Fatal(e)
	}
	code, e := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	if e != nil {
		t.Fatal(e)
	}
	defer code.Close()
	var f func(uint64, uint32) uint64
	e = code.Func(&f)
	if e != nil {
		t.Fatal(e)
	}
	if got := f(0x8000000000000000, 42); got != 0x800000000000002a {
		t.Fatalf("got%#x", got)
	}
}
