package compile_test

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Byte operations may consume just the low word of a 64-bit scalar on 386.
// Exercise every stack/ABI combination rather than relying on the larger
// StackPlatform register bank to hide unnecessary high-word allocation.
func TestRound13MemoryMatrix386(t *testing.T) {
	for _, pair := range []struct {
		load, store ir.Opcode
		bits        uint8
	}{
		{ir.OpLoad8, ir.OpStore8, 8}, {ir.OpLoad16, ir.OpStore16, 16},
		{ir.OpLoad32, ir.OpStore32, 32}, {ir.OpLoad64, ir.OpStore64, 64},
	} {
		for _, op := range []ir.Opcode{pair.load, pair.store} {
			for _, width := range []uint8{8, 16, 32, 64} {
				load := op == pair.load
				// The 16/32-bit operations have fixed-width results and accept matching
				// or 64-bit store values. Load/Store64 are tested at their native width.
				if load && pair.bits == 8 && (width == 16 || width == 32) {
					continue
				}
				if pair.bits != 8 && (load && width != pair.bits || !load && width != pair.bits && width != 64) {
					continue
				}
				for _, addressBits := range []uint8{32, 64} {
					for _, param := range []bool{false, true} {
						for _, result := range []round13Result{round13OperationResult, round13AddressResult, round13CombinedResult} {
							for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
								for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
									for _, harden := range []bool{false, true} {
										name := fmt.Sprintf("%s/width%d/addr%d/param%v/result%d/stack%d/abi%d/harden%v", op, width, addressBits, param, result, stack, flavor, harden)
										t.Run(name, func(t *testing.T) {
											p := round13MemoryProgram(op, pair.bits, width, addressBits, param, result)
											if err := p.Validate(); err != nil {
												t.Fatal(err)
											}
											options := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack}, Harden: harden}
											if stack == abi.StackPlatform {
												options.Stack.BytesAvailable = 8192
											}
											if _, err := lowerListing(p, compile.TargetSpec{Architecture: "386"}, options); err != nil {
												t.Fatal(err)
											}
										})
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

// round13Result selects what the memory program returns. The combined result
// keeps both the address and the loaded or stored value live after the access.
type round13Result int

const (
	round13OperationResult round13Result = iota
	round13AddressResult
	round13CombinedResult
)

func round13MemoryProgram(op ir.Opcode, bits, width, addressBits uint8, param bool, result round13Result) *ir.Program {
	p := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpConst, Bits: addressBits, Imm: 0x10},
		{Op: ir.OpConst, Bits: width, Imm: 7},
	}, Result: 1}
	if param {
		p.Arity = 2
		p.Instructions[0] = ir.Instruction{Op: ir.OpParam, Bits: addressBits}
		p.Instructions[1] = ir.Instruction{Op: ir.OpParam, Bits: width}
	}
	ins := ir.Instruction{Op: op, Bits: bits, A: 0}
	switch op {
	case ir.OpLoad8, ir.OpLoad16, ir.OpLoad32, ir.OpLoad64:
		ins.Bits = width
		p.Result = 2
	default:
		ins.B = 1
	}
	p.Instructions = append(p.Instructions, ins)
	switch result {
	case round13AddressResult:
		p.Result = 0
	case round13CombinedResult:
		// XOR the zero-extended address with the loaded or stored value.
		wide := func(id int, bits uint8) int {
			if bits == 64 {
				return id
			}
			p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: id})
			return len(p.Instructions) - 1
		}
		value, valueBits := 1, width
		if p.Result == 2 {
			value = 2
		}
		address := wide(0, addressBits)
		value = wide(value, valueBits)
		p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpXor, Bits: 64, A: address, B: value})
		p.Result = len(p.Instructions) - 1
	}
	return p
}

// A shared address/value does not require two distinct register groups. Keep
// this case separate so the pressure fallback cannot alter its old listing.
func TestRound13Store8SharedInput386(t *testing.T) {
	p := &ir.Program{Result: 0, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 64, Imm: 0x10}, {Op: ir.OpStore8, Bits: 8, A: 0, B: 0}}}
	if _, err := lowerListing(p, compile.TargetSpec{Architecture: "386"}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackNoUse}}); err != nil {
		t.Fatal(err)
	}
}

// A byte load whose 64-bit address stays live needs four words under
// StackNoUse, which has three registers. The address must move to a memory
// home instead of failing allocation.
func TestLoad8LiveWideAddress386(t *testing.T) {
	for name, instructions := range map[string][]ir.Instruction{
		"computed": {
			{Op: ir.OpConst, Bits: 64, Imm: 0xdeadbeef08001009}, {Op: ir.OpConst, Bits: 64},
			{Op: ir.OpAdd, Bits: 64, A: 0, B: 1}, {Op: ir.OpLoad8, Bits: 64, A: 2},
			{Op: ir.OpXor, Bits: 64, A: 2, B: 3}, {Op: ir.OpConst, Bits: 32, Imm: 0x08002000},
			{Op: ir.OpStore64, Bits: 64, A: 5, B: 4},
		},
		"constant": {
			{Op: ir.OpConst, Bits: 64, Imm: 0x08001009}, {Op: ir.OpLoad8, Bits: 64, A: 0},
			{Op: ir.OpXor, Bits: 64, A: 0, B: 1}, {Op: ir.OpConst, Bits: 32, Imm: 0x08002000},
			{Op: ir.OpStore64, Bits: 64, A: 3, B: 2},
		},
	} {
		result := 3
		if name == "constant" {
			result = 1
		}
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			for _, harden := range []bool{false, true} {
				p := &ir.Program{Result: result, Instructions: instructions}
				options := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: abi.StackNoUse}, Harden: harden}
				if _, err := lowerListing(p, compile.TargetSpec{Architecture: "386"}, options); err != nil {
					t.Errorf("%s abi %d harden %v: %v", name, flavor, harden, err)
				}
			}
		}
	}
}
