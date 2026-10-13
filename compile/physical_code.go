package compile

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// physicalCode holds a physical program's instructions with their PCs. A
// program the lowering keeps packs them: a packedInsn holds every field
// without a pointer, so the garbage collector never scans the instructions,
// and the rare Data, Symbol, Table and CallAreaBytes live in extra. A control
// fragment's body, built into its emitter's shared bodyBuffer and copied out
// before the next body, keeps them located instead. At most one of the two
// forms holds instructions; the accessors read either.
type physicalCode struct {
	packed  []packedInsn
	extra   []packedExtra
	located []locatedInsn
}

// packedInsn is a machine.Instruction and its PC without the pointer fields:
// 64 bytes on a 64-bit host, against 136 for a locatedInsn.
type packedInsn struct {
	imm, disp   uint64
	pc, slot    int
	extra       uint32 // one more than the index of the instruction's packedExtra; zero for none
	vectorType  abi.ValueType
	op          machine.Opcode
	bits        uint8
	flagRequest machine.FlagMask
	arithmetic  op.Opcode
	condition   op.BranchCondition
	comparison  machine.ComparisonMode
	byteOrder   op.ByteOrder
	barrier     machine.Barrier

	a, b, c, d, e, highA, highB, highC backend.Register

	carryMode machine.CarryMode
	switches  uint8 // packedPreserveFlags, packedIndexed, packedLong, packedFar
}

const (
	packedPreserveFlags uint8 = 1 << iota
	packedIndexed
	packedLong
	packedFar
)

// packedExtra holds the fields of an instruction that has any of them set.
type packedExtra struct {
	data          []byte
	symbol        string
	table         []int
	callAreaBytes int
}

// newPhysicalCode reserves room for count packed instructions, extras of
// which keep a packedExtra.
func newPhysicalCode(count, extras int) physicalCode {
	code := physicalCode{packed: make([]packedInsn, 0, count)}
	if extras != 0 {
		code.extra = make([]packedExtra, 0, extras)
	}
	return code
}

// needsExtra reports whether ins keeps a packedExtra.
func needsExtra(ins *machine.Instruction) bool {
	return ins.Data != nil || ins.Symbol != "" || ins.Table != nil || ins.CallAreaBytes != 0
}

// countExtras counts the instructions that keep a packedExtra.
func countExtras(insns []machine.Instruction) int {
	n := 0
	for i := range insns {
		if needsExtra(&insns[i]) {
			n++
		}
	}
	return n
}

// pack stores ins and pc into p, every field but extra. It writes p in
// place, field by field: a packedInsn built aside and copied in would be
// read back with wide loads across those narrow stores, which do not
// forward.
func pack(p *packedInsn, ins *machine.Instruction, pc int) {
	p.imm, p.disp, p.pc, p.slot = ins.Imm, ins.Disp, pc, ins.Slot
	p.vectorType, p.op, p.bits, p.flagRequest = ins.VectorType, ins.Op, ins.Bits, ins.FlagRequest
	p.arithmetic, p.condition, p.comparison = ins.Arithmetic, ins.Condition, ins.Comparison
	p.byteOrder, p.barrier = ins.ByteOrder, ins.Barrier
	p.a, p.b, p.c, p.d, p.e, p.highA, p.highB, p.highC = ins.A, ins.B, ins.C, ins.D, ins.E, ins.HighA, ins.HighB, ins.HighC
	p.carryMode = ins.CarryMode
	switches := uint8(0)
	if ins.PreserveFlags {
		switches |= packedPreserveFlags
	}
	if ins.Indexed {
		switches |= packedIndexed
	}
	if ins.Long {
		switches |= packedLong
	}
	if ins.Far {
		switches |= packedFar
	}
	p.switches = switches
}

// count returns the number of instructions.
func (c *physicalCode) count() int { return len(c.located) + len(c.packed) }

// add appends ins at pc to a packed program.
func (c *physicalCode) add(ins *machine.Instruction, pc int) {
	n := len(c.packed)
	if n == cap(c.packed) {
		c.packed = slices.Grow(c.packed, 1)
	}
	c.packed = c.packed[:n+1]
	p := &c.packed[n]
	pack(p, ins, pc)
	p.extra = 0
	if needsExtra(ins) {
		c.extra = append(c.extra, packedExtra{data: ins.Data, symbol: ins.Symbol, table: ins.Table, callAreaBytes: ins.CallAreaBytes})
		p.extra = uint32(len(c.extra))
	}
}

// at returns instruction n, equal to the one stored, field for field. A
// pass over many instructions reads them with decodeFrom instead.
func (c *physicalCode) at(n int) (ins machine.Instruction) {
	if len(c.located) != 0 {
		return c.located[n].ins
	}
	c.decode(n, &ins)
	return ins
}

// decode stores packed instruction n into ins, field by field.
func (c *physicalCode) decode(n int, ins *machine.Instruction) {
	p := &c.packed[n]
	ins.Imm, ins.Disp, ins.Slot = p.imm, p.disp, p.slot
	ins.VectorType, ins.Op, ins.Bits, ins.FlagRequest = p.vectorType, p.op, p.bits, p.flagRequest
	ins.PreserveFlags = p.switches&packedPreserveFlags != 0
	ins.Arithmetic, ins.Condition, ins.Comparison = p.arithmetic, p.condition, p.comparison
	ins.ByteOrder, ins.Barrier = p.byteOrder, p.barrier
	ins.A, ins.B, ins.C, ins.D, ins.E, ins.HighA, ins.HighB, ins.HighC = p.a, p.b, p.c, p.d, p.e, p.highA, p.highB, p.highC
	ins.CarryMode = p.carryMode
	ins.Indexed = p.switches&packedIndexed != 0
	ins.Long = p.switches&packedLong != 0
	ins.Far = p.switches&packedFar != 0
	if p.extra != 0 {
		x := &c.extra[p.extra-1]
		ins.Data, ins.Symbol, ins.Table, ins.CallAreaBytes = x.data, x.symbol, x.table, x.callAreaBytes
	} else {
		ins.Data, ins.Symbol, ins.Table, ins.CallAreaBytes = nil, "", nil, 0
	}
}

// decodeBatch is the number of instructions a pass decodes at a time.
const decodeBatch = 16

// decodeFrom decodes the instructions from n on into batch, as many as fit,
// and returns them. A pass that needs whole instructions reads them a batch
// at a time: copying an instruction just decoded field by field stalls on
// store forwarding, and copying one decoded a batch earlier does not.
func (c *physicalCode) decodeFrom(n int, batch []machine.Instruction) []machine.Instruction {
	batch = batch[:min(len(batch), c.count()-n)]
	if len(c.located) != 0 {
		for i := range batch {
			batch[i] = c.located[n+i].ins
		}
		return batch
	}
	for i := range batch {
		c.decode(n+i, &batch[i])
	}
	return batch
}

// opAt returns instruction n's opcode.
func (c *physicalCode) opAt(n int) machine.Opcode {
	if len(c.located) != 0 {
		return c.located[n].ins.Op
	}
	return c.packed[n].op
}

// pcAt returns instruction n's PC.
func (c *physicalCode) pcAt(n int) int {
	if len(c.located) != 0 {
		return c.located[n].pc
	}
	return c.packed[n].pc
}

// floatProbesFrom sets the floatWriteKey fields of batch's instructions to
// those of the instructions from n on, as many as fit, and leaves their
// other fields as they are. Like decodeFrom, it fills a batch ahead of the
// reads.
func (c *physicalCode) floatProbesFrom(n int, batch []machine.Instruction) {
	batch = batch[:min(len(batch), c.count()-n)]
	for i := range batch {
		probe := &batch[i]
		if len(c.located) != 0 {
			ins := &c.located[n+i].ins
			probe.Op, probe.Bits, probe.Arithmetic = ins.Op, ins.Bits, ins.Arithmetic
			probe.A, probe.B, probe.C, probe.D = ins.A, ins.B, ins.C, ins.D
			continue
		}
		p := &c.packed[n+i]
		probe.Op, probe.Bits, probe.Arithmetic = p.op, p.bits, p.arithmetic
		probe.A, probe.B, probe.C, probe.D = p.a, p.b, p.c, p.d
	}
}

// spillSlots returns referencedSpillSlots of the instructions, reading only
// the fields it needs.
func (c *physicalCode) spillSlots(wordBits uint8) int {
	word, slots := int(wordBits), 0
	for n := range c.located {
		ins := &c.located[n].ins
		if words := spillSlotWords(ins.Op, ins.Bits, word); words != 0 {
			slots = max(slots, ins.Slot+words)
		}
	}
	for n := range c.packed {
		p := &c.packed[n]
		if words := spillSlotWords(p.op, p.bits, word); words != 0 {
			slots = max(slots, p.slot+words)
		}
	}
	return slots
}

// set replaces instruction n and its PC. An instruction keeps its
// packedExtra, which then may hold only zero fields, so that each packed
// instruction has at most one.
func (c *physicalCode) set(n int, ins *machine.Instruction, pc int) {
	if len(c.located) != 0 {
		c.located[n] = locatedInsn{ins: *ins, pc: pc}
		return
	}
	p := &c.packed[n]
	pack(p, ins, pc)
	x := packedExtra{data: ins.Data, symbol: ins.Symbol, table: ins.Table, callAreaBytes: ins.CallAreaBytes}
	switch {
	case p.extra != 0:
		c.extra[p.extra-1] = x
	case needsExtra(ins):
		c.extra = append(c.extra, x)
		p.extra = uint32(len(c.extra))
	}
}

// setPC moves instruction n to pc.
func (c *physicalCode) setPC(n, pc int) {
	if len(c.located) != 0 {
		c.located[n].pc = pc
		return
	}
	c.packed[n].pc = pc
}

// setDisp replaces instruction n's Disp.
func (c *physicalCode) setDisp(n int, disp uint64) {
	if len(c.located) != 0 {
		c.located[n].ins.Disp = disp
		return
	}
	c.packed[n].disp = disp
}

// reset empties the program and keeps its storage for packed adds.
func (c *physicalCode) reset() {
	c.packed, c.extra, c.located = c.packed[:0], c.extra[:0], c.located[:0]
}

// goFrameCode returns the instructions a target's GoFrame reads: the frame
// operations and returns, with their PCs, and the last instruction, which
// tells GoFrame whether code follows a return. GoFrame ignores every other
// instruction, so its frame from these equals its frame from the whole
// program, without a copy of it.
func goFrameCode(code *physicalCode) []machine.LocatedInstruction {
	var located []machine.LocatedInstruction
	last := code.count() - 1
	for n := range code.count() {
		switch code.opAt(n) {
		case pFrameEnter, pFrameLeave, pReturn:
		default:
			if n != last {
				continue
			}
		}
		located = append(located, machine.LocatedInstruction{Instruction: code.at(n), PC: code.pcAt(n)})
	}
	return located
}
