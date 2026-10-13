package compile

import (
	"bytes"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Compare the byte and text visitors after EACH physical expansion, including
// branches, literals, frame operations, and bank copies. Final total-size
// equality alone would miss an expansion that shifted a later branch.
func assertUnitOffsets(t *testing.T, u *Unit) {
	t.Helper()
	u.finishLiterals() // as Listing and Template do before reading physical
	text, code := newMachineAssembler(u.options, emitText), newMachineAssembler(u.options, emitNative)
	visit := func(a machineAssembler, i machine.Instruction) error {
		if e, ok := a.(interface {
			emitInstruction(machine.Instruction) error
		}); ok {
			return e.emitInstruction(i)
		}
		return machine.Visit(i, a.(machine.Assembler))
	}
	for index, located := range u.physical.insns {
		if text.AssembledSize() != located.pc || code.AssembledSize() != located.pc {
			t.Fatalf("instruction%d PC%d text%d bytes%d", index, located.pc, text.AssembledSize(), code.AssembledSize())
		}
		text.SetPC(located.pc)
		code.SetPC(located.pc)
		if err := visit(text, located.ins); err != nil {
			t.Fatal(err)
		}
		if err := visit(code, located.ins); err != nil {
			t.Fatal(err)
		}
		if text.AssembledSize() != code.AssembledSize() {
			t.Fatalf("instruction%d ends text%d bytes%d", index, text.AssembledSize(), code.AssembledSize())
		}
	}
	listing, err := u.Listing()
	if err != nil || listing != text.artifact().assembly {
		t.Fatalf("unit listing differs from its physical visitor: %v", err)
	}
	if code.AssembledSize() != u.Report().CodeBytes() {
		t.Fatal("report byte count differs")
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == u.report.Architecture {
		emission, err := u.encoded()
		if err != nil || !bytes.Equal(emission.Bytes, code.artifact().code) {
			t.Fatalf("unit encoding differs from listed offsets: %v", err)
		}
	}
}

// checkUnitReportListingAreLazyAndDetached runs for
// TestUnitReportListingAreLazyAndDetached in package compile_test, which
// passes a clone of workload.Chain(40).
func checkUnitReportListingAreLazyAndDetached(t *testing.T, p *ir.Program) {
	t.Helper()
	spec := TargetSpec{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"bmi2"}}}
	if runtime.GOARCH == "amd64" {
		spec = TargetSpec{Architecture: "riscv64", CPU: abi.CPUProfile{Extensions: []string{"zbb"}}}
	}
	u, err := lowerUnit(p, spec, Func{})
	if err != nil {
		t.Fatal(err)
	}
	stats := u.Report().LoweringStats()
	p.Instructions[0].Op = ir.Opcode(255)
	spec.CPU.Extensions[0] = "invalid"
	report := u.Report()
	if clobbered := report.ClobberedRegisters(); len(clobbered) != 0 {
		clobbered[0] = "mutated"
	}
	if _, err := u.Listing(); err != nil {
		t.Fatal(err)
	}
	if len(u.emission.Bytes) != 0 || len(u.emission.goUnwind.SP) != 0 || u.executable != nil {
		t.Fatal("report/listing encoded or registered output")
	}
	if !slices.Equal(u.report.ClobberedRegisters, u.Report().ClobberedRegisters()) || stats != u.Report().LoweringStats() {
		t.Fatal("output requests changed lowering facts")
	}
	if runtime.GOARCH != spec.Architecture {
		if _, err := u.Template(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("cross template error: %v", err)
		}
	}
}

// checkUnitConcurrentOutputs runs for TestUnitConcurrentOutputs in package
// compile_test, which passes workload.Chain(40).
func checkUnitConcurrentOutputs(t *testing.T, p *ir.SealedProgram) {
	t.Helper()
	u, err := lowerUnit(p, TargetSpec{}, Func{})
	if err != nil {
		t.Fatal(err)
	}
	stats := u.Report().LoweringStats()
	var wg sync.WaitGroup
	templates := make(chan *execution.Template, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := u.Listing(); err != nil {
				t.Error(err)
			}
			template, err := u.Template()
			if err != nil {
				t.Error(err)
				return
			}
			templates <- template
			u.Report()
		}()
	}
	wg.Wait()
	close(templates)
	var first *execution.Template
	for template := range templates {
		if first == nil {
			first = template
		} else if first != template {
			t.Fatal("repeated encoding created another template")
		}
	}
	if stats != u.Report().LoweringStats() || len(u.emission.goUnwind.SP) != 0 {
		t.Fatal("outputs reallocated blocks or built unnecessary unwind metadata")
	}
}

func TestUnitManagedGatewayOffsets(t *testing.T) {
	if runtime.GOOS != "linux" || !hostBackend() {
		t.Skip("no native backend")
	}
	p := &ir.Program{Arity: 1, Result: 2, Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64}, {Op: ir.OpConst, Bits: 64, Imm: 0x12345678},
		{Op: ir.OpCallC, Bits: 64, Args: []int{1, 0}},
	}}
	f := Func{ABI: abi.C, Context: ContextGoManaged, AssumePointerFree: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
	u, err := lowerUnit(p, TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.emission.Bytes) != 0 || len(u.emission.goUnwind.SP) != 0 {
		t.Fatal("Lower encoded managed metadata")
	}
	assertUnitOffsets(t, u)
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	if !template.Report().GoManaged() || len(u.emission.goUnwind.SP) == 0 {
		t.Fatal("managed template omitted unwind metadata")
	}
	legacy, err := lowerTemplate(p, TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	legacyBytes, err := legacy.Bytes(nil)
	if err != nil || !bytes.Equal(legacyBytes, u.emission.Bytes) {
		t.Fatalf("native gateway bytes changed: %v", err)
	}
}

func TestUnitTemplateBindsIndependentSymbols(t *testing.T) {
	if runtime.GOOS != "linux" || !hostBackend() {
		t.Skip("no native backend")
	}
	p := &ir.Program{Arity: 1, Result: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 32}, {Op: ir.OpConst, Bits: 32, Symbol: "increment"}, {Op: ir.OpAdd, Bits: 32, A: 0, B: 1}}}
	u, err := lowerUnit(p, TargetSpec{}, Func{})
	if err != nil {
		t.Fatal(err)
	}
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	for _, increment := range []uint64{3, 17} {
		code, err := template.Bind(execution.Symbols{"increment": increment})
		if err != nil {
			t.Fatal(err)
		}
		got, err := code.Call(20)
		closeErr := code.Close()
		if err != nil || closeErr != nil || uint64(got) != 20+increment {
			t.Fatalf("binding%d result%d call%v close%v", increment, got, err, closeErr)
		}
	}
}

func TestEmptyUnitOutputsFail(t *testing.T) {
	for _, u := range []*Unit{nil, {}} {
		if _, err := u.Listing(); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("empty listing: %v", err)
		}
		if _, err := u.Template(); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("empty template: %v", err)
		}
	}
}
