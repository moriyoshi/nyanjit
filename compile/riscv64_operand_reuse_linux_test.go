//go:build linux && riscv64

package compile_test

import (
	"encoding/binary"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestRISCV64MulAddReusesDeadBase(t *testing.T) {
	policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)
	for position := 0; position < 3; position++ {
		if !policy.CanReuse(machine.SelectedInstruction{Op: machine.SelectedMulAdd}, position) {
			t.Fatalf("multiply-add input %d cannot be reused", position)
		}
	}
	b := builder.NewBuilder()
	base, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	index, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	result := base.Add(index.MulConst(7))
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}
	assembly, err := compile.EmitAssembly(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(assembly, "mul x30, ") || !strings.Contains(assembly, "add x10, x10, x30") {
		t.Fatalf("multiply-add did not reuse dead base X10:\n%s", assembly)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, test := range []struct{ base, index uint64 }{
		{0, 0}, {21, 3}, {^uint64(0), 2}, {9, ^uint64(0)},
	} {
		got, err := code.Call(uintptr(test.base), uintptr(test.index))
		want := test.base + test.index*7
		if err != nil || uint64(got) != want {
			t.Fatalf("scaled expression (%#x, %#x, 7) = %#x, %v; want %#x", test.base, test.index, got, err, want)
		}
	}
}

func TestRISCV64IndexedLoadReusesDeadBase(t *testing.T) {
	policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)
	for position := 0; position < 2; position++ {
		if !policy.CanReuse(machine.SelectedInstruction{Op: machine.SelectedLoadAddress}, position) {
			t.Fatalf("indexed load input %d cannot be reused", position)
		}
	}
	memory, err := syscall.Mmap(-1, 0, 8192, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	const want = uint64(0xfedcba9876543210)
	binary.LittleEndian.PutUint64(memory[4096+8*7:], want)
	b := builder.NewBuilder()
	base, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	index, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	scaled := base.Add(index.MulConst(7))
	displacement, err := b.Const(4096)
	if err != nil {
		t.Fatal(err)
	}
	address, err := b.Add(scaled, displacement)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Load64(address)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}
	assembly, err := compile.EmitAssembly(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(assembly, "mul x30, ") || !strings.Contains(assembly, "ld x10, 0(x31)") {
		t.Fatalf("indexed load did not reuse dead base X10:\n%s", assembly)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	basePointer := uint64(uintptr(unsafe.Pointer(&memory[0])))
	got, err := code.Call(uintptr(basePointer), 8)
	if err != nil || uint64(got) != want {
		t.Fatalf("indexed load = %#x, %v; want %#x", got, err, want)
	}
}
