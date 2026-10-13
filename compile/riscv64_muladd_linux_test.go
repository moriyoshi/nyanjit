//go:build linux && riscv64

package compile_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestRISCV64MulAddLiveAcrossCCall(t *testing.T) {
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
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	live := input.Add(input.MulConst(7))
	if err := b.Leave(live); err != nil {
		t.Fatal(err)
	}
	target, err := b.ConstNative(address)
	if err != nil {
		t.Fatal(err)
	}
	three, err := b.Const(3)
	if err != nil {
		t.Fatal(err)
	}
	called, err := b.CallC(target, input, three)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Add(live, called)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := compile.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
		program.Result, nil, abi.MaxArguments(), riscv64.NewPolicy(abi.C, abi.StackPlatform))
	if err != nil {
		t.Fatal(err)
	}
	mulAdds := 0
	for _, ins := range selected.Instructions {
		if ins.Op == machine.SelectedMulAdd {
			mulAdds++
		}
	}
	if mulAdds != 1 {
		t.Fatalf("selected %d multiply-add operations, want 1", mulAdds)
	}
	options := compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}}
	assembly, err := lowerListing(program, compile.TargetSpec{}, options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(assembly, "mul x30, ") || !strings.Contains(assembly, "add ") {
		t.Fatalf("multiply-add expansion missing:\n%s", assembly)
	}
	report, err := lowerReport(program, compile.TargetSpec{}, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasFunctionCalls() || report.SpillStorage() != abi.SpillStack {
		t.Fatalf("live value was not preserved across C call: %+v", report.Snapshot())
	}
	if !slices.Contains(report.ClobberedRegisters(), "X30") {
		t.Fatalf("multiply-add scratch register absent from report: %+v", report.ClobberedRegisters())
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, input := range []uint64{0, 1, 19, 0x123456789abcdef0, ^uint64(0)} {
		got, err := code.Call(uintptr(input))
		want := input*9 + 3
		if err != nil || uint64(got) != want {
			t.Fatalf("Call(%#x) = %#x, %v; want %#x", input, got, err, want)
		}
	}
}
