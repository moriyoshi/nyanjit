package compile_test

import (
	"errors"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestStackNoUseCallsIndependentOfControlFlow(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"} {
		for _, kind := range []string{"C", "Go", "syscall"} {
			for _, loop := range []bool{false, true} {
				b := n.NewBuilder()
				var counter n.Var
				var again, done n.Label
				if loop {
					counter = b.Var(b.ConstBits(32, 1))
					again, done = b.NewLabel(), b.NewLabel()
					b.Jump(again)
					b.Bind(again)
				}
				target := b.ConstNative(12345)
				var result n.Value
				switch kind {
				case "C":
					result = b.CallC(target)
				case "Go":
					result = b.CallGo(target)
				case "syscall":
					// Syscall's scalar contract uses 64 bits even on 386.
					result = b.Syscall(b.Const(39))
				}
				if loop {
					b.Set(counter, b.Sub(b.Get(counter), b.ConstBits(32, 1)))
					b.Branch(b.IsZero(b.Get(counter)), done)
					b.Jump(again)
					b.Bind(done)
					result = b.Const(42)
				}
				b.Return(result)
				p, err := testFinishRaw(b)
				if err != nil {
					t.Fatal(err)
				}
				spec := compile.TargetSpec{Architecture: arch}
				opts := compile.Func{ABI: abi.Go, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}
				_, err = lowerListing(p, spec, opts)
				if !errors.Is(err, compile.ErrStackRequired) {
					t.Errorf("%s %s loop%v: %v, want ErrStackRequired", arch, kind, loop, err)
				}
				// The builder requires a host-native function pointer. A 64-bit
				// host cannot construct a valid 386 call target for the positive case.
				if kind == "C" && (arch != "386" || builder.NativeBits == 32) {
					opts.Stack = compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}
					if _, err := lowerListing(p, spec, opts); err != nil {
						t.Errorf("%s C loop%v StackPlatform: %v", arch, loop, err)
					}
				}
			}
		}
	}
}
