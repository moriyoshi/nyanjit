package compile

import (
	"strconv"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func codegenFinish(t *testing.T, b *n.Builder) *ir.Program {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func codegenSum(t *testing.T, bits uint8, args bool) *ir.Program {
	b := n.NewBuilder()
	base, count := b.ParamBits(bits), b.ParamBits(bits)
	constant := func(x uint64) n.Value { return b.ConstBits(bits, x) }
	if args {
		loop, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		widths := []uint8{bits, bits, bits}
		b.JumpArgs(loop, constant(0), base, count)
		s := b.BindParamsBits(loop, widths)
		b.BranchArgs(b.IsZero(s[2]), done, []n.Value{s[0]}, body, s)
		s = b.BindParamsBits(body, widths)
		b.JumpArgs(loop, b.Add(s[0], b.LoadBits(s[1], bits)), b.Add(s[1], constant(uint64(bits/8))), b.Sub(s[2], constant(1)))
		result := b.BindParamsBits(done, []uint8{bits})
		b.Return(result[0])
	} else {
		b.Enter()
		sum, p, k := b.Var(constant(0)), b.Var(base), b.Var(count)
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(sum, b.Add(b.Get(sum), b.LoadBits(b.Get(p), bits)))
		b.Set(p, b.Add(b.Get(p), constant(uint64(bits/8))))
		b.Set(k, b.Sub(b.Get(k), constant(1)))
		b.Jump(loop)
		b.Bind(done)
		result := b.Get(sum)
		b.Leave(result)
		b.Return(result)
	}
	return codegenFinish(t, b)
}
func listingRows(text string) []string {
	rows := []string{}
	for _, line := range strings.Split(text, "\n") {
		_, tail, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if !ok || strings.HasPrefix(strings.TrimSpace(tail), ".") {
			continue
		}
		rows = append(rows, strings.TrimSpace(tail))
	}
	return rows
}
func listingLoop(t *testing.T, text string) []string {
	t.Helper()
	lines := strings.Split(text, "\n")
	pcs := []int{}
	rows := []string{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		address, tail := fields[0], strings.Join(fields[1:], " ")
		pc, err := strconv.ParseInt(strings.TrimSuffix(address, ":"), 16, 64)
		if err != nil || strings.HasPrefix(strings.TrimSpace(tail), ".") {
			continue
		}
		pcs = append(pcs, int(pc))
		rows = append(rows, strings.TrimSpace(tail))
	}
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		branch := strings.HasPrefix(row, "jmp ") || strings.HasPrefix(row, "b ") || strings.HasPrefix(row, "j ") || strings.HasPrefix(row, "jal x0, ")
		if !branch {
			continue
		}
		_, literal, ok := strings.Cut(row, "0x")
		if !ok {
			continue
		}
		target, err := strconv.ParseInt(literal, 16, 64)
		if err != nil || int(target) >= pcs[i] {
			continue
		}
		for j, pc := range pcs {
			if pc == int(target) {
				return rows[j : i+1]
			}
		}
	}
	t.Fatalf("no backward loop in:\n%s", text)
	return nil
}
func TestVarSumListingMatchesLabelLoop(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le", "ppc64"} {
		t.Run(arch, func(t *testing.T) {
			o := abi.CompileOptions{Architecture: arch, Stack: abi.StackPlatform, StackBytesAvailable: 8192}
			sum, args := codegenSum(t, 64, false), codegenSum(t, 64, true)
			listing, err := EmitAssembly(sum, o)
			if err != nil {
				t.Fatal(err)
			}
			other, err := EmitAssembly(args, o)
			if err != nil {
				t.Fatal(err)
			}
			body, reference := listingLoop(t, listing), listingLoop(t, other)
			if len(body) > len(reference) {
				t.Fatalf("Var loop %d > label loop %d:\n%s", len(body), len(reference), listing)
			}
			for _, row := range body {
				if strings.Contains(row, "SP") || strings.Contains(row, "rsp") || strings.Contains(row, "(x2)") || strings.Contains(row, "(r1)") {
					t.Fatalf("loop spill: %s", row)
				}
				if strings.HasPrefix(row, "mov ") && !strings.Contains(row, "[") || strings.HasPrefix(row, "mr ") || strings.HasPrefix(row, "mv ") {
					t.Fatalf("home copy: %s", row)
				}
				if strings.Contains(row, "0x0") || strings.Contains(row, "#0x0") {
					t.Fatalf("dead zero: %s", row)
				}
			}
			input, err := inputFromProgram(sum, o)
			if err != nil {
				t.Fatal(err)
			}
			_, report, err := lowerControlProgram(input, o, emitText)
			if err != nil {
				t.Fatal(err)
			}
			if report.StackBytes != 0 || report.WorkspaceBytes != 0 {
				t.Fatalf("reserved slots forced storage: %+v", report)
			}
			if arch == "arm64" && !strings.Contains(listing, "cbz ") {
				t.Fatal("Get lost CBZ")
			}
		})
	}
}
func TestFPVarAndLabelHomes(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le", "ppc64"} {
		o := abi.CompileOptions{Architecture: arch, Stack: abi.StackPlatform, StackBytesAvailable: 8192}
		b := n.NewBuilder()
		v := b.Var(b.Float64(1))
		loop, done := b.NewLabel(), b.NewLabel()
		k := b.Var(b.ConstBits(64, 2))
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.FAdd(b.Get(v), b.Float64(1)))
		b.Set(k, b.Sub(b.Get(k), b.ConstBits(64, 1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(v))
		p := codegenFinish(t, b)
		plan := planVariableHomes(p, newMachineTarget(o))
		if !plan.FloatHomes[0] || plan.Regions[0][0].Register < 0 || !machine.IsVectorRegister(physReg(plan.Regions[0][0].Register)) {
			t.Fatalf("%s not FP home: %+v", arch, plan)
		}
		listing, err := EmitAssembly(p, o)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range listingLoop(t, listing) {
			if strings.Contains(row, "movq") || strings.Contains(row, "fmov") || strings.Contains(row, "fmv.") || strings.Contains(row, "mtvsrd") || strings.Contains(row, "mfvsrd") {
				t.Fatalf("%s bank copy inside FP Var loop: %s", arch, row)
			}
		}
		b = n.NewBuilder()
		label := b.NewLabel()
		b.JumpArgs(label, b.Float64(1))
		x := b.BindParamsBits(label, []uint8{64})
		b.Return(b.FAdd(x[0], b.Float64(1)))
		p = codegenFinish(t, b)
		if !floatingLabelParams(p)[label][0] {
			t.Fatalf("%s label missing FP inference", arch)
		}
		if _, err := EmitAssembly(p, o); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCleanOuterHomeNotWrittenAtLeave(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le", "ppc64"} {
		b := n.NewBuilder()
		vars := []n.Var{}
		for i := 0; i < 28; i++ {
			vars = append(vars, b.Var(b.ConstBits(64, uint64(i+1))))
		}
		b.Enter()
		b.Var(b.ConstBits(64, 123))
		result := b.Get(vars[0])
		b.Leave(result)
		b.Return(result)
		p := codegenFinish(t, b)
		o := abi.CompileOptions{Architecture: arch, Stack: abi.StackPlatform, StackBytesAvailable: 8192}
		input, err := inputFromProgram(p, o)
		if err != nil {
			t.Fatal(err)
		}
		target := newMachineTarget(o)
		plan := input.homes
		old, inner := plan.Regions[0][0], plan.Regions[1][0]
		if old.Register < 0 || inner.Register < 0 || old.Register == inner.Register {
			t.Fatalf("%s test needs a moved live outer home: %+v %+v", arch, old, inner)
		}
		selected, err := selectProgramResultParts(input.code, input.leaves, input.arity, input.result, input.resultParts, target)
		if err != nil {
			t.Fatal(err)
		}
		bindVariableHomes(&selected, input.source, 0, plan)
		physical, _, _, err := allocateAndEmit(selected, target, newMachineEmitter(o))
		if err != nil {
			t.Fatal(err)
		}
		stores := 0
		for _, i := range physical.insns {
			if i.ins.Op == pSpill && i.ins.Slot == old.Slot {
				stores++
			}
		}
		if stores != 1 {
			t.Fatalf("%s clean outer Var written %d times instead of once at Enter", arch, stores)
		}
	}
}

func TestPreservedHomesRestoreWithWorkspace(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le", "ppc64"} {
		b := n.NewBuilder()
		vars := []n.Var{}
		for i := 0; i < 28; i++ {
			vars = append(vars, b.Var(b.ConstBits(64, uint64(i+1))))
		}
		done := b.NewLabel()
		b.Jump(done)
		b.Bind(done)
		b.Return(b.Get(vars[0]))
		p := codegenFinish(t, b)
		o := abi.CompileOptions{Architecture: arch, Stack: abi.StackNoUse}
		input, err := inputFromProgram(p, o)
		if err != nil {
			t.Fatal(err)
		}
		if len(input.homes.AdditionalRegisters) == 0 {
			t.Fatalf("%s did not allocate preserved registers", arch)
		}
		artifact, report, err := lowerControlProgram(input, o, emitText)
		if err != nil {
			t.Fatal(err)
		}
		if report.StackBytes != 0 || report.WorkspaceBytes == 0 {
			t.Fatalf("%s bad workspace preservation report:%+v", arch, report)
		}
		target := newMachineTarget(o)
		reg := input.homes.AdditionalRegisters[0]
		name := target.RegisterName(reg)
		if arch == "riscv64" || arch == "ppc64" || arch == "ppc64le" {
			name = strings.ToLower(name)
		}
		restored := false
		for _, row := range strings.Split(artifact.assembly, "\n") {
			if arch == "amd64" {
				restored = restored || strings.Contains(row, "mov "+name+", qword ptr [R10")
			}
			if arch == "arm64" {
				restored = restored || strings.Contains(row, "ldr "+name+", [X8")
			}
			if arch == "riscv64" || arch == "ppc64" || arch == "ppc64le" {
				restored = restored || strings.Contains(row, "ld "+name+",")
			}
		}
		if !restored {
			t.Fatalf("%s preserved %s has no restore on workspace-only return:\n%s", arch, name, artifact.assembly)
		}
	}
}

func TestUnusedLabelSlotsStillReserveEdgeStorage(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le", "ppc64"} {
		o := abi.CompileOptions{Architecture: arch, Stack: abi.StackPlatform, StackBytesAvailable: 8192}
		count := len(newMachineTarget(o).Registers()) + 2
		b := n.NewBuilder()
		label := b.NewLabel()
		args := []n.Value{}
		widths := []uint8{}
		for i := 0; i < count; i++ {
			args = append(args, b.ConstBits(64, uint64(i+1)))
			widths = append(widths, 64)
		}
		b.JumpArgs(label, args...)
		b.BindParamsBits(label, widths)
		b.Return(b.ConstBits(64, 7))
		p := codegenFinish(t, b)
		input, err := inputFromProgram(p, o)
		if err != nil {
			t.Fatal(err)
		}
		_, report, err := lowerControlProgram(input, o, emitText)
		if err != nil {
			t.Fatal(err)
		}
		if report.StackBytes == 0 {
			t.Fatalf("%s unused incoming params still have edge writes requiring a frame", arch)
		}
		o.Stack = abi.StackNoUse
		o.StackBytesAvailable = 0
		input, err = inputFromProgram(p, o)
		if err != nil {
			t.Fatal(err)
		}
		_, report, err = lowerControlProgram(input, o, emitText)
		if err != nil {
			t.Fatal(err)
		}
		if report.StackBytes != 0 || report.WorkspaceBytes < 16 {
			t.Fatalf("%s bad edge workspace:%+v", arch, report)
		}
	}
}
