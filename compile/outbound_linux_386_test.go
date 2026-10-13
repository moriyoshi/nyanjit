//go:build linux && 386

package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
)

func TestNestedCCall386(t *testing.T) {
	f := Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
	foreign := f
	foreign.Context = ContextForeign
	callee := builder.NewBuilder()
	a, err := callee.ParamBits(64)
	if err != nil {
		t.Fatal(err)
	}
	b, err := callee.ParamBits(64)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := callee.Add(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := callee.Return(sum); err != nil {
		t.Fatal(err)
	}
	calleeProgram, err := callee.Finish()
	if err != nil {
		t.Fatal(err)
	}
	calleeCode, err := compileCode(calleeProgram, TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	defer calleeCode.Close()
	address, err := calleeCode.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	caller := builder.NewBuilder()
	live, err := caller.ParamBits(64)
	if err != nil {
		t.Fatal(err)
	}
	target, err := caller.ConstNative(address)
	if err != nil {
		t.Fatal(err)
	}
	fortyTwo, err := caller.ConstBits(64, 42)
	if err != nil {
		t.Fatal(err)
	}
	called, err := caller.CallC(target, live, fortyTwo)
	if err != nil {
		t.Fatal(err)
	}
	result, err := caller.Add(called, live)
	if err != nil {
		t.Fatal(err)
	}
	if err := caller.Return(result); err != nil {
		t.Fatal(err)
	}
	callerProgram, err := caller.Finish()
	if err != nil {
		t.Fatal(err)
	}
	report, err := lowerReport(callerProgram, TargetSpec{}, foreign)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasFunctionCalls() || report.CallStackBytes() != 4 || report.StackBytes()%16 != 12 {
		t.Fatalf("386 call frame report = %+v", report.Snapshot())
	}
	callerCode, err := compileCode(callerProgram, TargetSpec{}, foreign)
	if err != nil {
		t.Fatal(err)
	}
	defer callerCode.Close()
	for _, input := range []uint64{0, 1, 1000, 0x1122334455667788} {
		lo, hi, err := callerCode.CallPair(execution.Words(input)...)
		if err != nil {
			t.Fatal(err)
		}
		got := execution.Wide(lo, hi)
		want := input*2 + 42
		if got != want {
			t.Fatalf("nested C call with %#x = %#x, want %#x", input, got, want)
		}
	}
}
