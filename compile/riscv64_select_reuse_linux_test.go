//go:build linux && riscv64

package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestRISCV64SelectReusesDeadConditionRegister(t *testing.T) {
	policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)
	for position := 0; position < 3; position++ {
		if !policy.CanReuse(machine.SelectedInstruction{Op: machine.SelectedSelect}, position) {
			t.Fatalf("select input %d cannot be reused", position)
		}
	}
	b := builder.NewBuilder()
	condition, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	ifTrue, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	ifFalse, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Select(condition, ifTrue, ifFalse)
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
	if !strings.Contains(assembly, "beq x10, x0") || !strings.Contains(assembly, "addi x10, x11, 0") || !strings.Contains(assembly, "addi x10, x12, 0") {
		t.Fatalf("select did not reuse the condition register X10:\n%s", assembly)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, test := range []struct{ condition, want uint64 }{{0, 91}, {1, 73}, {^uint64(0), 73}} {
		got, err := code.Call(uintptr(test.condition), 73, 91)
		if err != nil || uint64(got) != test.want {
			t.Fatalf("Select(%#x) = %d, %v; want %d", test.condition, got, err, test.want)
		}
	}
}
