//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"math"
	"runtime"
	"testing"
)

func TestVariableHomesLeaveFMAOperandCapacity(t *testing.T) {
	b := n.NewBuilder()
	vars := make([]n.Var, 40)
	for i := range vars {
		vars[i] = b.Var(b.Float64(float64(i + 1)))
	}
	result := b.FMA(b.Float64(2), b.Float64(3), b.Float64(4))
	for _, v := range vars {
		result = b.FAdd(result, b.Get(v))
	}
	b.Return(b.CastNative(result))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	// Baseline amd64 cannot execute FMA, but its v3 lowering must still
	// reserve the complete operand set under the same home pressure.
	if _, err := compile.EmitAssembly(p, abi.CompileOptions{Architecture: "amd64", CPU: abi.CPUProfile{Level: "x86-64-v3"}, Stack: abi.StackPlatform, StackBytesAvailable: 8192}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOARCH == "amd64" {
		t.Skip("baseline amd64 execution does not provide FMA; v3 lowering checked")
	}
	for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
		options := abi.CompileOptions{Stack: stack}
		if stack == abi.StackPlatform {
			options.StackBytesAvailable = 8192
		}
		c, err := compile.Compile(p, options)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Call()
		c.Close()
		if err != nil || got != uintptr(math.Float64bits(830)) {
			t.Fatalf("stack %v: got %#x, err %v", stack, got, err)
		}
	}
}
