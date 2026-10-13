package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
)

func TestRegisterSetsCannotChangeCompilation(t *testing.T) {
	type policy interface {
		Architecture() string
		Registers() backend.RegisterSet
		PhysicalRegisters() backend.RegisterSet
	}
	profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
	if err != nil {
		t.Fatal(err)
	}
	ppc, err := ppc64.NewPolicy(profile, abi.StackPlatform)
	if err != nil {
		t.Fatal(err)
	}
	policies := []policy{amd64.NewPolicy(abi.C, abi.StackPlatform), arm64.NewPolicy(abi.C, abi.StackPlatform), i386.NewPolicy(abi.C, abi.StackPlatform), loong64.NewPolicy(abi.C, abi.StackPlatform), ppc, riscv64.NewPolicy(abi.C, abi.StackPlatform)}
	for _, p := range policies {
		t.Run(p.Architecture(), func(t *testing.T) {
			spec := compile.TargetSpec{Architecture: p.Architecture()}
			opts := compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
			for _, program := range []struct {
				name string
				fp   bool
			}{{"gp", false}, {"fp", true}} {
				input := workload.Small(1)
				if program.fp {
					input = workload.FP(8)
				}
				before, err := lowerListing(input, spec, opts)
				if err != nil {
					t.Fatal(err)
				}
				for _, get := range []func() backend.RegisterSet{p.Registers, p.PhysicalRegisters, func() backend.RegisterSet { return machine.VectorRegisters(p.Architecture()) }} {
					original := get()
					copyValue := original
					copyValue = backend.RegisterSet{}
					_ = copyValue
					out := original.AppendTo(make([]backend.Register, 0, 64))
					for i := range out {
						out[i] = 255
					}
					_ = append(out, 255)
					if get() != original {
						t.Fatal("register set changed")
					}
					var storage [64]backend.Register
					if n := testing.AllocsPerRun(100, func() {
						s := get()
						for range s.All() {
						}
						_ = s.AppendTo(storage[:0])
						if s.Len() > 0 {
							_ = s.At(0)
						}
					}); n != 0 {
						t.Fatalf("register query allocations %g", n)
					}
				}
				after, err := lowerListing(input, spec, opts)
				if err != nil || after != before {
					t.Fatalf("%s listing changed: %v", program.name, err)
				}
			}
		})
	}
}
