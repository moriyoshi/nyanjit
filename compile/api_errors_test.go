//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestLegacyEntryPointErrorIdentityAndOrder(t *testing.T) {
	p := &ir.Program{Result: 0, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 7}}}
	bad := abi.CPUProfile{Level: "invalid-level"}
	cross := "amd64"
	if runtime.GOARCH == cross {
		cross = "arm64"
	}
	managed := Func{Context: ContextGoManaged}
	tests := []struct {
		name    string
		run     func() error
		yes, no []error
		message string
	}{
		// NewTarget resolves the CPU profile before any lowering, so an
		// invalid profile is reported first for every target and context.
		{name: "report cross CPU", run: func() error { _, e := lowerReport(p, TargetSpec{Architecture: cross, CPU: bad}, Func{}); return e }, yes: []error{abi.ErrInvalidSpec, ErrInvalidFunction}, no: []error{ErrUnsupported}},
		{name: "template native CPU", run: func() error { _, e := lowerTemplate(p, TargetSpec{CPU: bad}, Func{}); return e }, yes: []error{abi.ErrInvalidSpec, ErrInvalidFunction}, no: []error{ErrUnsupported}},
		{name: "listing CPU", run: func() error {
			_, e := lowerListing(p, TargetSpec{Architecture: "amd64", CPU: bad}, Func{})
			return e
		}, yes: []error{abi.ErrInvalidSpec, ErrInvalidFunction}},
		{name: "Go-managed pointer assertion and CPU", run: func() error { _, e := lowerTemplate(p, TargetSpec{CPU: bad}, managed); return e }, yes: []error{abi.ErrInvalidSpec, ErrInvalidFunction}, no: []error{ErrPointerMapsRequired}},
		{name: "Go-managed stack and CPU", run: func() error {
			f := managed
			f.AssumePointerFree = true
			_, e := lowerTemplate(p, TargetSpec{CPU: bad}, f)
			return e
		}, yes: []error{abi.ErrInvalidSpec, ErrInvalidFunction}, no: []error{ErrStackRequired}},
		{name: "Go-managed checked boundary CPU", run: func() error {
			f := managed
			f.AssumePointerFree = true
			f.Stack.Practice = abi.StackPlatform
			_, e := lowerTemplate(p, TargetSpec{CPU: bad}, f)
			return e
		}, yes: []error{abi.ErrInvalidSpec, ErrInvalidFunction}},
		{name: "Loong vector validation retains CPU identity", run: func() error {
			_, e := lowerListing(p, TargetSpec{Architecture: "loong64", CPU: abi.CPUProfile{Extensions: []string{"sse2"}}}, Func{})
			return e
		}, yes: []error{abi.ErrInvalidSpec}, no: []error{ErrInvalidFunction}},
		{name: "unknown architecture names target", run: func() error {
			_, e := lowerReport(p, TargetSpec{Architecture: "round5-unknown", CPU: bad}, Func{})
			return e
		}, yes: []error{ErrUnsupported}, no: []error{abi.ErrInvalidSpec}, message: "round5-unknown"},
	}
	// LoongArch has always resolved its CPU in SIMD option validation, even
	// for scalar input. That direct error precedes callable-boundary checks.
	if runtime.GOARCH == "loong64" {
		for n := range tests {
			switch tests[n].name {
			case "template native CPU", "Go-managed pointer assertion and CPU", "Go-managed stack and CPU", "Go-managed checked boundary CPU":
				tests[n].yes = []error{abi.ErrInvalidSpec}
				tests[n].no = []error{ErrInvalidFunction, ErrPointerMapsRequired, ErrStackRequired}
			}
		}
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range c.yes {
				if !errors.Is(err, want) {
					t.Errorf("%v does not wrap %v", err, want)
				}
			}
			for _, unwanted := range c.no {
				if errors.Is(err, unwanted) {
					t.Errorf("%v unexpectedly wraps %v", err, unwanted)
				}
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Errorf("missing target name in %v", err)
			}
		})
	}
}

func TestZeroArityReportsKeepEmptyArgumentRegisters(t *testing.T) {
	p := &ir.Program{Result: 0, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 7}}}
	r, e := lowerReport(p, TargetSpec{}, Func{})
	if e != nil {
		t.Fatal(e)
	}
	if r.ArgumentRegisters() == nil || len(r.ArgumentRegisters()) != 0 {
		t.Fatalf("arity-0 arguments: %#v", r.ArgumentRegisters())
	}
}
