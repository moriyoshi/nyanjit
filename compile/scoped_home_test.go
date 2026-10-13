package compile

import (
	"github.com/moriyoshi/nyanjit/abi"
	b "github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func homeScopeProgram(t *testing.T, pressure bool, bits uint8) *ir.Program {
	x := b.NewBuilder()
	outer := []b.Var{}
	count := 1
	if pressure {
		count = 24
	}
	for i := 0; i < count; i++ {
		v, _ := x.ConstBits(bits, uint64(i+1))
		h, e := x.Var(v)
		if e != nil {
			t.Fatal(e)
		}
		outer = append(outer, h)
	}
	x.Enter()
	for i := 0; i < count; i++ {
		v, _ := x.ConstBits(bits, uint64(i))
		if _, e := x.Var(v); e != nil {
			t.Fatal(e)
		}
	}
	x.Leave()
	v, _ := x.Get(outer[0])
	x.Return(v)
	p, e := x.Finish()
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestUnusedOuterHomesStayUntilPressure(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "ppc64", "ppc64le"} {
		bits := uint8(64)
		if arch == "386" {
			bits = 32
		}
		options := abi.CompileOptions{Architecture: arch, Stack: abi.StackPlatform, StackBytesAvailable: 8192}
		for _, pressure := range []bool{false, true} {
			p := homeScopeProgram(t, pressure, bits)
			target := newMachineTarget(options)
			plan := planVariableHomes(p, target)
			root, inner := plan.Regions[0][0], plan.Regions[1][0]
			if root.Register < 0 {
				t.Fatalf("%s root var must begin in a register", arch)
			}
			if pressure && inner.Register >= 0 {
				t.Fatalf("%s unused outer home must spill under pressure:%+v", arch, inner)
			}
			if !pressure && inner.Register != root.Register {
				t.Fatalf("%s spare capacity must retain outer home:%+v %+v", arch, root, inner)
			}
			input, e := inputFromProgram(p, options)
			if e != nil {
				t.Fatal(e)
			}
			program, e := selectProgramResultParts(input.code, input.leaves, input.arity, input.result, input.resultParts, target)
			if e != nil {
				t.Fatal(e)
			}
			bindVariableHomes(&program, input.source, 0, input.homes)
			physical, _, _, e := allocateAndEmit(program, target, newMachineEmitter(options))
			if e != nil {
				t.Fatal(e)
			}
			saves, reloads := 0, 0
			for _, at := range physical.insns {
				if int(at.ins.A) == root.Register && at.ins.Slot == root.Slot {
					if at.ins.Op == pSpill {
						saves++
					}
					if at.ins.Op == pReload {
						reloads++
					}
				}
			}
			if pressure && (saves == 0 || reloads == 0) {
				t.Fatalf("%s missing boundary spill/restore", arch)
			}
			if !pressure && (saves != 0 || reloads != 0) {
				t.Fatalf("%s needless outer spill (%d/%d)", arch, saves, reloads)
			}
		}
	}
}

func TestAMD64VariableHomesExcludeFixedExpansionRegisters(t *testing.T) {
	for _, cpu := range []abi.CPUProfile{{Level: "x86-64-v1"}, {Level: "x86-64-v3"}} {
		options := abi.CompileOptions{Architecture: "amd64", Stack: abi.StackPlatform, StackBytesAvailable: 8192, CPU: cpu}
		p := homeScopeProgram(t, true, 64)
		p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpMulHigh, Bits: 64, A: 0, B: 0}, ir.Instruction{Op: ir.OpShiftLeftBy, Bits: 64, A: 0, B: 0})
		p.Result = len(p.Instructions) - 1
		plan := planVariableHomes(p, newMachineTarget(options))
		for region, homes := range plan.Regions {
			for id, home := range homes {
				switch home.Register {
				case 0, 1, 2, 10, 11:
					t.Fatalf("region %d Var %d overlaps expansion register %d", region, id, home.Register)
				}
			}
		}
	}
}

// Parameter homes occur late in the candidate order. A smaller capacity must
// not truncate those candidates and move an outer home that still fits.
func TestOuterParameterHomeRetainedWithSpareCapacity(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64", "ppc64le"} {
		bits := uint8(64)
		if arch == "386" {
			bits = 32
		}
		x := b.NewBuilder()
		p0, _ := x.ParamBits(bits)
		v, _ := x.Var(p0)
		x.Enter()
		one, _ := x.ConstBits(bits, 1)
		w, _ := x.Var(one)
		a, _ := x.Get(w)
		r, _ := x.Add(a, one)
		x.Leave(r)
		out, _ := x.Get(v)
		x.Return(out)
		p, err := x.Finish()
		if err != nil {
			t.Fatal(err)
		}
		plan := planVariableHomes(p, newMachineTarget(abi.CompileOptions{Architecture: arch}))
		if plan.Regions[0][0].Register < 0 || plan.Regions[0][0].Register != plan.Regions[1][0].Register {
			t.Fatalf("%s moved outer parameter home despite spare capacity: %+v", arch, plan.Regions)
		}
	}
}
