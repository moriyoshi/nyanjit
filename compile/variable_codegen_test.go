package compile

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func codegenFinish(t *testing.T, b *builder.Builder) *ir.Program {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p.Clone()
}

// codegenSum sums count words at base. With args it is the raw-IR loop
// with the state in block parameters, the reference for the Var loop.
func codegenSum(t *testing.T, bits uint8, args bool) *ir.Program {
	if args {
		pb := ir.NewProgramBuilder()
		entry, typ := pb.Entry(), ir.Scalar(bits)
		base, count := pb.Param(entry, typ), pb.Param(entry, typ)
		constant := func(block ir.Block, x uint64) int {
			return pb.Append(block, ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: x})
		}
		loop, body, done := pb.NewBlock(typ, typ, typ), pb.NewBlock(typ, typ, typ), pb.NewBlock(typ)
		pb.Jump(entry, loop, constant(entry, 0), base, count)
		s := []int{pb.BlockParam(loop, 0), pb.BlockParam(loop, 1), pb.BlockParam(loop, 2)}
		pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: s[2]}, done, []int{s[0]}, body, s)
		s = []int{pb.BlockParam(body, 0), pb.BlockParam(body, 1), pb.BlockParam(body, 2)}
		load := ir.Instruction{Op: ir.OpLoad64, A: s[1], ByteOrder: ir.ByteOrderLittleEndian}
		if bits == 32 {
			load.Op, load.Bits = ir.OpLoad32, 32
		}
		sum := pb.Append(body, ir.Instruction{Op: ir.OpAdd, Bits: bits, A: s[0], B: pb.Append(body, load)})
		next := pb.Append(body, ir.Instruction{Op: ir.OpAdd, Bits: bits, A: s[1], B: constant(body, uint64(bits/8))})
		left := pb.Append(body, ir.Instruction{Op: ir.OpSub, Bits: bits, A: s[2], B: constant(body, 1)})
		pb.Jump(body, loop, sum, next, left)
		pb.Return(done, pb.BlockParam(done, 0))
		sealed, err := pb.Seal()
		if err != nil {
			t.Fatal(err)
		}
		return sealed.Clone()
	}
	b := builder.NewBuilder()
	base, count := must(b.ParamBits(bits)), must(b.ParamBits(bits))
	constant := func(x uint64) builder.Value { return must(b.ConstBits(bits, x)) }
	{
		mustDo(b.Enter())
		sum, p, k := must(b.Var(constant(0))), must(b.Var(base)), must(b.Var(count))
		loop, done := b.NewLabel(), b.NewLabel()
		mustDo(b.Jump(loop))
		mustDo(b.Bind(loop))
		mustDo(b.Branch(must(b.IsZero(must(b.Get(k)))), done))
		mustDo(b.Set(sum, must(b.Add(must(b.Get(sum)), must(b.LoadBits(must(b.Get(p)), bits))))))
		mustDo(b.Set(p, must(b.Add(must(b.Get(p)), constant(uint64(bits/8))))))
		mustDo(b.Set(k, must(b.Sub(must(b.Get(k)), constant(1)))))
		mustDo(b.Jump(loop))
		mustDo(b.Bind(done))
		result := must(b.Get(sum))
		mustDo(b.Leave(result))
		mustDo(b.Return(result))
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
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le"} {
		t.Run(arch, func(t *testing.T) {
			spec := TargetSpec{Architecture: arch}
			f := Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}
			o := mustTarget(t, spec).options(f)
			sum, args := codegenSum(t, 64, false), codegenSum(t, 64, true)
			listing, err := lowerListing(sum, spec, f)
			if err != nil {
				t.Fatal(err)
			}
			other, err := lowerListing(args, spec, f)
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
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le"} {
		spec := TargetSpec{Architecture: arch}
		f := Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}
		b := builder.NewBuilder()
		v := must(b.Var(must(b.Float64(1))))
		loop, done := b.NewLabel(), b.NewLabel()
		k := must(b.Var(must(b.ConstBits(64, 2))))
		mustDo(b.Jump(loop))
		mustDo(b.Bind(loop))
		mustDo(b.Branch(must(b.IsZero(must(b.Get(k)))), done))
		mustDo(b.Set(v, must(b.FAdd(must(b.Get(v)), must(b.Float64(1))))))
		mustDo(b.Set(k, must(b.Sub(must(b.Get(k)), must(b.ConstBits(64, 1))))))
		mustDo(b.Jump(loop))
		mustDo(b.Bind(done))
		mustDo(b.Return(must(b.Get(v))))
		p := codegenFinish(t, b)
		plan := planVariableHomes(p, newMachineTarget(mustTarget(t, spec).options(f)))
		if !plan.FloatHomes[0] || testVariableHome(t, plan, 0, 0).Register < 0 || !backend.IsVectorRegister(physReg(testVariableHome(t, plan, 0, 0).Register)) {
			t.Fatalf("%s not FP home: %+v", arch, plan)
		}
		listing, err := lowerListing(p, spec, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range listingLoop(t, listing) {
			if strings.Contains(row, "movq") || strings.Contains(row, "fmov") || strings.Contains(row, "fmv.") || strings.Contains(row, "mtvsrd") || strings.Contains(row, "mfvsrd") {
				t.Fatalf("%s bank copy inside FP Var loop: %s", arch, row)
			}
		}
		// A raw-IR block parameter is inferred FP from its FP use.
		pb := ir.NewProgramBuilder()
		label := pb.NewBlock(ir.Scalar(64))
		one := func(block ir.Block) int {
			return pb.Append(block, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: math.Float64bits(1)})
		}
		pb.Jump(pb.Entry(), label, one(pb.Entry()))
		pb.Return(label, pb.Append(label, ir.Instruction{Op: ir.OpFAdd, Bits: 64, A: pb.BlockParam(label, 0), B: one(label)}))
		sealed, err := pb.Seal()
		if err != nil {
			t.Fatal(err)
		}
		p = sealed.Clone()
		target := -1
		for _, step := range p.Control.Steps {
			if !step.IsLabel && step.Term == ir.TermJump {
				target = step.To
			}
		}
		if target < 0 || !newProgramFacts(p).LabelFloating(target)[0] {
			t.Fatalf("%s label missing FP inference", arch)
		}
		if _, err := lowerListing(p, spec, f); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCleanOuterHomeNotWrittenAtLeave(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le"} {
		b := builder.NewBuilder()
		vars := []builder.Var{}
		for i := 0; i < 28; i++ {
			vars = append(vars, must(b.Var(must(b.ConstBits(64, uint64(i+1))))))
		}
		mustDo(b.Enter())
		must(b.Var(must(b.ConstBits(64, 123))))
		// A computed result: a returned Get would keep var 0 in the
		// result register across the scope.
		result := must(b.Add(must(b.Get(vars[0])), must(b.Get(vars[1]))))
		mustDo(b.Leave(result))
		mustDo(b.Return(result))
		p := codegenFinish(t, b)
		o := mustTarget(t, TargetSpec{Architecture: arch}).options(Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
		input, err := inputFromProgram(p, o)
		if err != nil {
			t.Fatal(err)
		}
		target := newMachineTarget(o)
		plan := input.homes
		old, inner := testVariableHome(t, plan, 0, 0), testVariableHome(t, plan, 1, 0)
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
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le"} {
		b := builder.NewBuilder()
		vars := []builder.Var{}
		for i := 0; i < 28; i++ {
			vars = append(vars, must(b.Var(must(b.ConstBits(64, uint64(i+1))))))
		}
		done := b.NewLabel()
		mustDo(b.Jump(done))
		mustDo(b.Bind(done))
		mustDo(b.Return(must(b.Get(vars[0]))))
		p := codegenFinish(t, b)
		o := mustTarget(t, TargetSpec{Architecture: arch}).options(Func{Stack: StackPolicy{Practice: abi.StackNoUse}})
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
		if arch == "riscv64" || arch == "ppc64le" {
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
			if arch == "riscv64" || arch == "ppc64le" {
				restored = restored || strings.Contains(row, "ld "+name+",")
			}
		}
		if !restored {
			t.Fatalf("%s preserved %s has no restore on workspace-only return:\n%s", arch, name, artifact.assembly)
		}
	}
}

func TestUnusedLabelSlotsStillReserveEdgeStorage(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le"} {
		target := mustTarget(t, TargetSpec{Architecture: arch})
		o := target.options(Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
		count := newMachineTarget(o).Registers().Len() + 2
		// Raw IR: an edge passes more values than registers to parameters
		// the target block never uses.
		pb := ir.NewProgramBuilder()
		args := []int{}
		types := []ir.Type{}
		for i := 0; i < count; i++ {
			args = append(args, pb.Append(pb.Entry(), ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: uint64(i + 1)}))
			types = append(types, ir.Scalar(64))
		}
		label := pb.NewBlock(types...)
		pb.Jump(pb.Entry(), label, args...)
		pb.Return(label, pb.Append(label, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 7}))
		sealed, err := pb.Seal()
		if err != nil {
			t.Fatal(err)
		}
		p := sealed.Clone()
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
		o = target.options(Func{Stack: StackPolicy{Practice: abi.StackNoUse}})
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
