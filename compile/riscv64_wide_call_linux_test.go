//go:build linux && riscv64

package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestRISCV64WideValueLiveAcrossCCall(t *testing.T) {
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
	wide, err := b.ParamBits(128)
	if err != nil {
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
	called, err := b.CallC(target, three, three)
	if err != nil {
		t.Fatal(err)
	}
	one, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	incremented, err := b.Add(wide, one)
	if err != nil {
		t.Fatal(err)
	}
	high, err := b.ExtractBits(incremented, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Add(high, called)
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
	options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240}
	report, err := compile.Analyze(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasFunctionCalls || report.StackBytes == 0 {
		t.Fatalf("wide call has no call frame: %+v", report)
	}
	code, err := compile.CompileForeign(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, test := range []struct{ low, high uint64 }{
		{0, 0}, {9, 21}, {^uint64(0), 17}, {^uint64(0), ^uint64(0)},
	} {
		got, err := code.Call(uintptr(test.low), uintptr(test.high))
		want := test.high + 6
		if test.low == ^uint64(0) {
			want++
		}
		if err != nil || uint64(got) != want {
			t.Fatalf("Call(%#x, %#x) = %#x, %v; want %#x", test.low, test.high, got, err, want)
		}
	}
}
