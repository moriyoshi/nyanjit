package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// tailCallProgram is f(target, p1, ..., pn) ending in a tail call of target
// with args, value IDs of the parameters (target is 0). Parameters take the
// widths in params.
func tailCallProgram(t *testing.T, params []uint8, args ...int) *ir.SealedProgram {
	t.Helper()
	code := make([]ir.Instruction, len(params))
	for i, bits := range params {
		code[i] = ir.Instruction{Op: ir.OpParam, Bits: bits}
	}
	program, err := ir.Seal(&ir.Program{Arity: len(params), Instructions: code, Control: &ir.Control{
		Labels: []bool{true}, LabelScopes: []int{0}, Params: []int{0}, ParamBits: [][]uint8{nil},
		Steps: []ir.ControlStep{{IsLabel: true}, {Term: ir.TermTailCall, End: len(code), Arity: len(code), Result: -1, To: -1, Other: -1, Args: append([]int{0}, args...)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return program
}

var tailForeign = compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}

// TestTailCallListing lowers a tail call on every target and tier: it ends
// with the jump through the call-target register, never a call.
func TestTailCallListing(t *testing.T) {
	jumps := map[string][2]string{
		"amd64": {"jmp R11", "call R11"}, "386": {"jmp EAX", "call EAX"}, "arm64": {"br X16", "blr X16"},
		"riscv64": {"jalr x0, 0(x31)", "jalr x1, 0(x31)"}, "loong64": {"jirl r0, r20, 0", "jirl r1, r20, 0"}, "ppc64le": {"bctr", "bctrl"},
	}
	for arch, jump := range jumps {
		width := uint8(64)
		if arch == "386" {
			width = 32
		}
		program := tailCallProgram(t, []uint8{width, 32, 64}, 2, 1)
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			unit, err := compile.Lower(target, program, tailForeign)
			if err != nil {
				t.Fatalf("%s tier %d: %v", arch, tier, err)
			}
			listing, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(listing, jump[0]) || strings.Contains(listing, jump[1]) {
				t.Errorf("%s tier %d: want %q and no %q in\n%s", arch, tier, jump[0], jump[1], listing)
			}
			if r := unit.Report(); !r.HasFunctionCalls() {
				t.Errorf("%s tier %d: a tail call is not reported as a function call", arch, tier)
			}
		}
	}
}

// TestTailCallLimits rejects more arguments than C argument registers, and
// on 386 more argument words than the entry's own, with LimitArguments.
func TestTailCallLimits(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
		width := uint8(64)
		if arch == "386" {
			width = 32
		}
		n := abi.MaxArgumentsFor(arch) + 1
		params := []uint8{width}
		args := make([]int, n)
		for i := range args {
			params = append(params, 8)
			args[i] = i + 1
		}
		cases := []*ir.SealedProgram{tailCallProgram(t, params[:min(len(params), abi.MaxArgumentsFor(arch))], 1, 1, 1, 1, 1, 1, 1, 1, 1)}
		if arch == "386" {
			// Two incoming words, then four: one more word than the entry's.
			cases = append(cases, tailCallProgram(t, []uint8{32, 32}, 1, 1, 1), tailCallProgram(t, []uint8{32, 32, 64}, 2, 2, 1))
		}
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			for i, program := range cases {
				_, err := compile.Lower(target, program, tailForeign)
				var limit *compile.LimitError
				if !errors.As(err, &limit) || limit.Kind != compile.LimitArguments {
					t.Errorf("%s tier %d case %d: got %v", arch, tier, i, err)
				}
			}
			if arch == "386" {
				for _, args := range [][]int{{1}, {1, 1}} {
					if _, err := compile.Lower(target, tailCallProgram(t, []uint8{32, 32}, args...), tailForeign); err != nil {
						t.Errorf("386 tier %d: %d words in two: %v", tier, len(args), err)
					}
				}
			}
		}
	}
}

// TestTailCallHardened lowers a hardened tail call without other control
// flow on every target and tier. A tail call makes a control program, which
// riscv64 and loong64, without a speculation barrier, reject.
func TestTailCallHardened(t *testing.T) {
	f := tailForeign
	f.Harden = true
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
		width := uint8(64)
		if arch == "386" {
			width = 32
		}
		program := tailCallProgram(t, []uint8{width, 32}, 1)
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			_, err = compile.Lower(target, program, f)
			if arch == "riscv64" || arch == "loong64" {
				if !errors.Is(err, compile.ErrUnsupported) {
					t.Errorf("%s tier %d: %v", arch, tier, err)
				}
			} else if err != nil {
				t.Errorf("%s tier %d: %v", arch, tier, err)
			}
		}
	}
}
