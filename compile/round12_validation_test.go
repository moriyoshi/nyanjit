package compile_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func typedParamProgram(bits uint8) *ir.Program {
	return &ir.Program{Result: -1, Instructions: []ir.Instruction{
		{Op: ir.OpConst, Bits: bits, Imm: 7},
		{Op: ir.OpLabelParam, Bits: bits, Type: ir.Scalar(bits)},
	}, Control: &ir.Control{
		Labels: []bool{true, true}, LabelScopes: []int{0, 0}, Params: []int{0, 1},
		ParamBits: [][]uint8{nil, {bits}}, ParamTypes: [][]ir.Type{nil, {ir.Scalar(bits)}},
		Steps: []ir.ControlStep{
			{IsLabel: true, Label: 0},
			{Start: 0, End: 1, Term: ir.TermJump, To: 1, Args: []int{0}},
			{IsLabel: true, Label: 1},
			{Start: 1, End: 2, Term: ir.TermReturn, Result: 1},
		},
	}}
}

func TestRound12TypedParamBitsAgreement(t *testing.T) {
	for _, bits := range []uint8{8, 16, 32, 64} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			p := typedParamProgram(bits)
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			if _, err := ir.Seal(p); err != nil {
				t.Fatal(err)
			}
			for _, wrong := range []uint8{0, 8, 16, 32, 64, 128} {
				if wrong == bits {
					continue
				}
				q := p.Clone()
				q.Control.ParamBits[1][0] = wrong
				if err := q.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
					t.Fatalf("bits=%d Validate=%v", wrong, err)
				}
				controlFieldEntryPoints(t, q, false)
				controlFieldEntryPoints(t, q, false, true)
			}
		})
	}
}

func memoryOrderProgram(op ir.Opcode, bits uint8, order ir.ByteOrder, addressBits uint8) *ir.Program {
	p := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpConst, Bits: addressBits, Imm: 0x10},
		{Op: ir.OpConst, Bits: bits, Imm: 7},
	}, Result: 1}
	ins := ir.Instruction{Op: op, Bits: bits, A: 0, ByteOrder: order}
	switch op {
	case ir.OpLoad8, ir.OpLoad16, ir.OpLoad32, ir.OpLoad64:
		p.Result = 2
	default:
		ins.B = 1
	}
	p.Instructions = append(p.Instructions, ins)
	return p
}

func TestRound12MemoryOrderValidationAndLowering(t *testing.T) {
	for _, pair := range []struct {
		load, store ir.Opcode
		bits        uint8
	}{
		{ir.OpLoad8, ir.OpStore8, 8}, {ir.OpLoad16, ir.OpStore16, 16},
		{ir.OpLoad32, ir.OpStore32, 32}, {ir.OpLoad64, ir.OpStore64, 64},
	} {
		for _, op := range []ir.Opcode{pair.load, pair.store} {
			for _, order := range []ir.ByteOrder{ir.ByteOrderNative, ir.ByteOrderLittleEndian, ir.ByteOrderBigEndian, 99} {
				t.Run(fmt.Sprintf("%s/order%d", op, order), func(t *testing.T) {
					p := memoryOrderProgram(op, pair.bits, order, 64)
					valid := order.Valid() && !(pair.bits == 64 && order == ir.ByteOrderBigEndian)
					if err := p.Validate(); (err == nil) != valid {
						t.Fatalf("Validate=%v want valid=%v", err, valid)
					}
					if !valid {
						controlFieldEntryPoints(t, p, false)
						controlFieldEntryPoints(t, p, false, true)
						return
					}
					controlFieldEntryPoints(t, p, true)
					controlFieldEntryPoints(t, p, true, true)
					for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
						for _, harden := range []bool{false, true} {
							_, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, Harden: harden})
							if err != nil {
								t.Fatalf("%s Harden=%v: %v", arch, harden, err)
							}
						}
					}
				})
			}
		}
	}
}

func TestRound12MemoryAddressWidths386(t *testing.T) {
	for _, pair := range []struct {
		load, store ir.Opcode
		bits        uint8
	}{
		{ir.OpLoad8, ir.OpStore8, 8}, {ir.OpLoad16, ir.OpStore16, 16},
		{ir.OpLoad32, ir.OpStore32, 32}, {ir.OpLoad64, ir.OpStore64, 64},
	} {
		for _, op := range []ir.Opcode{pair.load, pair.store} {
			for _, addressBits := range []uint8{32, 64} {
				p := memoryOrderProgram(op, pair.bits, ir.ByteOrderNative, addressBits)
				if op == ir.OpStore8 {
					p.Instructions[1].Bits = 64
				}
				for _, harden := range []bool{false, true} {
					_, err := lowerListing(p, compile.TargetSpec{Architecture: "386"}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, Harden: harden})
					if err != nil {
						t.Errorf("%s addr%d Harden=%v: %v", op, addressBits, harden, err)
					}
				}
			}
		}
	}
}

func TestRound12Load8WideResult386(t *testing.T) {
	for _, addressBits := range []uint8{32, 64} {
		p := memoryOrderProgram(ir.OpLoad8, 8, ir.ByteOrderNative, addressBits)
		p.Instructions[2].Bits = 64
		for _, harden := range []bool{false, true} {
			if _, err := lowerListing(p, compile.TargetSpec{Architecture: "386"}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, Harden: harden}); err != nil {
				t.Errorf("addr%d Harden=%v: %v", addressBits, harden, err)
			}
		}
	}
}
