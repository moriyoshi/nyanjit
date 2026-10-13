//go:build linux

package compile_test

import (
	"errors"
	"runtime"
	"slices"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/abicheck"
)

// pressurePreserved lists C-preserved registers that register pressure
// allocates in a Go ABI entry. 386 allocates none of them.
var pressurePreserved = map[string][]string{
	"amd64":   {"RBX", "R12", "R13", "R15"},
	"arm64":   {"X19", "X20", "X21", "X22", "X23", "X24", "X25", "X26", "X27"},
	"ppc64le": {"R14", "R15", "R16", "R17", "R18", "R19", "R20", "R21", "R22", "R23", "R24", "R25", "R26", "R27", "R28", "R31"},
	"riscv64": {"X18", "X19", "X20", "X21", "X22", "X23", "X24", "X25", "X26"},
	"loong64": {"R24", "R25", "R26", "R27", "R28", "R31"},
}

func TestManagedLeafPreservesAllocatedRegisters(t *testing.T) {
	b := n.NewBuilder()
	x := b.Param()
	const count = 30
	vars := make([]n.Var, count)
	for k := range vars {
		vars[k] = b.Var(b.Add(x, b.Const(uint64(k))))
	}
	loop, done := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	values := make([]n.Value, count)
	for k := range vars {
		values[k] = b.Get(vars[k])
	}
	for k := range vars {
		b.Set(vars[k], b.Add(b.Mul(values[k], b.Const(3)), values[(k+1)%count]))
	}
	b.Jump(done)
	b.Bind(done)
	sum := b.Get(vars[0])
	for _, v := range vars[1:] {
		sum = b.Add(sum, b.Get(v))
	}
	b.Return(sum)
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	fn := compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
	// Go entry reports the registers used by the same allocation without C
	// preservation hiding their writes from the report.
	report, err := lowerReport(p, compile.TargetSpec{}, fn)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range pressurePreserved[runtime.GOARCH] {
		if !slices.Contains(report.ClobberedRegisters(), name) {
			t.Fatalf("pressure did not allocate %s: %v", name, report.ClobberedRegisters())
		}
	}
	fn.ABI = abi.C
	native := fn
	native.Context = compile.ContextNative
	workspace := native
	workspace.Stack = compile.StackPolicy{Practice: abi.StackNoUse}
	for name, cfn := range map[string]compile.Func{"managed": fn, "native": native, "workspace": workspace} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := lowerTemplate(p, compile.TargetSpec{}, cfn)
			if runtime.GOARCH == "386" && cfn.Context == compile.ContextGoManaged && errors.Is(err, compile.ErrBackendLimit) {
				t.Skip("the managed frame exceeds the 386 stack limit:", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			res, err := abicheck.CheckPreserved(tmpl, execution.Words(uint64(11))...)
			if cfn.Context == compile.ContextGoManaged && !goregister.Supported() {
				if !errors.Is(err, abicheck.ErrUnsupported) {
					t.Fatalf("managed entry without goregister: got %v, want ErrUnsupported", err)
				}
				t.Skip("Go-managed entries need goregister")
			}
			if r := tmpl.Report(); runtime.GOARCH == "386" && r.StackBytes()+r.CallStackBytes() > 240 {
				// A 386 NOSPLIT caller cannot host this frame.
				if !errors.Is(err, abicheck.ErrUnsupported) {
					t.Fatalf("%d-byte frame: got %v, want ErrUnsupported", r.StackBytes(), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := uintptr(4 * (count*11 + count*(count-1)/2))
			if res.Return != want {
				t.Fatalf("got %d want %d", res.Return, want)
			}
			if len(res.Clobbered) != 0 {
				t.Fatalf("callee-preserved register, g or SP changed: %v", res.Clobbered)
			}
		})
	}
}
