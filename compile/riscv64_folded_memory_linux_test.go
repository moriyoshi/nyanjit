//go:build linux && riscv64

package compile_test

import (
	"encoding/binary"
	"slices"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestRISCV64FoldedMemoryAddresses(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 16384, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	base := uint64(uintptr(unsafe.Pointer(&memory[0])))
	options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}
	policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)

	t.Run("scaled load with negative displacement", func(t *testing.T) {
		const want = uint64(0xfedcba9876543210)
		binary.LittleEndian.PutUint64(memory[4096+3*8:], want)
		b := builder.NewBuilder()
		pointer, err := b.Param()
		if err != nil {
			t.Fatal(err)
		}
		index, err := b.Param()
		if err != nil {
			t.Fatal(err)
		}
		scaled := pointer.Add(index.MulConst(8))
		const offset = ^uint64(4095)
		displacement, err := b.Const(offset)
		if err != nil {
			t.Fatal(err)
		}
		address, err := b.Add(scaled, displacement)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := b.Load64(address)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(loaded); err != nil {
			t.Fatal(err)
		}
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		selected, err := machine.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
			program.Result, nil, abi.MaxArguments(), policy)
		if err != nil {
			t.Fatal(err)
		}
		folded := false
		for _, ins := range selected.Instructions {
			if ins.Op == machine.SelectedLoadAddress && ins.Imm == 8 && ins.Disp == offset {
				folded = true
			}
		}
		if !folded {
			t.Fatalf("scaled load was not folded: %+v", selected.Instructions)
		}
		report, err := compile.Analyze(program, options)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(report.ClobberedRegisters, "X30") || !slices.Contains(report.ClobberedRegisters, "X31") {
			t.Fatalf("folded far load omitted scratch clobbers: %+v", report.ClobberedRegisters)
		}
		code, err := compile.Compile(program, options)
		if err != nil {
			t.Fatal(err)
		}
		defer code.Close()
		got, err := code.Call(uintptr(base+8192), 3)
		if err != nil || uint64(got) != want {
			t.Fatalf("folded load = %#x, %v; want %#x", got, err, want)
		}
	})

	t.Run("non-power-of-two indexed store and load", func(t *testing.T) {
		b := builder.NewBuilder()
		pointer, err := b.Param()
		if err != nil {
			t.Fatal(err)
		}
		indexAndValue, err := b.Param()
		if err != nil {
			t.Fatal(err)
		}
		const scale = 7
		const offset = ^uint64(4095)
		makeAddress := func() builder.Value {
			t.Helper()
			scaled := pointer.Add(indexAndValue.MulConst(scale))
			displacement, err := b.Const(offset)
			if err != nil {
				t.Fatal(err)
			}
			address, err := b.Add(scaled, displacement)
			if err != nil {
				t.Fatal(err)
			}
			return address
		}
		if err := b.Store64(makeAddress(), indexAndValue); err != nil {
			t.Fatal(err)
		}
		loaded, err := b.Load64(makeAddress())
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(loaded); err != nil {
			t.Fatal(err)
		}
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		selected, err := machine.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
			program.Result, nil, abi.MaxArguments(), policy)
		if err != nil {
			t.Fatal(err)
		}
		var stores, loads int
		for _, ins := range selected.Instructions {
			if ins.Op == machine.SelectedStoreAddress && ins.Imm == scale && ins.Disp == offset {
				stores++
				if ins.B != ins.C {
					t.Fatalf("store lost aliased index/value: %+v", ins)
				}
			}
			if ins.Op == machine.SelectedLoadAddress && ins.Imm == scale && ins.Disp == offset {
				loads++
			}
		}
		if stores != 1 || loads != 1 {
			t.Fatalf("folded store/load counts = %d/%d", stores, loads)
		}
		report, err := compile.Analyze(program, options)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(report.ClobberedRegisters, "X30") || !slices.Contains(report.ClobberedRegisters, "X31") {
			t.Fatalf("folded far address omitted scratch clobbers: %+v", report.ClobberedRegisters)
		}
		code, err := compile.Compile(program, options)
		if err != nil {
			t.Fatal(err)
		}
		defer code.Close()
		got, err := code.Call(uintptr(base+8192), 8)
		if err != nil || got != 8 {
			t.Fatalf("folded store/load = %d, %v; want 8", got, err)
		}
		if stored := binary.LittleEndian.Uint64(memory[4096+8*scale:]); stored != 8 {
			t.Fatalf("folded store wrote %d, want 8", stored)
		}
	})

	t.Run("store with large base offset", func(t *testing.T) {
		b := builder.NewBuilder()
		pointer, err := b.Param()
		if err != nil {
			t.Fatal(err)
		}
		value, err := b.Param()
		if err != nil {
			t.Fatal(err)
		}
		displacement, err := b.Const(4096)
		if err != nil {
			t.Fatal(err)
		}
		address, err := b.Add(pointer, displacement)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Store64(address, value); err != nil {
			t.Fatal(err)
		}
		if err := b.Return(value); err != nil {
			t.Fatal(err)
		}
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		selected, err := machine.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
			program.Result, nil, abi.MaxArguments(), policy)
		if err != nil {
			t.Fatal(err)
		}
		folded := false
		for _, ins := range selected.Instructions {
			if ins.Op == machine.SelectedStoreOffset && ins.Disp == 4096 {
				folded = true
			}
		}
		if !folded {
			t.Fatalf("base-offset store was not folded: %+v", selected.Instructions)
		}
		code, err := compile.Compile(program, options)
		if err != nil {
			t.Fatal(err)
		}
		defer code.Close()
		const want = uint64(0x123456789abcdef0)
		got, err := code.Call(uintptr(base), uintptr(want))
		if err != nil || uint64(got) != want {
			t.Fatalf("folded store call = %#x, %v; want %#x", got, err, want)
		}
		if stored := binary.LittleEndian.Uint64(memory[4096:]); stored != want {
			t.Fatalf("folded store wrote %#x, want %#x", stored, want)
		}
	})

	t.Run("emitter reports expansion writes", func(t *testing.T) {
		b := builder.NewBuilder()
		pointer, err := b.Const(base + 8192)
		if err != nil {
			t.Fatal(err)
		}
		index, err := b.Const(3)
		if err != nil {
			t.Fatal(err)
		}
		scaled := pointer.Add(index.MulConst(8))
		displacement, err := b.Const(^uint64(4095))
		if err != nil {
			t.Fatal(err)
		}
		address, err := b.Add(scaled, displacement)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := b.Load64(address)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(loaded); err != nil {
			t.Fatal(err)
		}
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		emitter := compile.NewEmitter()
		if err := emitter.AppendProgram(program); err != nil {
			t.Fatal(err)
		}
		if err := emitter.Exit(); err != nil {
			t.Fatal(err)
		}
		_, report, err := emitter.Emit(abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(report.ClobberedRegisters, "X30") || !slices.Contains(report.ClobberedRegisters, "X31") {
			t.Fatalf("emitter omitted expansion writes: %+v", report.ClobberedRegisters)
		}
	})
}
