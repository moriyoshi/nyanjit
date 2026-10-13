package compile

import (
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestDenseAndUncachedCompactHomePlansMatch(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		t.Run(arch, func(t *testing.T) {
			bits := uint8(64)
			if arch == "386" {
				bits = 32
			}
			options := mustTarget(t, TargetSpec{Architecture: arch}).options(Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
			target := newMachineTarget(options)
			input, err := inputFromProgram(homeScopeProgram(t, true, bits), options)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := selectProgramResultParts(input.code, input.leaves, input.arity, input.result, input.resultParts, target)
			if err != nil {
				t.Fatal(err)
			}
			bindVariableHomes(&selected, input.source, 0, input.homes)
			reference, workspace, writes, err := allocateAndEmit(selected, target, newMachineEmitter(options))
			if err != nil {
				t.Fatal(err)
			}
			uncached := *input.homes
			uncached.RegisterVariables = nil
			uncached.ReservedRegions = nil
			dense := uncached
			dense.RegisterOverrides = nil
			dense.Regions = make(map[int]map[int]machine.Home)
			for region := range input.homes.Parents {
				homes := make(map[int]machine.Home)
				for id := range input.homes.Slots {
					if h, ok := input.homes.Lookup(region, id); ok {
						homes[id] = h
					}
				}
				dense.Regions[region] = homes
			}
			for _, c := range []struct {
				name string
				plan *machine.HomePlan
			}{{"uncached-compact", &uncached}, {"dense", &dense}} {
				t.Run(c.name, func(t *testing.T) {
					selected.HomePlan = c.plan
					got, gotWorkspace, gotWrites, err := allocateAndEmit(selected, target, newMachineEmitter(options))
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got.insns, reference.insns) || got.stackBytes != reference.stackBytes || gotWorkspace != workspace || gotWrites != writes {
						t.Fatal("home plan representation changed allocation, storage or register writes")
					}
				})
			}
		})
	}
}
