//go:build linux && (ppc64 || ppc64le)

package compile_test

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/target/ppc64"
	"math/bits"
	"math/rand/v2"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestPPC64WideNativeCarryChainsAndProducts(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, op := range []string{"add", "sub", "mul"} {
		b := nyanjit.NewBuilder()
		x, y := b.ParamBits(128), b.ParamBits(128)
		result := b.Add(x, y)
		if op == "sub" {
			result = b.Sub(x, y)
		}
		if op == "mul" {
			result = b.Mul(x, y)
		}
		b.Return(result)
		program := finishPPC(t, b)
		options := abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536, PPC64CPU: cpu}
		listing, err := compile.EmitAssembly(program, options)
		if err != nil {
			t.Fatal(err)
		}
		if op == "mul" {
			if strings.Count(listing, "mulhdu ") != 1 {
				t.Fatalf("wide product lacks native high half:\n%s", listing)
			}
		} else {
			low, high := "addc.", "adde."
			if op == "sub" {
				low, high = "subfc.", "subfe."
			}
			if strings.Count(listing, low+" ") != 1 || strings.Count(listing, high+" ") != 1 || strings.Contains(listing, "addze ") || strings.Contains(listing, "subfze ") {
				t.Fatalf("wide operation lacks direct carry chain:\n%s", listing)
			}
		}
		code, err := compile.Compile(program, options)
		if err != nil {
			t.Fatal(err)
		}
		random := rand.New(rand.NewPCG(123, 456))
		samples := [][4]uint64{{0, 0, 0, 0}, {^uint64(0), 0, 1, 0}, {0, 1, 1, 0}, {^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}, {1, 0, 0, ^uint64(0)}}
		for i := 0; i < 200; i++ {
			samples = append(samples, [4]uint64{random.Uint64(), random.Uint64(), random.Uint64(), random.Uint64()})
		}
		for _, s := range samples {
			lo, hi := uint64(0), uint64(0)
			switch op {
			case "add":
				var c uint64
				lo, c = bits.Add64(s[0], s[2], 0)
				hi, _ = bits.Add64(s[1], s[3], c)
			case "sub":
				var c uint64
				lo, c = bits.Sub64(s[0], s[2], 0)
				hi, _ = bits.Sub64(s[1], s[3], c)
			case "mul":
				hi, lo = bits.Mul64(s[0], s[2])
				hi += s[1]*s[2] + s[0]*s[3]
			}
			gotLo, gotHi, err := code.CallPair(uintptr(s[0]), uintptr(s[1]), uintptr(s[2]), uintptr(s[3]))
			if err != nil || uint64(gotLo) != lo || uint64(gotHi) != hi {
				t.Fatalf("%s(%x): got%#x:%#x,%v;want%#x:%#x\n%s", op, s, gotHi, gotLo, err, hi, lo, listing)
			}
		}
		code.Close()
	}
}

func TestPPC64WideCarryRecomputedAfterClobber(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	address := uint64(uintptr(unsafe.Pointer(&memory[0])))
	for _, subtract := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		x, y := b.ParamBits(128), b.ParamBits(128)
		home := b.Param()
		result := b.Add(x, y)
		if subtract {
			result = b.Sub(x, y)
		}
		b.Store64(home, b.ExtractBits(result, 0, 64))
		b.Store64(home, b.Add(b.ExtractBits(x, 64, 64), b.ExtractBits(y, 64, 64))) // overwrite XER after the low word
		b.Return(result)
		program := finishPPC(t, b)
		options := abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536}
		listing, err := compile.EmitAssembly(program, options)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(listing, ": addc ") && !strings.Contains(listing, ": subfc ") {
			t.Fatalf("clobbered carry was incorrectly reused:\n%s", listing)
		}
		code, err := compile.Compile(program, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range [][4]uint64{{^uint64(0), 0, 1, 0}, {0, 0, 1, 0}, {0, ^uint64(0), ^uint64(0), 0}, {^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}} {
			lo, c := bits.Add64(s[0], s[2], 0)
			hi, _ := bits.Add64(s[1], s[3], c)
			if subtract {
				lo, c = bits.Sub64(s[0], s[2], 0)
				hi, _ = bits.Sub64(s[1], s[3], c)
			}
			gotLo, gotHi, err := code.CallPair(uintptr(s[0]), uintptr(s[1]), uintptr(s[2]), uintptr(s[3]), uintptr(address))
			if err != nil || uint64(gotLo) != lo || uint64(gotHi) != hi {
				t.Fatalf("clobber subtract%v:got%#x:%#x,%v;want%#x:%#x", subtract, gotHi, gotLo, err, hi, lo)
			}
		}
		code.Close()
	}
}

func TestPPC64WideCarryAliasingAndScopeLabelTransfer(t *testing.T) {
	for _, subtract := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		x := b.ParamBits(128)
		b.Enter()
		result := b.Add(x, x)
		if subtract {
			result = b.Sub(x, x)
		}
		b.Leave(result)
		label := b.NewLabel()
		b.JumpArgs(label, result)
		state := b.BindParamsBits(label, []uint8{128})
		b.Return(state[0])
		code, err := compile.Compile(finishPPC(t, b), abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536})
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range [][2]uint64{{0, 0}, {1, 1}, {^uint64(0), 0}, {^uint64(0), ^uint64(0)}, {0, 1 << 63}} {
			lo, c := bits.Add64(input[0], input[0], 0)
			hi, _ := bits.Add64(input[1], input[1], c)
			if subtract {
				lo, hi = 0, 0
			}
			gotLo, gotHi, err := code.CallPair(uintptr(input[0]), uintptr(input[1]))
			if err != nil || uint64(gotLo) != lo || uint64(gotHi) != hi {
				t.Fatalf("scope/label subtract%v input%x:got%#x:%#x,%v;want%#x:%#x", subtract, input, gotHi, gotLo, err, hi, lo)
			}
		}
		code.Close()
	}
}

func TestPPC64SharedCarryIsMaterialized(t *testing.T) {
	for _, subtract := range []bool{false, true} {
		carry, high, low := ir.OpCarryAdd, ir.OpAddWithCarry, ir.OpAdd
		if subtract {
			carry, high, low = ir.OpBorrowSub, ir.OpSubWithBorrow, ir.OpSub
		}
		ops := []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64}, {Op: low, Bits: 64, A: 0, B: 2}, {Op: carry, Bits: 64, A: 0, B: 2}, {Op: high, Bits: 64, A: 1, B: 3, C: 5}, {Op: ir.OpXor, Bits: 64, A: 6, B: 5}}
		program := &ir.Program{Instructions: ops, Arity: 4, Result: 4, ResultParts: []int{4, 7}}
		options := abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536}
		listing, err := compile.EmitAssembly(program, options)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(listing, "addze ") && !strings.Contains(listing, "subfze ") {
			t.Fatalf("observed carry was not materialized:\n%s", listing)
		}
		code, err := compile.Compile(program, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range [][4]uint64{{^uint64(0), 0, 1, 0}, {0, 0, 1, 0}, {1, 0, 0, 0}, {^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}} {
			lo, c := bits.Add64(s[0], s[2], 0)
			hi, _ := bits.Add64(s[1], s[3], c)
			if subtract {
				lo, c = bits.Sub64(s[0], s[2], 0)
				hi, _ = bits.Sub64(s[1], s[3], c)
			}
			hi ^= c
			gotLo, gotHi, err := code.CallPair(uintptr(s[0]), uintptr(s[1]), uintptr(s[2]), uintptr(s[3]))
			if err != nil || uint64(gotLo) != lo || uint64(gotHi) != hi {
				t.Fatalf("shared subtract%v:got%#x:%#x,%v;want%#x:%#x", subtract, gotHi, gotLo, err, hi, lo)
			}
		}
		code.Close()
	}
}

func TestPPC64WideHighWordResultFlags(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, subtract := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		x, y := b.ParamBits(128), b.ParamBits(128)
		result := b.Add(x, y)
		if subtract {
			result = b.Sub(x, y)
		}
		high := b.ExtractBits(result, 64, 64)
		code, listing, _ := ppcFlagCode(t, b, b.IsZero(high), cpu)
		assertNoPPCCompare(t, listing)
		for _, s := range [][4]uint64{{0, 0, 0, 0}, {^uint64(0), 0, 1, 0}, {0, 1, 1, 0}, {^uint64(0), ^uint64(0), 1, 0}} {
			_, c := bits.Add64(s[0], s[2], 0)
			hi, _ := bits.Add64(s[1], s[3], c)
			if subtract {
				_, c = bits.Sub64(s[0], s[2], 0)
				hi, _ = bits.Sub64(s[1], s[3], c)
			}
			want := uint64(0)
			if hi == 0 {
				want = 1
			}
			got, err := code.Call(uintptr(s[0]), uintptr(s[1]), uintptr(s[2]), uintptr(s[3]))
			if err != nil || uint64(got) != want {
				t.Fatalf("wide high zero subtract%v:got%d,%v;want%d", subtract, got, err, want)
			}
		}
	}
}
