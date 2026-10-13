package compile_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// mathBudgetArchitectures orders the columns of mathBudgets.
var mathBudgetArchitectures = [...]string{"amd64", "arm64", "riscv64", "ppc64le", "loong64"}

// mathBudgets bounds the instructions emitted for each inline binary64 math
// function: the counts measured on 2026-10-14 plus about 10%. A failure means a
// change made the expansion or its lowering larger; investigate before
// raising a budget. Lower each width's budgets when its measured lowering
// improves, preserving the corresponding baseline CPU profile.
var mathBudgets = map[string][len(mathBudgetArchitectures)]int{
	"Sin":         {361, 274, 393, 490, 312},
	"Cos":         {340, 259, 367, 457, 295},
	"Tan":         {340, 259, 382, 446, 291},
	"Asin":        {153, 157, 250, 279, 189},
	"Acos":        {191, 188, 295, 340, 227},
	"Atan":        {117, 122, 197, 219, 147},
	"Sinh":        {172, 148, 226, 312, 189},
	"Cosh":        {132, 114, 180, 233, 147},
	"Tanh":        {179, 150, 224, 312, 189},
	"Asinh":       {197, 165, 258, 348, 206},
	"Acosh":       {204, 174, 269, 358, 214},
	"Atanh":       {223, 178, 292, 383, 218},
	"Exp":         {113, 96, 148, 189, 120},
	"Exp2":        {113, 90, 139, 182, 115},
	"Expm1":       {269, 210, 312, 408, 252},
	"Log":         {132, 111, 175, 236, 141},
	"Log2":        {182, 149, 247, 324, 193},
	"Log10":       {136, 114, 180, 245, 146},
	"Log1p":       {169, 139, 226, 303, 172},
	"Cbrt":        {87, 77, 114, 136, 90},
	"Floor":       {49, 35, 71, 72, 44},
	"Ceil":        {53, 37, 82, 85, 49},
	"Trunc":       {29, 21, 40, 42, 24},
	"Round":       {46, 33, 65, 70, 40},
	"RoundToEven": {59, 41, 75, 79, 47},
	"Pow":         {752, 472, 716, 904, 552},
	"Atan2":       {277, 255, 468, 479, 313},
	"Hypot":       {75, 62, 115, 119, 74},
}

// The loong64 columns were measured on 2026-10-15 with baseline LA64 FMA.
// mathBudgets32 measures binary32 expansions independently, including their
// widening/narrowing conversions, with ceil(count*1.1) instruction headroom.
var mathBudgets32 = map[string][len(mathBudgetArchitectures)]int{
	"Sin":         {366, 280, 403, 498, 319},
	"Cos":         {345, 263, 374, 464, 301},
	"Tan":         {345, 264, 392, 454, 297},
	"Asin":        {158, 162, 259, 284, 195},
	"Acos":        {198, 193, 304, 348, 234},
	"Atan":        {124, 127, 206, 227, 153},
	"Sinh":        {176, 153, 235, 317, 195},
	"Cosh":        {139, 120, 190, 241, 154},
	"Tanh":        {184, 156, 233, 317, 195},
	"Asinh":       {202, 171, 267, 356, 213},
	"Acosh":       {209, 180, 278, 366, 220},
	"Atanh":       {228, 183, 300, 391, 225},
	"Exp":         {119, 103, 158, 197, 128},
	"Exp2":        {119, 96, 149, 191, 123},
	"Expm1":       {275, 216, 322, 416, 260},
	"Log":         {138, 116, 184, 244, 148},
	"Log2":        {187, 154, 256, 332, 200},
	"Log10":       {140, 118, 187, 251, 151},
	"Log1p":       {175, 145, 235, 311, 179},
	"Cbrt":        {95, 83, 123, 143, 96},
	"Floor":       {54, 40, 80, 80, 51},
	"Ceil":        {59, 42, 86, 87, 54},
	"Trunc":       {36, 28, 50, 51, 32},
	"Round":       {53, 40, 75, 79, 49},
	"RoundToEven": {65, 48, 85, 87, 55},
	"Pow":         {763, 482, 730, 917, 564},
	"Atan2":       {283, 264, 482, 491, 324},
	"Hypot":       {86, 73, 130, 134, 86},
}

// Spills go to the workspace that Code.Call supplies, so expansions never
// move the stack pointer. FP and general-purpose values use separate banks with a shared spill scheduler.
const mathWorkspaceBudget = 256

func buildInlineMath(name string, width uint8) *nyanjit.Builder {
	b := nyanjit.NewBuilder()
	x := b.ParamBits(width)
	switch name {
	case "Pow":
		b.Return(b.FPow(x, b.ParamBits(width)))
	case "Atan2":
		b.Return(b.FAtan2(x, b.ParamBits(width)))
	case "Hypot":
		b.Return(b.FHypot(x, b.ParamBits(width)))
	default:
		unary := map[string]func(*nyanjit.Builder, nyanjit.Value) nyanjit.Value{
			"Sin": (*nyanjit.Builder).FSin, "Cos": (*nyanjit.Builder).FCos, "Tan": (*nyanjit.Builder).FTan,
			"Asin": (*nyanjit.Builder).FAsin, "Acos": (*nyanjit.Builder).FAcos, "Atan": (*nyanjit.Builder).FAtan,
			"Sinh": (*nyanjit.Builder).FSinh, "Cosh": (*nyanjit.Builder).FCosh, "Tanh": (*nyanjit.Builder).FTanh,
			"Asinh": (*nyanjit.Builder).FAsinh, "Acosh": (*nyanjit.Builder).FAcosh, "Atanh": (*nyanjit.Builder).FAtanh,
			"Exp": (*nyanjit.Builder).FExp, "Exp2": (*nyanjit.Builder).FExp2, "Expm1": (*nyanjit.Builder).FExpm1,
			"Log": (*nyanjit.Builder).FLog, "Log2": (*nyanjit.Builder).FLog2, "Log10": (*nyanjit.Builder).FLog10,
			"Log1p": (*nyanjit.Builder).FLog1p, "Cbrt": (*nyanjit.Builder).FCbrt, "Floor": (*nyanjit.Builder).FFloor,
			"Ceil": (*nyanjit.Builder).FCeil, "Trunc": (*nyanjit.Builder).FTrunc, "Round": (*nyanjit.Builder).FRound,
			"RoundToEven": (*nyanjit.Builder).FRoundToEven,
		}
		b.Return(unary[name](b, x))
	}
	return b
}

func countAssemblyInstructions(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasSuffix(line, ":") && !strings.Contains(line, ".byte") {
			count++
		}
	}
	return count
}

// TestInlineMathInstructionBudgets keeps every inline math expansion within
// its instruction budget on every 64-bit target, and a frameless call-free
// leaf on the host.
func TestInlineMathInstructionBudgets(t *testing.T) {
	for name, budgets := range mathBudgets {
		for column, arch := range mathBudgetArchitectures {
			for _, width := range []uint8{64, 32} {
				budget := budgets[column]
				if width == 32 {
					budget = mathBudgets32[name][column]
				}
				program := finishCrossAssembly(t, buildInlineMath(name, width))
				spec, fn := compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C}
				text, err := lowerListing(program, spec, fn)
				if err != nil {
					t.Fatalf("%s%d on %s: %v", name, width, arch, err)
				}
				count := countAssemblyInstructions(text)
				if arch == "loong64" {
					t.Logf("loong64 %s%d instructions=%d", name, width, count)
				}
				if count > budget {
					t.Errorf("%s%d on %s: %d instructions, budget %d", name, width, arch, count, budget)
				}
				if arch != runtime.GOARCH {
					continue
				}
				// Analysis needs the host target. Other architectures are
				// checked for calls by TestCrossAssemblyInlineMath.
				report, err := lowerReport(program, spec, fn)
				if err != nil {
					t.Fatalf("%s%d on %s: %v", name, width, arch, err)
				}
				if report.HasFunctionCalls() || report.HasSystemCalls() || report.StackBytes() != 0 || report.WorkspaceBytes() > mathWorkspaceBudget {
					t.Errorf("%s%d on %s: calls=%v syscalls=%v stack=%d workspace=%d, want a frameless leaf with at most %d workspace bytes",
						name, width, arch, report.HasFunctionCalls(), report.HasSystemCalls(), report.StackBytes(), report.WorkspaceBytes(), mathWorkspaceBudget)
				}
			}
		}
	}
}

func TestInlinePowFMAInstructionBudget(t *testing.T) {
	for _, width := range []uint8{64, 32} {
		p := finishCrossAssembly(t, buildInlineMath("Pow", width))
		text, err := lowerListing(p, compile.TargetSpec{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"fma"}}}, compile.Func{})
		if err != nil {
			t.Fatal(err)
		}
		budget := 636 // 578 binary64 instructions, plus 10%.
		if width == 32 {
			budget = 647 // 588 binary32 instructions, plus 10%.
		}
		if got := countAssemblyInstructions(text); got > budget {
			t.Fatalf("FMA Pow%d: %d instructions, budget%d", width, got, budget)
		}
	}
}
