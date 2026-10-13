package compile_test

import (
	"embed"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Captured reviewer seed 1720000259 used to fit eight 386 platform-stack
// configurations. Native Store16/32 changed the schedule enough to exhaust
// registers in an unrelated paired shift with reserved scoped-variable homes.
func TestNarrowMemory386PressureRegression(t *testing.T) {
	program := narrowPressureFixture(t, "386-1720000259.json")
	for _, name := range []string{"C", "Go", "GoManaged", "CManaged", "Compact", "NoSIMD", "BMI", "CLMUL"} {
		t.Run(name, func(t *testing.T) {
			spec := compile.TargetSpec{Architecture: "386"}
			// The fixture calls C; it is lowered for external use unless managed.
			fn := compile.Func{Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true}
			switch name {
			case "Go", "GoManaged":
				fn.ABI = abi.Go
			}
			if name == "GoManaged" || name == "CManaged" {
				fn.Context = compile.ContextGoManaged
			}
			fn.PreferCompactLoops = name == "Compact"
			if name == "NoSIMD" {
				spec.Vectors = compile.VectorsOff()
			}
			if name == "BMI" {
				spec.CPU = abi.CPUProfile{Level: "i686", Extensions: []string{"bmi1", "bmi2", "lzcnt", "popcnt"}}
			}
			if name == "CLMUL" {
				spec.CPU = abi.CPUProfile{Level: "i686", Extensions: []string{"pclmulqdq"}}
			}
			// Cross-target lowering always checks allocation, including CPU
			// profiles that exceed the native host's execution capabilities.
			unit, err := lowerUnit(program, spec, fn)
			if errors.Is(err, compile.ErrCPUUnsupported) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			compat, err := lowerListing(program, spec, fn)
			if err != nil || compat == "" {
				t.Fatalf("assembly length %d, error %v", len(compat), err)
			}
			listing, err := unit.Listing()
			if err != nil || listing != compat {
				t.Fatalf("entry-point disagreement: error %v", err)
			}
		})
	}
}

// These programs fitted the 128-byte budget before native narrow memory
// operations. The retry must fit that same caller-supplied budget and report
// the actual resource use of the successful plan. The ppc64le program only
// fitted by copying a float into r14 without saving it (security audit
// 2026-10-25, F15); saving it needs a frame, so 128 bytes must fail closed.
// The 386-1720000736 program gets 144 bytes: one more spill slot buys a
// plan with far fewer stack accesses than the byte-store fallback that fits
// 128.
func TestNarrowMemorySmallFrameRegression(t *testing.T) {
	// The fixtures' frames come from their Var homes. With Vars promoted,
	// the ppc64le program fits 128 bytes using volatile registers only.
	compile.WithoutVarPromotion(t)
	budgets := map[string]struct {
		bytes      int
		failsSmall bool
	}{
		"riscv64-1720000409.json": {128, false},
		"386-1720000736.json":     {144, false},
		"ppc64le-1720000817.json": {256, true},
		"386-1720001128.json":     {128, false},
	}
	for name, budget := range budgets {
		t.Run(name, func(t *testing.T) {
			program := narrowPressureFixture(t, name)
			arch, _, _ := strings.Cut(name, "-")
			spec := compile.TargetSpec{Architecture: arch}
			fn := compile.Func{Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: budget.bytes}}
			if budget.failsSmall {
				small := fn
				small.Stack.BytesAvailable = 128
				if _, err := lowerUnit(program, spec, small); !errors.Is(err, compile.ErrStackRequired) {
					t.Fatalf("128-byte budget: %v, want ErrStackRequired", err)
				}
			}
			unit, err := lowerUnit(program, spec, fn)
			if err != nil {
				t.Fatal(err)
			}
			report := unit.Report()
			if report.StackBytes()+report.CallStackBytes() > budget.bytes || report.WorkspaceBytes() != 0 {
				t.Fatalf("unexpected resources: stack=%d call=%d workspace=%d", report.StackBytes(), report.CallStackBytes(), report.WorkspaceBytes())
			}
			if _, err := unit.Listing(); err != nil {
				t.Fatal(err)
			}
			if _, err := lowerListing(program, spec, fn); err != nil {
				t.Fatal(err)
			}
		})
	}
}

//go:embed testdata/narrow-pressure/*.json
var narrowPressureFixtures embed.FS

func narrowPressureFixture(t *testing.T, name string) *ir.Program {
	t.Helper()
	data, err := narrowPressureFixtures.ReadFile("testdata/narrow-pressure/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var program ir.Program
	if err := json.Unmarshal(data, &program); err != nil {
		t.Fatal(err)
	}
	if err := program.Validate(); err != nil {
		t.Fatal(err)
	}
	return &program
}
