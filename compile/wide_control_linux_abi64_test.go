//go:build linux && (amd64 || arm64 || riscv64)

package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestEmitterWideFragmentExit(t *testing.T) {
	fragment := &ir.Program{
		Instructions: []ir.Instruction{{Op: ir.OpConst, Imm: 17}, {Op: ir.OpConst, Imm: 19}},
		Result:       0, ResultParts: []int{0, 1},
	}
	emitter := NewEmitter()
	start, other := emitter.NewLabel(), emitter.NewLabel()
	if err := emitter.Bind(start); err != nil {
		t.Fatal(err)
	}
	if err := emitter.AppendProgram(fragment); err != nil {
		t.Fatal(err)
	}
	if err := emitter.Branch(0, BranchEqual, start, other); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Branch on pair error = %v", err)
	}
	if err := emitter.Exit(); err != nil {
		t.Fatal(err)
	}
	bytes, report, err := emitter.Emit(CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	if report.ResultWords != 2 || len(report.ResultLocation.Words) != 2 {
		t.Fatalf("result report = %+v", report)
	}
	code, err := execution.NewCode(bytes, execution.CodeOptions{ABI: ABIC, ResultWords: report.ResultWords}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	low, high, err := code.CallPair()
	if err != nil || low != 17 || high != 19 {
		t.Fatalf("CallPair = (%d, %d, %v), want (17, 19, nil)", low, high, err)
	}
}
