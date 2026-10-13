package abicheck_test

import (
	"errors"
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/abicheck"
	"github.com/moriyoshi/nyanjit/ir"
)

const pressureVars = 30

// pressure keeps pressureVars values live across a loop edge so allocation
// reaches the registers an entry must preserve. It returns
// 4*(pressureVars*x + pressureVars*(pressureVars-1)/2).
func pressure(t *testing.T) ir.Input {
	t.Helper()
	b := n.NewBuilder()
	x := b.Param()
	vars := make([]n.Var, pressureVars)
	for k := range vars {
		vars[k] = b.Var(b.Add(x, b.Const(uint64(k))))
	}
	loop, done := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	values := make([]n.Value, pressureVars)
	for k := range vars {
		values[k] = b.Get(vars[k])
	}
	for k := range vars {
		b.Set(vars[k], b.Add(b.Mul(values[k], b.Const(3)), values[(k+1)%pressureVars]))
	}
	b.Jump(done)
	b.Bind(done)
	sum := b.Get(vars[0])
	for _, v := range vars[1:] {
		sum = b.Add(sum, b.Get(v))
	}
	b.Return(sum)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func lower(p ir.Input, fn compile.Func) (*execution.Template, error) {
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		return nil, err
	}
	u, err := compile.Lower(target, p, fn)
	if err != nil {
		return nil, err
	}
	return u.Template()
}

func TestCheckPreservedPressure(t *testing.T) {
	p := pressure(t)
	want := uintptr(4 * (pressureVars*11 + pressureVars*(pressureVars-1)/2))
	cases := map[string]compile.Func{
		"native/workspace": {Context: compile.ContextNative, Stack: compile.StackPolicy{Practice: abi.StackNoUse}},
		"native/frame":     {Context: compile.ContextNative, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
		// Go-managed entries need a platform frame.
		"managed/frame": {Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true},
	}
	for flavorName, flavor := range map[string]abi.Flavor{"C": abi.C, "Go": abi.Go} {
		for name, fn := range cases {
			fn.ABI = flavor
			t.Run(flavorName+"/"+name, func(t *testing.T) {
				tmpl, err := lower(p, fn)
				if runtime.GOARCH == "386" && fn.Context == compile.ContextGoManaged && errors.Is(err, compile.ErrBackendLimit) {
					t.Skip("the managed frame exceeds the 386 stack limit:", err)
				}
				if err != nil {
					t.Fatal(err)
				}
				r := tmpl.Report()
				res, err := abicheck.CheckPreserved(tmpl, execution.Words(uint64(11))...)
				if fn.Context == compile.ContextGoManaged && !goregister.Supported() {
					if !errors.Is(err, abicheck.ErrUnsupported) {
						t.Fatalf("managed entry without goregister: got %v, want ErrUnsupported", err)
					}
					t.Skip("Go-managed entries need goregister")
				}
				if r.StackBytes()+r.CallStackBytes() > 240 && runtime.GOARCH == "386" {
					// A 386 NOSPLIT caller cannot host this frame.
					if !errors.Is(err, abicheck.ErrUnsupported) {
						t.Fatalf("%d-byte frame: got %v, want ErrUnsupported", r.StackBytes(), err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if res.Return != want || len(res.Clobbered) != 0 {
					t.Fatalf("got %d clobbering %v, want %d", res.Return, res.Clobbered, want)
				}
			})
		}
	}
}

func TestCheckPreservedRejects(t *testing.T) {
	tmpl, err := lower(pressure(t), compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := abicheck.CheckPreserved(tmpl); !errors.Is(err, execution.ErrArity) {
		t.Fatalf("no arguments: got %v, want ErrArity", err)
	}
	if _, err := abicheck.CheckPreserved(nil); !errors.Is(err, abicheck.ErrUnsupported) {
		t.Fatalf("nil template: got %v, want ErrUnsupported", err)
	}
}
