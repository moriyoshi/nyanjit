package irconform

import (
	"fmt"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// prefetchCases prefetch nil, invalid and valid addresses before a load and
// a store. A prefetch never faults and has no effect, so each case returns
// what the load reads and leaves the buffer as the store wrote it.
func prefetchCases() []Case {
	var cases []Case
	kinds := []ir.PrefetchKind{ir.PrefetchRead, ir.PrefetchWrite}
	// Nil, near nil, all ones and a non-canonical 64-bit address.
	invalid := []uint64{0, 8, mask(nativeBits), 0x0123456789abcdef & mask(nativeBits)}
	for _, kind := range kinds {
		for _, address := range invalid {
			cases = append(cases, Case{Name: name("memory", ir.OpPrefetch, kind, fmt.Sprintf("%#x", address)), Buffer: memorySize, Inputs: [][]uint64{{}},
				Build: straight(append(params(nativeBits),
					ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: address},
					ir.Instruction{Op: ir.OpPrefetch, A: 1, Imm: uint64(kind)},
					ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
		}
		// A 32-bit address on a 64-bit target, and the buffer itself before
		// a store and a load of it.
		cases = append(cases, Case{Name: name("memory", ir.OpPrefetch, kind, "address32"), Buffer: memorySize, Inputs: [][]uint64{{}},
			Build: straight(append(params(nativeBits),
				ir.Instruction{Op: ir.OpConst, Bits: 32, Imm: 0xfffff000},
				ir.Instruction{Op: ir.OpPrefetch, A: 1, Imm: uint64(kind)},
				ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
		cases = append(cases, Case{Name: name("memory", ir.OpPrefetch, kind, "buffer"), Buffer: memorySize, Inputs: [][]uint64{{0}, {0x0123456789abcdef}},
			Build: straight(append(params(nativeBits, 64),
				ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: 8},
				ir.Instruction{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: 2},
				ir.Instruction{Op: ir.OpPrefetch, A: 3, Imm: uint64(kind)},
				ir.Instruction{Op: ir.OpStore64, A: 3, B: 1},
				ir.Instruction{Op: ir.OpPrefetch, A: 0, Imm: uint64(kind)},
				ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
		// Through the Builder, inside control flow, with the address in a Var.
		cases = append(cases, Case{Name: "control/prefetch-" + itoa(uint8(kind)), Inputs: unary(64), Buffer: memorySize, Build: built(func(b *nyanjit.Builder) {
			buffer, x := b.ParamNative(), b.Param()
			address := b.Var(b.ConstBits(nativeBits, 0))
			skip := b.NewLabel()
			b.Branch(b.IsZero(x), skip)
			b.Set(address, buffer)
			b.Jump(skip)
			b.Bind(skip)
			b.Prefetch(b.Get(address), kind)
			b.Store64(buffer, x)
			b.Prefetch(b.ConstBits(nativeBits, mask(nativeBits)), kind)
			b.Return(b.Load64(buffer))
		})})
	}
	return cases
}
