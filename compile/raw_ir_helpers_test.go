package compile_test

import (
	"github.com/moriyoshi/nyanjit/ir"
)

// rawIR wraps an ir.ProgramBuilder for tests of raw-IR block parameters,
// which Builders no longer produce.
type rawIR struct{ pb *ir.ProgramBuilder }

func newRawIR() rawIR { return rawIR{ir.NewProgramBuilder()} }

func (r rawIR) constant(block ir.Block, bits uint8, value uint64) int {
	return r.pb.Append(block, ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: value})
}

func (r rawIR) op(block ir.Block, code ir.Opcode, bits uint8, a, b int) int {
	return r.pb.Append(block, ir.Instruction{Op: code, Bits: bits, A: a, B: b})
}

// load reads a little-endian word of 32 or 64 bits.
func (r rawIR) load(block ir.Block, bits uint8, address int) int {
	ins := ir.Instruction{Op: ir.OpLoad64, A: address, ByteOrder: ir.ByteOrderLittleEndian}
	if bits == 32 {
		ins.Op, ins.Bits = ir.OpLoad32, 32
	}
	return r.pb.Append(block, ins)
}

func (r rawIR) params(block ir.Block, n int) []int {
	values := make([]int, n)
	for i := range values {
		values[i] = r.pb.BlockParam(block, i)
	}
	return values
}

func rawIsZero(value int) *ir.PredicateExpr {
	return &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: value}
}

func rawTypes(t ir.Type, n int) []ir.Type {
	types := make([]ir.Type, n)
	for i := range types {
		types[i] = t
	}
	return types
}

func (r rawIR) seal() *ir.SealedProgram {
	p, err := r.pb.Seal()
	if err != nil {
		panic(err)
	}
	return p
}
