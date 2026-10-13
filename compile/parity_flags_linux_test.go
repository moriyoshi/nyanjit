//go:build linux && (arm64 || amd64)

package compile_test

import (
	"fmt"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"runtime"
	"strings"
	"testing"
)

func TestNativeArithmeticPredicateFlags(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		for _, sub := range []bool{false, true} {
			for _, carry := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/sub%v/carry%v", width, sub, carry), func(t *testing.T) {
					b := nyanjit.NewBuilder()
					x, y := b.ParamBits(width), b.ParamBits(width)
					var result nyanjit.Value
					if sub {
						result = b.Sub(x, y)
					} else {
						result = b.Add(x, y)
					}
					pred := b.CompareImmediate(result, nyanjit.BranchEqual, 0)
					if carry {
						pred = b.Carry(result)
					}
					yes, no := b.NewLabel(), b.NewLabel()
					b.Branch(pred, yes, no)
					b.Bind(yes)
					b.Return(b.Const(1))
					b.Bind(no)
					b.Return(b.Const(0))
					p := finishCrossAssembly(t, b)
					options := abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 1024}
					asm, err := compile.EmitAssembly(p, options)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(asm, "  cmp ") {
						t.Fatalf("arithmetic predicate repeated comparison on %s:\n%s", runtime.GOARCH, asm)
					}
					code, err := compile.Compile(p, options)
					if err != nil {
						t.Fatal(err)
					}
					defer code.Close()
					mask := ^uint64(0) >> (64 - width)
					for _, args := range [][2]uint64{{0, 0}, {1, 1}, {0, 1}, {mask, 1}, {mask, mask}, {1, mask}} {
						a, c := args[0]&mask, args[1]&mask
						r := a + c
						if sub {
							r = a - c
						}
						r &= mask
						match := r == 0
						if carry {
							if sub {
								match = a < c
							} else {
								match = r < a
							}
						}
						want := uint64(0)
						if match {
							want = 1
						}
						got, err := code.Call(uintptr(args[0]), uintptr(args[1]))
						if err != nil || uint64(got) != want {
							t.Fatalf("%v: %d,%v want%d\n%s", args, got, err, want, asm)
						}
					}
				})
			}
		}
	}
}

func TestAMD64CarrySurvivesDecrement(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("INC/DEC preserves CF on x86")
	}
	b := nyanjit.NewBuilder()
	x, y, z := b.Param(), b.Param(), b.Param()
	sum := b.Add(x, y)
	next := b.Sub(z, b.Const(1))
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.Carry(sum).And(b.IsZero(next)), yes, no)
	b.Bind(yes)
	b.Return(b.Const(1))
	b.Bind(no)
	b.Return(b.Const(0))
	p := finishCrossAssembly(t, b)
	o := abi.CompileOptions{}
	asm, err := compile.EmitAssembly(p, o)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(asm, "  cmp ") {
		t.Fatalf("CF/SZ were not reused independently:\n%s", asm)
	}
	code, err := compile.Compile(p, o)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, v := range [][3]uint64{{^uint64(0), 1, 1}, {^uint64(0), 1, 2}, {1, 1, 1}, {0, 0, 1}} {
		want := uint64(0)
		if v[0]+v[1] < v[0] && v[2] == 1 {
			want = 1
		}
		got, err := code.Call(uintptr(v[0]), uintptr(v[1]), uintptr(v[2]))
		if err != nil || uint64(got) != want {
			t.Fatalf("%v: %d,%v want%d\n%s", v, got, err, want, asm)
		}
	}
}

func TestSignedArithmeticFlagsAcrossWidening(t *testing.T) {
	for _, width := range []uint8{8, 16, 32} {
		b := nyanjit.NewBuilder()
		x, y := b.ParamBits(width), b.ParamBits(width)
		sum := b.Add(x, y)
		yes, no := b.NewLabel(), b.NewLabel()
		b.Branch(b.CompareImmediate(sum, nyanjit.BranchLess, 0), yes, no)
		b.Bind(yes)
		b.Return(b.Const(1))
		b.Bind(no)
		b.Return(b.Const(0))
		p := finishCrossAssembly(t, b)
		asm, err := compile.EmitAssembly(p, abi.CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if (runtime.GOARCH == "amd64" || width == 32) && strings.Contains(asm, "  cmp ") {
			t.Fatalf("width%d signed widening lost SF:\n%s", width, asm)
		}
		code, err := compile.Compile(p, abi.CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		mask := ^uint64(0) >> (64 - width)
		for _, v := range [][2]uint64{{0, 0}, {mask, 1}, {1 << (width - 1), 0}, {(1 << (width - 1)) - 1, 1}, {mask, 0}} {
			want := uint64(0)
			if (v[0]+v[1])&mask&(1<<(width-1)) != 0 {
				want = 1
			}
			got, err := code.Call(uintptr(v[0]), uintptr(v[1]))
			if err != nil || uint64(got) != want {
				t.Fatalf("width%d %v: %d,%v want%d\n%s", width, v, got, err, want, asm)
			}
		}
		code.Close()
	}
}

func TestSignedFlagAliasDoesNotConfuseUnsignedWidening(t *testing.T) {
	b := nyanjit.NewBuilder()
	x, y := b.ParamBits(32), b.ParamBits(32)
	sum := b.Add(x, y)
	// Keep both widenings in the block: only the signed one has the producer's SF.
	signed := b.SignExtend(sum, 32)
	unsigned := b.CastUnsigned(sum, 64)
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.CompareImmediate(signed, nyanjit.BranchLess, 0).And(b.CompareImmediate(unsigned, nyanjit.BranchLess, 0)), yes, no)
	b.Bind(yes)
	b.Return(b.Const(1))
	b.Bind(no)
	b.Return(b.Const(0))
	p := finishCrossAssembly(t, b)
	code, err := compile.Compile(p, abi.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, x := range []uint64{0, 1, 0x7fffffff, 0x80000000, 0xffffffff} {
		got, err := code.Call(uintptr(x), 0)
		if err != nil || got != 0 {
			t.Fatalf("unsigned widening of %#x classified negative: %d,%v", x, got, err)
		}
	}
}
