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
	"github.com/moriyoshi/nyanjit/execution"
)

func TestRISCV64LeafAndABIArguments(t *testing.T) {
	leaf, err := testNewReturn(0x123456789abcdef0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := leaf.Call()
	_ = leaf.Close()
	if err != nil || got != 0x123456789abcdef0 {
		t.Fatalf("constant = %#x, %v", got, err)
	}
	add, err := testNewAdd()
	if err != nil {
		t.Fatal(err)
	}
	got, err = add.Call(20, 22)
	_ = add.Close()
	if err != nil || got != 42 {
		t.Fatalf("add = %d, %v", got, err)
	}
}

func TestRISCV64BranchAndTextAssembler(t *testing.T) {
	b := builder.NewBuilder()
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	input, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.CompareImmediate(input, builder.BranchBelow, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Branch(predicate, yes, no); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(yes); err != nil {
		t.Fatal(err)
	}
	one, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(one); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(no); err != nil {
		t.Fatal(err)
	}
	zero, err := b.Const(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(zero); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240}
	assembly, err := compile.EmitAssembly(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(assembly, "bltu ")+strings.Count(assembly, "bgeu ") != 1 {
		t.Fatalf("near branch did not relax to one instruction:\n%s", assembly)
	}
	if strings.Contains(assembly, ".word ") || strings.Contains(assembly, "frame.enter") {
		t.Fatalf("assembly contains undecoded instructions:\n%s", assembly)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, test := range []struct{ input, want uint64 }{{41, 1}, {42, 0}, {^uint64(0), 0}} {
		got, err := code.Call(uintptr(test.input))
		if err != nil || uint64(got) != test.want {
			t.Fatalf("branch(%d)=%d,%v; want %d", test.input, got, err, test.want)
		}
	}
}

func TestRISCV64ZeroComparisonBranch(t *testing.T) {
	b := builder.NewBuilder()
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	input, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.CompareImmediate(input, builder.BranchEqual, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Branch(predicate, yes, no); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(yes); err != nil {
		t.Fatal(err)
	}
	one, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(one); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(no); err != nil {
		t.Fatal(err)
	}
	two, err := b.Const(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(two); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240}
	assembly, err := compile.EmitAssembly(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(assembly, ", x0, ") || strings.Contains(assembly, "addi x31, x0, 0") {
		t.Fatalf("zero comparison did not use x0 directly:\n%s", assembly)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, test := range []struct{ input, want uint64 }{{0, 1}, {5, 2}} {
		got, err := code.Call(uintptr(test.input))
		if err != nil || uint64(got) != test.want {
			t.Fatalf("branch(%d) = %d, %v; want %d", test.input, got, err, test.want)
		}
	}
}

func TestRISCV64SymbolScaledMemory(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	b := builder.NewBuilder()
	index, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	base, err := b.Symbol("base")
	if err != nil {
		t.Fatal(err)
	}
	address := base.Add(index.MulConst(8))
	if err := b.Store64(address, value); err != nil {
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
	code, err := compile.CompileWithBindings(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240},
		map[string]uint64{"base": uint64(uintptr(unsafe.Pointer(&memory[0])))})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	const want = uint64(0xfedcba9876543210)
	got, err := code.Call(3, uintptr(want))
	if err != nil || uint64(got) != want {
		t.Fatalf("scaled memory = %#x,%v", got, err)
	}
	if stored := binary.LittleEndian.Uint64(memory[24:]); stored != want {
		t.Fatalf("stored = %#x", stored)
	}
}

func TestRISCV64OutboundCCall(t *testing.T) {
	callee, err := testNewAdd()
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	address, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder()
	input, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	target, err := b.ConstNative(address)
	if err != nil {
		t.Fatal(err)
	}
	seven, err := b.Const(7)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallC(target, input, seven)
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
	bytes, _, err := compile.Emit(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := execution.NewCode(bytes, execution.CodeOptions{ABI: abi.C, Arity: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	got, err := caller.Call(35)
	if err != nil || got != 42 {
		t.Fatalf("outbound C call = %d,%v", got, err)
	}
}

func TestRISCV64Syscall(t *testing.T) {
	b := builder.NewBuilder()
	number, err := b.Const(172) // Linux riscv64 getpid.
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Syscall(number)
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
	bytes, _, err := compile.Emit(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240})
	if err != nil {
		t.Fatal(err)
	}
	code, err := execution.NewCode(bytes, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call()
	if err != nil || got != uintptr(syscall.Getpid()) {
		t.Fatalf("getpid = %d, %v; want %d", got, err, syscall.Getpid())
	}
}
