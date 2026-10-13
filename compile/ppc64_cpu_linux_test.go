//go:build linux && ppc64le

package compile_test

import (
	"errors"
	"os"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestPPC64NativeCPUGatingAndMultiplyAdd(t *testing.T) {
	host, err := ppc64.HostCPU()
	if err != nil {
		t.Fatal(err)
	}
	if expected := os.Getenv("QEMU_CPU"); expected == "power8" || expected == "power9" || expected == "power10" {
		if host.String() != expected {
			t.Fatalf("HostCPU()=%s; emulator is %s", host, expected)
		}
	}
	for _, level := range []abi.PPC64CPU{0, 8, 9, 10} {
		for _, width := range []uint8{8, 16, 32, 64} {
			mask := ^uint64(0) >> (64 - width)
			for _, reverse := range []bool{false, true} {
				for _, shared := range []bool{false, true} {
					program := multiplyAddProgram(t, width, reverse, shared, false)
					template, err := lowerTemplate(program, ppc64LevelSpec("", level), compile.Func{})
					if level > host {
						if !errors.Is(err, compile.ErrBackendLimit) {
							t.Fatalf("CPU %s on %s: %v; want ErrBackendLimit", level, host, err)
						}
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					resolved, _ := level.Resolve()
					if template.Report().PPC64CPU() != resolved {
						t.Fatalf("reported CPU=%s; want %s", template.Report().PPC64CPU(), resolved)
					}
					code, err := template.Bind(nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, args := range [][3]uint64{{6, 5, 12}, {0, 0, 1}, {^uint64(0), ^uint64(0), ^uint64(0)}, {0x8000000000000001, 0x123456789abcdef, 0xfedcba9876543210}} {
						product := (args[0] & mask) * (args[1] & mask) & mask
						want := (product + (args[2] & mask)) & mask
						if shared {
							want ^= product
						}
						got, callErr := code.Call(uintptr(args[0]), uintptr(args[1]), uintptr(args[2]))
						if callErr != nil || uint64(got) != want {
							t.Fatalf("CPU %s width %d reverse=%t shared=%t args=%#v: %#x,%v; want %#x", level, width, reverse, shared, args, got, callErr, want)
						}
					}
					if err := code.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}

func TestPPC64NativeMinimumLevelBothOptions(t *testing.T) {
	host, err := ppc64.HostCPU()
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilder()
	b.Return(b.Const(42))
	p := finishCrossAssembly(t, b)
	for _, level := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
		spec := compile.TargetSpec{CPU: abi.CPUProfile{Level: level.String()}}
		_, err := lowerTemplate(p, spec, compile.Func{})
		if level > host {
			if !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("host%s request%+v: %v; want ErrBackendLimit", host, spec, err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPPC64MultiplyAddInControlFlowReportsCPU(t *testing.T) {
	host, err := ppc64.HostCPU()
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilder()
	x, y, z := b.Param(), b.Param(), b.Param()
	yes, no := b.NewLabel(), b.NewLabel()
	result := b.Add(b.Mul(x, y), z)
	b.Branch(b.CompareImmediate(result, ir.BranchEqual, 42), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.Const(1))
	b.Bind(no)
	b.Return(b.Const(0))
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(program, ppc64LevelSpec("", host), compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}})
	if err != nil {
		t.Fatal(err)
	}
	if got := template.Report().PPC64CPU(); got != host {
		t.Fatalf("control-flow report CPU=%s; want %s", got, host)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, args := range [][3]uint64{{6, 5, 12}, {6, 5, 11}, {^uint64(0), 1, 43}, {0, 7, 42}} {
		want := uint64(0)
		if args[0]*args[1]+args[2] == 42 {
			want = 1
		}
		got, err := code.Call(uintptr(args[0]), uintptr(args[1]), uintptr(args[2]))
		if err != nil || uint64(got) != want {
			t.Fatalf("%#v: %d,%v; want %d", args, got, err, want)
		}
	}
}

func TestPPC64NativeNarrowScaledArithmetic(t *testing.T) {
	host, err := ppc64.HostCPU()
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
		if level > host {
			continue
		}
		for _, width := range []uint8{8, 16, 32, 64} {
			mask := ^uint64(0) >> (64 - width)
			for _, scale := range []uint64{0, 1, 2, 7, ^uint64(0)} {
				for _, displacement := range []uint64{0, 9, mask} {
					ops := []ir.Instruction{
						{Op: ir.OpParam, Bits: width},
						{Op: ir.OpParam, Bits: width},
						{Op: ir.OpAddScaled, A: 0, B: 1, Imm: scale, Bits: width},
					}
					result := 2
					if displacement != 0 {
						ops = append(ops, ir.Instruction{Op: ir.OpConst, Imm: displacement, Bits: width},
							ir.Instruction{Op: ir.OpAdd, A: 2, B: 3, Bits: width})
						result = 4
					}
					code, err := nyanjit.Compile(&ir.Program{Instructions: ops, Arity: 2, Result: result}, ppc64LevelSpec("", level), compile.Func{})
					if err != nil {
						t.Fatal(err)
					}
					for _, args := range [][2]uint64{{0, 0}, {1, mask}, {mask, mask}, {0x8123456789abcdef, 0xfedcba9876543210}} {
						want := ((args[0] & mask) + (args[1]&mask)*scale + displacement) & mask
						got, err := code.Call(uintptr(args[0]), uintptr(args[1]))
						if err != nil || uint64(got) != want {
							t.Fatalf("CPU %s width %d scale %#x displacement %#x inputs %#v: %#x,%v; want %#x", level, width, scale, displacement, args, got, err, want)
						}
					}
					if err := code.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}

func TestPPC64MultiplyAddAliasedOperands(t *testing.T) {
	host, err := ppc64.HostCPU()
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilder()
	x := b.Param()
	b.Return(b.Add(b.Mul(x, x), x))
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, ppc64LevelSpec("", host), compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, x := range []uint64{0, 1, 7, 0x8000000000000001, ^uint64(0)} {
		if got, err := code.Call(uintptr(x)); err != nil || uint64(got) != x*x+x {
			t.Fatalf("aliased multiply-add(%#x)=%#x,%v; want %#x", x, got, err, x*x+x)
		}
	}
}
