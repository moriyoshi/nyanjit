//go:build linux && riscv64

package compile_test

import (
	"encoding/binary"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestRISCV64FoldedAddressesUnderRegisterPressure(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 8192, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	b := builder.NewBuilder()
	base, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	indexAndValue, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	const count = 40
	values := make([]builder.Value, count)
	for i := range values {
		constant, err := b.Const(uint64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		values[i], err = b.Add(seed, constant)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Leave(values...); err != nil {
		t.Fatal(err)
	}
	makeAddress := func() builder.Value {
		t.Helper()
		scaled := base.Add(indexAndValue.MulConst(8))
		displacement, err := b.Const(4096)
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
	result := loaded
	for _, value := range values {
		result, err = b.Add(result, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := machine.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
		program.Result, nil, abi.MaxArguments(), riscv64.NewPolicy(abi.C, abi.StackNoUse))
	if err != nil {
		t.Fatal(err)
	}
	var stores, loads int
	for _, ins := range selected.Instructions {
		if ins.Op == machine.SelectedStoreAddress {
			stores++
			if ins.B != ins.C {
				t.Fatalf("store lost aliased index/value: %+v", ins)
			}
		}
		if ins.Op == machine.SelectedLoadAddress {
			loads++
		}
	}
	if stores != 1 || loads != 1 {
		t.Fatalf("folded store/load counts = %d/%d", stores, loads)
	}
	options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}
	report, err := compile.Analyze(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.SpillStorage != abi.SpillWorkspace || report.WorkspaceBytes == 0 {
		t.Fatalf("register pressure did not spill: %+v", report)
	}
	if report.WorkspaceRegister != "X9" {
		t.Fatalf("workspace register = %q, want X9", report.WorkspaceRegister)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	pointer := uint64(uintptr(unsafe.Pointer(&memory[0])))
	for _, seed := range []uint64{0, 7, ^uint64(0)} {
		got, err := code.Call(uintptr(pointer), 3, uintptr(seed))
		want := 3 + count*seed + count*(count+1)/2
		if err != nil || uint64(got) != want {
			t.Fatalf("Call(seed=%#x) = %#x, %v; want %#x", seed, got, err, want)
		}
		if stored := binary.LittleEndian.Uint64(memory[4096+3*8:]); stored != 3 {
			t.Fatalf("folded store wrote %d, want 3", stored)
		}
	}
}
