//go:build linux && ppc64

package execution_test

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"math"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

// Build a C ABI probe that uses R30 as a local integer while calling back.
// The callback must find g0 through the active runtime call's C back chains,
// then return the original C nonvolatile registers and condition fields.
func callbackCProbe(t *testing.T, convention abi.PPC64CABI) *execution.Code {
	t.Helper()
	profile, err := abi.NewPPC64Profile("ppc64", abi.C, convention)
	if err != nil {
		t.Fatal(err)
	}
	emitter, _ := ppc64.NewEmitter(profile, abi.StackPlatform)
	assembler, _ := ppc64.NewByteAssembler(ir.ByteOrderBigEndian)
	concrete := func(ins ppc64.Instruction) {
		ins.PC = assembler.AssembledSize()
		if err := assembler.Visit(ins); err != nil {
			t.Fatal(err)
		}
	}
	allocated := func(ins machine.Instruction) {
		seq, err := emitter.Expand(ins, assembler.AssembledSize())
		if err != nil {
			t.Fatal(err)
		}
		for _, native := range seq {
			concrete(native)
		}
	}
	allocated(machine.Instruction{Op: machine.OpFrameEnter, Imm: 256})
	for _, ins := range []ppc64.Instruction{
		{Op: ppc64.Std, A: 30, B: 1, Imm: 128},
		{Op: ppc64.Stfd, A: 14, B: 1, Imm: 136},
		{Op: ppc64.Addi, A: 11, Imm: 144},
		{Op: ppc64.Stvx, A: 20, B: 1, C: 11},
		{Op: ppc64.Mfcr, A: 11},
		{Op: ppc64.Std, A: 11, B: 1, Imm: 160},
		{Op: ppc64.Std, A: 5, B: 1, Imm: 168},
		{Op: ppc64.Lfd, A: 14, B: 5},
		{Op: ppc64.Addi, A: 11, Imm: 16},
		{Op: ppc64.Lvx, A: 20, B: 5, C: 11},
	} {
		concrete(ins)
	}
	allocated(machine.Instruction{Op: machine.OpConst, A: 30, Imm: 0x1234})
	allocated(machine.Instruction{Op: machine.OpConst, A: 11, Imm: 0xa5a5a5a5})
	concrete(ppc64.Instruction{Op: ppc64.Mtcrf, A: 11, Imm: 0x38})
	allocated(machine.Instruction{Op: machine.OpMove, A: 12, B: 3})
	allocated(machine.Instruction{Op: machine.OpMove, A: 3, B: 4})
	allocated(machine.Instruction{Op: machine.OpCallC, PPC64CABI: convention})
	for _, ins := range []ppc64.Instruction{
		{Op: ppc64.Ld, A: 5, B: 1, Imm: 168},
		{Op: ppc64.Stfd, A: 14, B: 5, Imm: 8},
		{Op: ppc64.Addi, A: 11, Imm: 16},
		{Op: ppc64.Stvx, A: 20, B: 5, C: 11},
		{Op: ppc64.Std, A: 30, B: 5, Imm: 32},
		{Op: ppc64.Mfcr, A: 11},
		{Op: ppc64.Std, A: 11, B: 5, Imm: 40},
		{Op: ppc64.Addi, A: 11, Imm: 144},
		{Op: ppc64.Lvx, A: 20, B: 1, C: 11},
		{Op: ppc64.Lfd, A: 14, B: 1, Imm: 136},
		{Op: ppc64.Ld, A: 30, B: 1, Imm: 128},
		{Op: ppc64.Ld, A: 11, B: 1, Imm: 160},
		{Op: ppc64.Mtcrf, A: 11, Imm: 0x38},
	} {
		concrete(ins)
	}
	allocated(machine.Instruction{Op: machine.OpFrameLeave, Imm: 256})
	allocated(machine.Instruction{Op: machine.OpReturn})
	code, err := execution.NewCode(assembler.Bytes(), execution.CodeOptions{ABI: abi.C, PPC64CABI: convention, Foreign: true, Arity: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = code.Close() })
	return code
}

func TestPPC64CallbackBothELFConventionsPreserveCState(t *testing.T) {
	clobber := callbackGoClobberProbe(t)
	data, err := execution.Scratch(16)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.FreeScratch(data)
	for i := range data {
		data[i] = byte(0xf0 + i)
	}

	for _, convention := range []abi.PPC64CABI{abi.PPC64ELFv1, abi.PPC64ELFv2} {
		callback, err := execution.RegisterForeignCallbackWithOptions(func(x uint64) uint64 {
			_, err := clobber.Call(uintptr(unsafe.Pointer(&data[0])))
			if err != nil {
				panic(err)
			}
			return callbackGrow(x, 64)
		}, execution.CallbackOptions{PPC64CABI: convention})
		if err != nil {
			t.Fatal(err)
		}
		code := callbackCProbe(t, convention)
		scratch, err := execution.Scratch(48)
		if err != nil {
			t.Fatal(err)
		}
		defer execution.FreeScratch(scratch)
		floatBits := math.Float64bits(3.25)
		binary.BigEndian.PutUint64(scratch, floatBits)
		for i := 16; i < 32; i++ {
			scratch[i] = byte(i * 7)
		}
		vector := append([]byte(nil), scratch[16:32]...)
		got, err := code.Call(callback, 35, uintptr(unsafe.Pointer(&scratch[0])))
		if err != nil || got != 42 {
			t.Fatalf("ELFv%d callback = %d,%v", convention, got, err)
		}
		if fp := binary.BigEndian.Uint64(scratch[8:]); fp != floatBits {
			t.Fatalf("F14 changed: %#x != %#x", fp, floatBits)
		}
		if !slices.Equal(scratch[16:32], vector) {
			t.Fatalf("V20 changed: %x != %x", scratch[16:32], vector)
		}
		if r30 := binary.BigEndian.Uint64(scratch[32:]); r30 != 0x1234 {
			t.Fatalf("C's R30 changed: %#x", r30)
		}
		if cr := binary.BigEndian.Uint64(scratch[40:]); cr&0x00fff000 != 0x00a5a000 {
			t.Fatalf("C's CR2-CR4 changed: %#x", cr)
		}
	}
}

func TestPPC64CallbackGoCallsGoAndReentersJIT(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	b.Return(b.CallGo(b.Const(uint64(reflect.ValueOf(callbackJITGoTarget).Pointer())), x))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.CompileGo(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	callback, err := execution.RegisterForeignCallback(func(x uint64) uint64 {
		result, err := code.Call(uintptr(x))
		if err != nil {
			panic(err)
		}
		return uint64(result)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.CallForeign(callback, []uintptr{35}); got != 42 {
		t.Fatalf("C callback to JIT to Go=%d", got)
	}
}

func callbackJITGoTarget(x uint64) uint64 { runtime.GC(); return x + 7 }

// This ABIInternal leaf deliberately destroys Go-volatile state that the C
// callback boundary must preserve. It performs no Go calls or stack access.
func callbackGoClobberProbe(t *testing.T) *execution.Code {
	t.Helper()
	assembler, _ := ppc64.NewByteAssembler(ir.ByteOrderBigEndian)
	emit := func(ins ppc64.Instruction) {
		ins.PC = assembler.AssembledSize()
		if err := assembler.Visit(ins); err != nil {
			t.Fatal(err)
		}
	}
	for r := machine.Register(14); r < 32; r++ {
		if r != 30 {
			emit(ppc64.Instruction{Op: ppc64.Addi, A: r, Imm: int64(r) * 17})
		}
	}
	for r := machine.Register(14); r < 32; r++ {
		emit(ppc64.Instruction{Op: ppc64.Lfd, A: r, B: 3})
	}
	for r := machine.Register(20); r < 32; r++ {
		emit(ppc64.Instruction{Op: ppc64.Lvx, A: r, B: 3, C: 0})
	}
	emit(ppc64.Instruction{Op: ppc64.Addi, A: 11})
	emit(ppc64.Instruction{Op: ppc64.Mtcrf, A: 11, Imm: 255})
	emit(ppc64.Instruction{Op: ppc64.Blr})
	code, err := execution.NewCode(assembler.Bytes(), execution.CodeOptions{ABI: abi.Go, Arity: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = code.Close() })
	return code
}

func TestPPC64JITCallsCCallbackThatReentersJIT(t *testing.T) {
	innerBuilder := nyanjit.NewBuilder()
	input := innerBuilder.Param()
	innerBuilder.Return(innerBuilder.CallGo(innerBuilder.Const(uint64(reflect.ValueOf(callbackJITGoTarget).Pointer())), input))
	innerProgram, err := innerBuilder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	inner, err := compile.CompileGo(innerProgram, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	for _, convention := range []abi.PPC64CABI{abi.PPC64ELFv1, abi.PPC64ELFv2} {
		callback, err := execution.RegisterForeignCallbackWithOptions(func(x uint64) uint64 {
			value, err := inner.Call(uintptr(x + 1))
			if err != nil {
				panic(err)
			}
			return uint64(value) + 1
		}, execution.CallbackOptions{PPC64CABI: convention})
		if err != nil {
			t.Fatal(err)
		}
		effect, err := abi.PreflightCall(abi.CallSpec{ABI: abi.C, ArgumentCount: 1, PPC64CABI: convention})
		if err != nil {
			t.Fatal(err)
		}
		b := nyanjit.NewBuilder()
		x := b.Param()
		b.Return(b.CallCWithEffect(b.Const(uint64(callback)), effect, x))
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		code, err := compile.CompileGo(program, abi.CompileOptions{ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
		if err != nil {
			t.Fatal(err)
		}
		got, err := code.Call(33)
		_ = code.Close()
		if err != nil || got != 42 {
			t.Fatalf("ELFv%d JIT/C/callback/JIT/Go=%d,%v", convention, got, err)
		}
	}
}
