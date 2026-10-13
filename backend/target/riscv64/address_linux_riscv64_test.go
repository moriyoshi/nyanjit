//go:build linux && riscv64

package riscv64_test

import (
	"encoding/binary"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

func TestScaledAddressWithLargeDisplacement(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 16384, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	for _, test := range []struct {
		name   string
		disp   uint64
		offset int
	}{
		{"positive", 4096, 8192 + 24 + 4096},
		{"negative", ^uint64(4095), 8192 + 24 - 4096},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := riscv64.NewByteAssembler(abi.StackNoUse, 0)
			a.StoreAddress(10, 11, 12, 8, test.disp)
			a.LoadAddress(10, 10, 11, 8, test.disp)
			a.Ret()
			code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			const want = uint64(0xfedcba9876543210)
			base := uint64(uintptr(unsafe.Pointer(&memory[8192])))
			got, err := code.Call(uintptr(base), 3, uintptr(want))
			if err != nil || uint64(got) != want {
				t.Fatalf("load/store = %#x, %v; want %#x", got, err, want)
			}
			if stored := binary.LittleEndian.Uint64(memory[test.offset:]); stored != want {
				t.Fatalf("stored = %#x, want %#x", stored, want)
			}
		})
	}
}

func TestIndexedLoadAcrossBranchUsesExpandedSize(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 8192, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	binary.LittleEndian.PutUint64(memory[4096:], 123)

	policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)
	stream := machine.NewStream(machine.StreamOptions{
		Size:       func(ins machine.Instruction) int { return riscv64.PhysicalSize(ins, abi.StackNoUse, 0) },
		FlagEffect: riscv64.FlagEffect,
	})
	stream.Append(machine.Instruction{Op: machine.OpCompareImmediate, A: 10})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 0, Long: true, Far: true})
	stream.Append(machine.Instruction{Op: machine.OpLoadAddress, A: 13, B: 11, C: 12, Imm: 7, Disp: 4096})
	stream.Append(machine.Instruction{Op: machine.OpConst, A: 10, Imm: 99})
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	labels := []int{stream.PC()}
	stream.Append(machine.Instruction{Op: machine.OpConst, A: 10, Imm: 42})
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	bound := []bool{true}
	labels, err = machine.CompactBranches(stream, labels, bound)
	if err != nil {
		t.Fatal(err)
	}
	labels, err = machine.RelaxBranches(stream, labels, bound, policy.BranchFits)
	if err != nil {
		t.Fatal(err)
	}
	located, err := machine.ResolveBranches(stream, labels, bound, policy.BranchFits)
	if err != nil {
		t.Fatal(err)
	}
	a := riscv64.NewByteAssembler(abi.StackNoUse, 0)
	for _, entry := range located {
		a.SetPC(entry.PC)
		if err := machine.Visit(entry.Instruction, a); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.AssembledSize(); got != stream.PC() {
		t.Fatalf("assembler emitted %d bytes, stream counted %d", got, stream.PC())
	}
	code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	base := uint64(uintptr(unsafe.Pointer(&memory[0])))
	for _, test := range []struct{ input, want uint64 }{{0, 42}, {1, 99}} {
		got, err := code.Call(uintptr(test.input), uintptr(base), 0)
		if err != nil || uint64(got) != test.want {
			t.Fatalf("Call(%d) = %d, %v; want %d", test.input, got, err, test.want)
		}
	}
}
