package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// A system call pushes nothing and keeps the link register, so a leaf that
// only makes one needs no frame at either tier, even without stack bytes.
func TestBaselineLeafSyscallNeedsNoFrame(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, args := range []int{0, 3, 6} {
			for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
				target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
				if err != nil {
					t.Fatal(err)
				}
				b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform}})
				n := b.ParamBits(64)
				as := make([]nyanjit.Value, args)
				for i := range as {
					as[i] = n
				}
				b.Return(b.Syscall(n, as...))
				u, err := b.Lower()
				if err != nil {
					t.Fatalf("%s tier %d, %d arguments: %v", arch, tier, args, err)
				}
				r := u.Report()
				if r.StackBytes() != 0 || r.CallStackBytes() != 0 {
					t.Errorf("%s tier %d, %d arguments: stack %d, call stack %d, want none", arch, tier, args, r.StackBytes(), r.CallStackBytes())
				}
			}
		}
	}
}
