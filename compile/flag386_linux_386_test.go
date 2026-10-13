//go:build linux && 386

package compile_test

import (
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestI386FlagsAcrossCalls branches on arithmetic computed before a C call
// or a system call: the call clobbers the flags, so the branch must test the
// value again. The leaves return 42 and leave ZF set (and SF, CF clear), or
// SF and CF set (and ZF clear), the opposite of what the arithmetic left.
func TestI386FlagsAcrossCalls(t *testing.T) {
	leaves := map[string][]byte{
		"zero": {0xb8, 42, 0, 0, 0, 0x31, 0xd2, 0xc3}, // mov eax, 42; xor edx, edx; ret
		// mov eax, 42; xor edx, edx; dec edx; stc; mov edx, 0; ret
		"ones": {0xb8, 42, 0, 0, 0, 0x31, 0xd2, 0x4a, 0xf9, 0xba, 0, 0, 0, 0, 0xc3},
	}
	type check struct {
		name string
		// arith returns s, computed before the call, and the predicate on
		// it tested after.
		arith func(b *n.Builder, x n.Value) (n.Value, n.Predicate)
		want  func(x uint32) bool
	}
	checks := []check{
		{"inc-zero", func(b *n.Builder, x n.Value) (n.Value, n.Predicate) {
			s := b.Add(x, b.ConstBits(32, 1))
			return s, b.IsZero(s)
		}, func(x uint32) bool { return x+1 == 0 }},
		{"dec-sign", func(b *n.Builder, x n.Value) (n.Value, n.Predicate) {
			s := b.Sub(x, b.ConstBits(32, 1))
			return s, b.CompareImmediate(s, ir.BranchLess, 0)
		}, func(x uint32) bool { return int32(x-1) < 0 }},
		{"add-carry", func(b *n.Builder, x n.Value) (n.Value, n.Predicate) {
			s := b.Add(x, b.ConstBits(32, 0x80000000))
			return s, b.Compare(s, ir.BranchBelow, x)
		}, func(x uint32) bool { return x+0x80000000 < x }},
		{"sub-borrow", func(b *n.Builder, x n.Value) (n.Value, n.Predicate) {
			s := b.Sub(x, b.ConstBits(32, 7))
			return s, b.CompareImmediate(x, ir.BranchBelow, 7)
		}, func(x uint32) bool { return x < 7 }},
	}
	inputs := []uint32{0, 1, 6, 7, 0x7fffffff, 0x80000000, 0xfffffffe, 0xffffffff}
	for leafName, code := range leaves {
		leaf, err := execution.NewCode(code, execution.CodeOptions{ABI: abi.C}, nil)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := leaf.EntryAddress()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range checks {
			for _, call := range []string{"call", "syscall"} {
				for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
					build := func(b *n.Builder) {
						x := b.ParamBits(32)
						s, p := c.arith(b, x)
						// The call takes s, so s is computed before it.
						var r n.Value
						if call == "call" {
							r = b.CallC(b.ConstNative(entry), s)
						} else {
							r = b.Syscall(b.Const(20), b.CastUnsigned(s, 64)) // getpid
						}
						yes := b.NewLabel()
						b.Branch(p, yes)
						b.Return(b.Add(b.CastUnsigned(r, 32), b.ConstBits(32, 1<<24)))
						b.Bind(yes)
						b.Return(b.Add(b.CastUnsigned(r, 32), b.ConstBits(32, 2<<24)))
					}
					var fn *execution.Code
					if call == "call" {
						b := n.NewBuilder()
						build(b)
						prog, err := testFinishRaw(b)
						if err != nil {
							t.Fatal(err)
						}
						if fn, err = n.Compile(prog, compile.TargetSpec{Tier: tier}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}); err != nil {
							t.Fatal(err)
						}
					} else {
						// External code, mapped here as raw bytes.
						u := lower386(t, tier, build, sys386External)
						template, err := u.Template()
						if err != nil {
							t.Fatal(err)
						}
						code, err := template.Bytes(nil)
						if err != nil {
							t.Fatal(err)
						}
						r := u.Report()
						if fn, err = execution.NewCode(code, execution.CodeOptions{ABI: r.ABI(), Arity: 1, ArgumentWords: 1, StackBytes: r.StackBytes() + r.CallStackBytes()}, nil); err != nil {
							t.Fatal(err)
						}
					}
					for _, in := range inputs {
						got, err := fn.Call(uintptr(in))
						base := uint32(2 << 24)
						if !c.want(in) {
							base = 1 << 24
						}
						if call == "call" && (err != nil || uint32(got) != 42+base) || call == "syscall" && (err != nil || uint32(got)-base > 1<<22) {
							t.Errorf("%s/%s/%s/%d: f(%#x) = %d, %v; want base %d", leafName, c.name, call, tier, in, got, err, base)
						}
					}
					fn.Close()
				}
			}
		}
		leaf.Close()
	}
}
