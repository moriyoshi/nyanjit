package compile

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func Test386GoSubwordValidationCannotBypassBuilder(t *testing.T) {
	for _, widths := range [][]uint8{{8, 16, 8, 32}, {8, 8}, {16, 16}, {32, 8, 16}} {
		ops := []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 1}}
		args := []int{0}
		for _, w := range widths {
			args = append(args, len(ops))
			ops = append(ops, ir.Instruction{Op: ir.OpConst, Bits: w, Imm: 1})
		}
		result := len(ops)
		ops = append(ops, ir.Instruction{Op: ir.OpCallGo, Bits: 64, Args: args})
		p := &ir.Program{Instructions: ops, Result: result}
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		spec := TargetSpec{Architecture: "386"}
		f := Func{ABI: abi.Go, Context: ContextGoManaged, AssumePointerFree: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}}
		_, err := lowerListing(p, spec, f)
		if !errors.Is(err, ErrBackendLimit) || !strings.Contains(err.Error(), "packed Go ABI0") {
			t.Fatalf("widths%v lower: %v", widths, err)
		}
		u, err := lowerUnit(p, spec, f)
		if err == nil {
			_, err = u.RegisterScopes()
		}
		if !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("widths%v scoped preflight: %v", widths, err)
		}
	}
	for _, bits := range [][]uint8{{}, {8}, {16}, {8, 32, 16}, {64, 16, 64, 8}} {
		if err := validate386GoSubwords(bits); err != nil {
			t.Fatalf("nonpacked shape%v: %v", bits, err)
		}
	}
}

func TestControlRelaxationPreservesCompilerErrorAndCause(t *testing.T) {
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "ppc64le"} {
		t.Run(arch, func(t *testing.T) {
			options := mustTarget(t, TargetSpec{Architecture: arch}).options(Func{})
			emitter := newMachineEmitter(options).(*physicalEmitter)
			stream := emitter.machineStream()
			stream.Append(machine.Instruction{Op: machine.OpJump, Imm: 0, Long: true})
			_, err := relaxControlLayout(stream, []int{0}, []bool{false}, newMachineTarget(options))
			if !errors.Is(err, ErrInvalidFunction) || !errors.Is(err, machine.ErrInvalidBranch) {
				t.Fatalf("layout error: %v", err)
			}
		})
	}
}
