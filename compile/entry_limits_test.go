package compile

import (
	"errors"
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestPreflightRegisterScopesEntryArity(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			t.Run(fmt.Sprintf("%s/%d", arch, flavor), func(t *testing.T) {
				limit := abi.MaxArgumentsFor(arch)
				for _, arity := range []int{limit, limit + 1} {
					p := &ir.Program{Arity: arity, Result: 0}
					for i := 0; i < arity; i++ {
						p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpParam, Bits: 32})
					}
					spec, f := TargetSpec{Architecture: arch}, Func{ABI: flavor}
					// RegisterScopes needs a lowered unit; above the limit
					// the lowering itself reports the entry arity.
					u, err := lowerUnit(p, spec, f)
					if err == nil {
						_, err = u.RegisterScopes()
					}
					if arity == limit {
						if err != nil {
							t.Fatalf("at limit: %v", err)
						}
						continue
					}
					_, compileErr := lowerListing(p, spec, f)
					if !errors.Is(err, ErrBackendLimit) || compileErr == nil || err.Error() != compileErr.Error() {
						t.Fatalf("arity %d: preflight=%v; compile=%v", arity, err, compileErr)
					}
				}
			})
		}
	}
}

func TestEntryParametersDoNotOverlapWorkspace(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			t.Run(fmt.Sprintf("%s/%d", arch, flavor), func(t *testing.T) {
				target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: arch}).options(Func{ABI: flavor}))
				workspace, ok := target.(interface{ WorkspaceRegister() physReg })
				if !ok {
					return
				}
				for index := 0; index < abi.MaxArgumentsFor(arch); index++ {
					if register, ok := target.ParamRegister(index); ok && register == workspace.WorkspaceRegister() {
						t.Fatalf("entry parameter %d uses workspace register %s", index, target.RegisterName(register))
					}
				}
			})
		}
	}
}
