package builder

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// Small builders keep their original instruction prefix. Larger builders use
// compact temporary blocks; Finish expands them once into exact-size public IR.
const instructionBlockSize = 1024
const instructionPrefixSize = 384

// valueRecord is what the Builder keeps for every instruction ID, in both
// modes. Type checks, scope liveness and capture eligibility read only these
// records, so a segment Builder needs no instruction store for them. The
// payload holds what Carry and scaled-address folding read from a value's
// definition, which a segment Builder may already have emitted: the
// immediate of a literal constant, or the operands of OpAdd and OpSub.
type valueRecord struct {
	typ    ir.Type
	op     op
	flags  uint8
	scope  int32
	lo, hi uint32
}

const (
	// recordLiteral marks an OpConst without a symbol.
	recordLiteral uint8 = 1 << iota
	// recordExporting marks a value Leave is exporting, while Leave runs.
	recordExporting
)

// appendRecord appends the record of instruction i, owned by scope.
func (b *Builder) appendRecord(i *instruction, scope int) {
	n := len(b.values)
	if n == cap(b.values) {
		b.values = append(b.values, valueRecord{})
	} else {
		b.values = b.values[:n+1]
	}
	r := &b.values[n]
	r.typ, r.op, r.flags, r.scope = i.ValueType(), i.Op, 0, int32(scope)
	switch i.Op {
	case opConst:
		r.lo, r.hi = 0, 0
		if i.Symbol == "" {
			r.flags = recordLiteral
			r.lo, r.hi = uint32(i.Imm), uint32(i.Imm>>32)
		}
	case opAdd, opSub:
		r.lo, r.hi = uint32(i.A), uint32(i.B)
	default:
		r.lo, r.hi = 0, 0
	}
}

type instructionExtra struct {
	Data   []byte
	Symbol string
	Args   []int
	Effect *abi.CallEffect
}
type storedInstruction struct {
	Imm                         uint64
	A, B, C, Variable, EntryReg int
	Type                        ir.Type
	Op                          op
	Bits                        uint8
	ByteOrder                   ir.ByteOrder
	Contract                    bool
	Aligned                     bool
	extra                       *instructionExtra
}

func storeInstruction(i instruction) storedInstruction {
	s := storedInstruction{Imm: i.Imm, A: i.A, B: i.B, C: i.C, Variable: i.Variable, EntryReg: i.EntryReg, Type: i.Type, Op: i.Op, Bits: i.Bits, ByteOrder: i.ByteOrder, Contract: i.Contract, Aligned: i.Aligned}
	if i.Data != nil || i.Symbol != "" || i.Args != nil || i.Effect != nil {
		s.extra = &instructionExtra{Data: i.Data, Symbol: i.Symbol, Args: i.Args, Effect: i.Effect}
	}
	return s
}
func (s *storedInstruction) expand() instruction {
	i := instruction{Imm: s.Imm, A: s.A, B: s.B, C: s.C, Variable: s.Variable, EntryReg: s.EntryReg, Type: s.Type, Op: s.Op, Bits: s.Bits, ByteOrder: s.ByteOrder, Contract: s.Contract, Aligned: s.Aligned}
	if e := s.extra; e != nil {
		i.Data = e.Data
		i.Symbol = e.Symbol
		i.Args = e.Args
		i.Effect = e.Effect
	}
	return i
}

// writeTo fills a fresh zeroed IR record. Ordinary instructions contain no
// pointer payload, so avoid aggregate pointer barriers for their nil fields.
func (s *storedInstruction) writeTo(i *instruction) {
	i.Imm = s.Imm
	i.A = s.A
	i.B = s.B
	i.C = s.C
	i.Variable = s.Variable
	i.EntryReg = s.EntryReg
	i.Type = s.Type
	i.Op = s.Op
	i.Bits = s.Bits
	i.ByteOrder = s.ByteOrder
	i.Contract = s.Contract
	i.Aligned = s.Aligned
	if e := s.extra; e != nil {
		i.Data = e.Data
		i.Symbol = e.Symbol
		i.Args = e.Args
		i.Effect = e.Effect
	}
}

func (b *Builder) codeLen() int { return b.codeSize }
func (b *Builder) storedAt(id int) *storedInstruction {
	offset := id - len(b.code)
	return &b.codeBlocks[offset/instructionBlockSize][offset%instructionBlockSize]
}
func (b *Builder) instructionOp(id int) op        { return b.values[id].op }
func (b *Builder) instructionType(id int) ir.Type { return b.values[id].typ }

// instructionLiteral returns the immediate of a literal OpConst.
func (b *Builder) instructionLiteral(id int) (uint64, bool) {
	r := &b.values[id]
	return uint64(r.lo) | uint64(r.hi)<<32, r.flags&recordLiteral != 0
}

// setContract marks an instruction of the open block.
func (b *Builder) setContract(id int) {
	switch {
	case b.seg != nil:
		b.seg.code[id-b.seg.codeBase].Contract = true
	case id < len(b.code):
		b.code[id].Contract = true
	default:
		b.storedAt(id).Contract = true
	}
}
func (b *Builder) appendCaptureArgs(id int, args []int) {
	if id < len(b.code) {
		b.code[id].Args = append(b.code[id].Args, args...)
		return
	}
	s := b.storedAt(id)
	if s.extra == nil {
		s.extra = new(instructionExtra)
	}
	s.extra.Args = append(s.extra.Args, args...)
}
func (b *Builder) growInstructions() {
	capacity := max(3, min(instructionPrefixSize, 4*cap(b.code)))
	out := make([]instruction, len(b.code), capacity)
	copy(out, b.code)
	b.code = out
	b.growValues(cap(out))
}
func (b *Builder) growValues(capacity int) {
	if cap(b.values) >= capacity {
		return
	}
	if capacity > instructionPrefixSize {
		power := 1
		for power < capacity {
			power *= 2
		}
		capacity = power
	}
	values := make([]valueRecord, len(b.values), capacity)
	copy(values, b.values)
	b.values = values
}
func (b *Builder) flatCode() []instruction {
	if b.codeBlocks == nil {
		return tightCopy(b.code)
	}
	out := make([]instruction, b.codeSize)
	n := copy(out, b.code)
	for _, block := range b.codeBlocks {
		for i := 0; i < len(block) && n < len(out); i++ {
			block[i].writeTo(&out[n])
			n++
		}
	}
	return out
}
func (b *Builder) finishCode() []instruction {
	b.code = b.flatCode()
	b.codeBlocks = nil
	return b.code
}
