package compile_test

import (
	"errors"
	"testing"
	"time"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func carrylessMulChain(count int) (*ir.SealedProgram, error) {
	b := n.NewBuilder()
	x := b.Param()
	v := b.Splat(ir.Vec(2, 64), x)
	for range count {
		v = b.CarrylessMul(v, v, 0, 1)
	}
	b.Return(b.ExtractLane(v, 0))
	return b.Finish()
}

// Without a native carryless multiply each operation lowers to about 500
// scalar instructions; legalization stops at a budget proportional to the
// program instead of growing compile memory by megabytes per operation
// (security audit 2026-10-25, F19).
func TestCarrylessMulExpansionIsBudgeted(t *testing.T) {
	small, err := carrylessMulChain(32)
	if err != nil {
		t.Fatal(err)
	}
	large, err := carrylessMulChain(2000)
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"arm64", "riscv64", "386"} {
		target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
		if err != nil {
			t.Fatal(err)
		}
		f := compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
		if _, err := compile.Lower(target, small, f); err != nil {
			t.Fatalf("%s: 32 carryless multiplies: %v", arch, err)
		}
		start := time.Now()
		if _, err := compile.Lower(target, large, f); !errors.Is(err, compile.ErrBackendLimit) {
			t.Fatalf("%s: 2000 carryless multiplies: %v, want ErrBackendLimit", arch, err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("%s: rejecting took %v", arch, elapsed)
		}
	}
}
