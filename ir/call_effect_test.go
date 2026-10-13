package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// A custom call effect must describe a function call that some target could
// accept. Lowering matches the remaining fields against the chosen target.
func TestValidateCallEffect(t *testing.T) {
	valid, err := abi.PreflightCall(abi.CallSpec{Architecture: "amd64", Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	program := func(op ir.Opcode, effect *abi.CallEffect) *ir.Program {
		return &ir.Program{Arity: 2, Result: 2, Instructions: []ir.Instruction{
			{Op: ir.OpParam}, {Op: ir.OpParam}, {Op: op, Args: []int{0, 1}, Effect: effect},
		}}
	}
	if err := program(ir.OpCallC, &valid).Validate(); err != nil {
		t.Fatalf("preflight effect rejected: %v", err)
	}
	for _, callABI := range []abi.Flavor{abi.C, abi.Go} {
		// 386 results live in ResultLocation, not a single result register.
		effect, err := abi.PreflightCall(abi.CallSpec{Architecture: "386", Kind: abi.FunctionCall, ABI: callABI, ArgumentCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		op := ir.OpCallC
		if callABI == abi.Go {
			op = ir.OpCallGo
		}
		if err := program(op, &effect).Validate(); err != nil {
			t.Fatalf("386 %v preflight effect rejected: %v", callABI, err)
		}
	}
	if err := program(ir.OpCallC, nil).Validate(); err != nil {
		t.Fatalf("call without effect rejected: %v", err)
	}
	mutate := func(f func(*abi.CallEffect)) *abi.CallEffect {
		e := valid.Clone()
		f(&e)
		return &e
	}
	for name, c := range map[string]struct {
		op     ir.Opcode
		effect *abi.CallEffect
	}{
		"zero":            {ir.OpCallC, &abi.CallEffect{}},
		"syscall":         {ir.OpSyscall, &valid},
		"kind":            {ir.OpCallC, mutate(func(e *abi.CallEffect) { e.Kind = abi.SystemCall })},
		"abi":             {ir.OpCallGo, &valid},
		"architecture":    {ir.OpCallC, mutate(func(e *abi.CallEffect) { e.Architecture = "" })},
		"number register": {ir.OpCallC, mutate(func(e *abi.CallEffect) { e.NumberRegister = "RAX" })},
		"result":          {ir.OpCallC, mutate(func(e *abi.CallEffect) { e.ResultRegister, e.ResultLocation = "", abi.ValueLocation{} })},
		"result clobber":  {ir.OpCallC, mutate(func(e *abi.CallEffect) { e.ClobberedRegisters = nil })},
	} {
		if err := program(c.op, c.effect).Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Errorf("%s: Validate = %v, want ErrInvalidProgram", name, err)
		}
	}
}
