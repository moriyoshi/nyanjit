package compile

import (
	"errors"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestC386EntryLayout(t *testing.T) {
	layout, err := c386EntryLayout([]int{2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.arguments) != 2 || len(layout.arguments[0].Words) != 2 || len(layout.arguments[1].Words) != 2 {
		t.Fatalf("argument shape = %+v", layout.arguments)
	}
	for arg := range layout.arguments {
		for word := 0; word < 2; word++ {
			got := layout.arguments[arg].Words[word]
			want := 4 + 8*arg + 4*word
			if got.Storage != ABIStack || got.StackOffset != want || got.Register != "" {
				t.Fatalf("arg %d word %d = %+v, want stack offset %d", arg, word, got, want)
			}
		}
	}
	if got := layout.result.Words; len(got) != 2 || got[0].Register != "EAX" || got[1].Register != "EDX" ||
		got[0].Storage != ABIRegister || got[1].Storage != ABIRegister {
		t.Fatalf("result = %+v, want EAX:EDX", got)
	}
	if singleABIRegister(layout.result) != "" || singleABIRegister(layout.arguments[0]) != "" {
		t.Fatal("multiword or stack location was exposed as a scalar register")
	}
	if _, err := c386EntryLayout([]int{2, 2, 2, 2, 2, 2, 2, 2, 2}); !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("excess arity = %v, want ErrBackendLimit", err)
	}
}

func TestExistingEntryLocations(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" && runtime.GOARCH != "riscv64" {
		t.Skip("native target required")
	}
	for _, abi := range []ABIFlavor{ABIC, ABIGo} {
		target := newMachineTarget(CompileOptions{ABI: abi})
		layout, err := entryLayout(target, []int{1, 1})
		if err != nil {
			t.Fatal(err)
		}
		for i, arg := range layout.arguments {
			if got := singleABIRegister(arg); got != target.RegisterName(target.ParamRegister(i)) {
				t.Fatalf("ABI %d argument %d = %q", abi, i, got)
			}
		}
		if got := singleABIRegister(layout.result); got != target.RegisterName(target.ResultRegister()) {
			t.Fatalf("ABI %d result = %q", abi, got)
		}
	}
	if runtime.GOARCH == "amd64" {
		if _, err := entryLayout(newMachineTarget(CompileOptions{ABI: ABIC}), []int{1, 1, 1, 1, 1, 1, 1}); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("seventh amd64 argument = %v, want ErrBackendLimit", err)
		}
	}
}

func TestPreflightCallLocations(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" && runtime.GOARCH != "riscv64" {
		t.Skip("native target required")
	}
	effect, err := PreflightCall(CallSpec{Kind: FunctionCall, ABI: ABIC, ArgumentCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(effect.ArgumentLocations) != 2 {
		t.Fatalf("argument locations = %+v", effect.ArgumentLocations)
	}
	for i, value := range effect.ArgumentLocations {
		if got := singleABIRegister(value); got != effect.ArgumentRegisters[i] {
			t.Fatalf("argument %d = %q, want %q", i, got, effect.ArgumentRegisters[i])
		}
	}
	if got := singleABIRegister(effect.ResultLocation); got != effect.ResultRegister {
		t.Fatalf("result = %q, want %q", got, effect.ResultRegister)
	}
}

type test386Target struct{ abi ABIFlavor }

func (test386Target) Architecture() string                      { return "386" }
func (target test386Target) ABIFlavor() ABIFlavor               { return target.abi }
func (test386Target) RegisterBits() uint8                       { return 32 }
func (test386Target) SupportsOrdinalSP() bool                   { return false }
func (test386Target) Registers() []physReg                      { return []physReg{0, 1, 2, 3, 6, 7} }
func (test386Target) ParamRegister(int) physReg                 { return 0 }
func (test386Target) ResultRegister() physReg                   { return 0 }
func (test386Target) ScratchRegister() physReg                  { return 5 }
func (test386Target) WorkspaceRegister() physReg                { return 5 }
func (test386Target) CanScaled(uint64, uint64) bool             { return false }
func (test386Target) CanCombineScaled() bool                    { return false }
func (test386Target) CanMulAdd() bool                           { return false }
func (test386Target) CanBinaryImmediate(op, uint8, uint64) bool { return false }
func (test386Target) CanFoldAddress(uint64, uint64) bool        { return false }
func (test386Target) CanFoldBaseOffset(uint64) bool             { return false }
func (test386Target) CanReuse(ins machineInstruction, inputPosition int) bool {
	return inputPosition == 0 && (ins.Op == mMove || ins.Op == mBinary || ins.Op == mNarrow)
}
func (test386Target) CallTargetRegister() physReg          { return 0 }
func (test386Target) LinkRegister() (physReg, bool)        { return 0, false }
func (test386Target) ReturnAddress() machine.ReturnAddress { return machine.ReturnAddress{} }
func (test386Target) Integer128Result() ([2]physReg, bool) { return [2]physReg{}, false }
func (test386Target) ManagedFrameLimit() int               { return 0 }
func (test386Target) NativeByteOrder() ir.ByteOrder        { return ir.ByteOrderLittleEndian }
func (test386Target) PhysicalRegisters() []physReg {
	return []physReg{0, 1, 2, 3, 4, 5, 6, 7}
}
func (test386Target) BranchFits(machine.Instruction, int, int, int) bool { return true }
func (test386Target) RegisterName(reg physReg) string {
	return [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI"}[reg]
}

func TestSelectionRecordsRegisterWords(t *testing.T) {
	target := test386Target{}
	program, err := selectProgram([]instruction{{Op: opConst, Bits: 64, Imm: 0x123456789abcdef0}}, nil, 0, 0, target)
	if err != nil {
		t.Fatal(err)
	}
	if program.Values[0].Words != 2 {
		t.Fatalf("64-bit value on 32-bit target uses %d words, want 2", program.Values[0].Words)
	}
	program, err = selectProgram([]instruction{{Op: opConst, Bits: 32, Imm: 0x12345678}}, nil, 0, 0, target)
	if err != nil {
		t.Fatal(err)
	}
	if program.Values[0].Words != 1 {
		t.Fatalf("32-bit value on 32-bit target uses %d words, want 1", program.Values[0].Words)
	}
}

func TestEntryLayoutDispatches386(t *testing.T) {
	target := test386Target{abi: ABIC}
	layout, err := entryLayout(target, []int{2})
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.arguments) != 1 || len(layout.arguments[0].Words) != 2 || layout.arguments[0].Words[0].StackOffset != 4 ||
		layout.result.Words[0].Register != "EAX" {
		t.Fatalf("386 entry layout = %+v", layout)
	}
	target.abi = ABIGo
	goLayout, err := entryLayout(target, []int{2})
	if err != nil || goLayout.arguments[0].Words[0].StackOffset != 4 ||
		goLayout.result.Words[0].StackOffset != 12 || goLayout.result.Words[1].StackOffset != 16 {
		t.Fatalf("386 Go entry = %+v, %v", goLayout, err)
	}
}

func TestWideSelectionAndMemoryOn386Target(t *testing.T) {
	target := test386Target{abi: ABIC}
	ops := []instruction{
		{Op: opParam, Bits: 64}, {Op: opParam, Bits: 64},
		{Op: opConst, Bits: 64, Imm: 1},
		{Op: opAdd, Bits: 64, A: 0, B: 2},
	}
	program, err := selectProgram(ops, nil, 2, 3, target)
	if err != nil {
		t.Fatal(err)
	}
	if program.Values[0].Words != 2 || program.Values[1].Words != 2 || program.Values[3].Words != 2 {
		t.Fatalf("wide values were not paired: %+v", program.Values)
	}
	ops = []instruction{
		{Op: opParam, Bits: 32},
		{Op: opParam, Bits: 64}, {Op: opParam, Bits: 64},
		{Op: opStore64, A: 0, B: 1},
		{Op: opLoad64, A: 0},
	}
	if _, err := selectProgram(ops, nil, 3, 4, target); err != nil {
		t.Fatal(err)
	}
}
