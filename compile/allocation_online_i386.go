package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// The 386 expansions on the regalloc.Allocator protocol. A 64-bit integer is
// a pair of registers there, and the direct walk expands the instructions on
// pairs itself. Each expansion here is one position: words it streams from
// memory are Memory uses, its fixed result registers a Fixed definition, its
// fixed scratch registers clobbers, and the scratch registers it chooses
// definitions of values of their own that nothing uses, which Online keeps
// apart from the result. A temporary word goes to the first slot Online never
// uses, known once the walk ends: the walk patches its spills and reloads.
// The emitters are the direct walk's, with the registers and memory Online
// gives.

// An online386 kind is the direct walk's expansion of a 386 instruction.
const (
	o386Generic    uint8 = iota // the common path, on single words
	o386Vector64                // a 64-bit vector splat, extract or insert-zero
	o386FloatMove               // a float move, perhaps between a pair and a vector register
	o386Symbol                  // a pair symbol address
	o386StreamMul               // a pair multiply without a stack
	o386Stream                  // a pair shift or select without a stack, or an access at a pair address
	o386ShiftImm                // a pair shift by a constant
	o386Wide                    // a 64-bit load or store at a single-word address
	o386Byte                    // a byte store, a pair scaled add, or a byte load with a pair
	o386Select                  // a pair select
	o386ShiftBy                 // a shift by a value
	o386Mul                     // a pair multiply
	o386Move                    // a pair move
	o386Widen                   // a sign or zero extension to a pair
	o386Binary                  // a pair add, subtract, and, or or xor
	o386Divide                  // a pair division
	o386IntToFloat              // a conversion of a pair to a float
	o386FloatToInt              // a conversion of a float to a pair
)

// online386Words is a value's register words, as the direct walk counts them.
func online386Words(p *machineProgram, id int) int {
	if v := &p.Values[id]; v.Class != backend.Float && v.Words > 1 {
		return int(v.Words)
	}
	return 1
}

// plan386 classifies each instruction of s.program into s.temps.online386,
// counts the scratch values and reports whether Online can take them all:
// otherwise the direct walk takes the program and reports what it rejects.
func plan386(s *onlineSetup) bool {
	p, t := &s.program, s.temps
	kinds := resetBuffer(&t.online386, len(p.Instructions))
	t.online386Scratch, t.online386Patches = 0, t.online386Patches[:0]
	stack := s.emitter.(*physicalEmitter).stack
	workspace := stack == abi.StackNoUse
	words := func(id int) int { return online386Words(p, id) }
	inPool := func(r physReg) bool { return s.config.Registers.Contains(r) }
	for _, v := range p.Values {
		if v.EntryReg >= 0 && v.Class != backend.Float && v.Words > 1 {
			return false
		}
	}
	if len(p.ResultParts) != 0 {
		return false
	}
	// A control block's pair result returns through its exit's moves.
	if !p.DiscardResult && words(p.Result) == 2 && !s.bare && !(s.final && len(s.entry.result.Words) == 2) && s.block == nil {
		return false
	}
	if p.DirectExports {
		for _, id := range p.Exports {
			if words(id) != 1 {
				return false
			}
		}
	}
	var buffer [4]int
	for pos := range p.Instructions {
		ins := &p.Instructions[pos]
		kind := o386Generic
		dst2 := ins.Dst >= 0 && words(ins.Dst) == 2
		switch {
		case ins.Op == mConst || ins.Op == machine.SelectedHomeBoundary || isCall(ins.Op):
		case ins.Op == machine.SelectedHomeGet || ins.Op == machine.SelectedHomeSet:
			// A pair goes to a slot home word by word.
			id := ins.Dst
			if ins.Op == machine.SelectedHomeSet {
				id = ins.A
			}
			if words(id) != 1 {
				if h := homeAt(p.HomePlan, p.HomeRegion, ins, 32); h.Register >= 0 || onlineHomeInRegister(p.HomePlan, ins) {
					return false
				}
			}
		case ins.Op == mVector && ins.Bits == 64 && (ins.Arithmetic == ir.OpVSplat || ins.Arithmetic == ir.OpVExtract || ins.Arithmetic == ir.OpVInsertZero):
			kind = o386Vector64
		case ins.Op == machine.SelectedFloatMove:
			kind = o386FloatMove
		case ins.Op == mSymbol && dst2:
			kind = o386Symbol
		case workspace && ins.Op == mBinary && ins.Bits == 64 && ins.Arithmetic == opMul:
			kind = o386StreamMul
			if !inPool(0) || !inPool(1) || !inPool(2) {
				return false
			}
		case workspace && (ins.Op == mShift && dst2 || ins.Op == mSelect && (dst2 || words(ins.A) == 2)) ||
			(ins.Op == mLoad64 || ins.Op == mStore64) && words(ins.A) == 2:
			kind = o386Stream
			n := 3 // a store's pair and scratch
			if ins.Op != mStore64 {
				n = 3 - words(ins.Dst)
			}
			t.online386Scratch += n
		case ins.Op == mShift && dst2:
			if stack != abi.StackPlatform || words(ins.A) != 2 {
				return false
			}
			switch ins.Arithmetic {
			case opShiftLeft, opShiftRight, opShiftRightArithmetic, opRotateLeft, opRotateRight:
			default:
				return false
			}
			kind = o386ShiftImm
		case ins.Op == mLoad64 || ins.Op == mStore64:
			if words(ins.A) != 1 || ins.Op == mStore64 && words(ins.B) != 2 || ins.Op == mLoad64 && words(ins.Dst) != 2 {
				return false
			}
			kind = o386Wide
		case ins.Op == mStore8 || ins.Op == mScaled && dst2 || ins.Op == mLoad8 && (dst2 || words(ins.A) == 2):
			if ins.Op == mStore8 || ins.Op == mLoad8 {
				// The direct walk streams low words when the pairs cannot
				// all fit.
				n := words(ins.A)
				if ins.Op == mStore8 && ins.B != ins.A {
					n += words(ins.B)
				} else if ins.Op == mLoad8 {
					n += words(ins.Dst)
				}
				general := 0
				for r := range s.config.Registers.All() {
					if !backend.IsVectorRegister(r) {
						general++
					}
				}
				if n > general {
					return false
				}
			}
			if words(ins.A) != 2 && !((ins.Op == mStore8 || ins.Op == mLoad8) && words(ins.A) == 1) ||
				ins.Op == mScaled && (words(ins.B) != 2 || words(ins.Dst) != 2) {
				return false
			}
			kind = o386Byte
		case ins.Op == mSelect && (dst2 || words(ins.A) == 2):
			if stack != abi.StackPlatform || !inPool(2) || dst2 && !inPool(6) {
				return false
			}
			kind = o386Select
		case ins.Op == mShiftBy:
			if !inPool(0) || ins.Dst >= 0 && dst2 && !inPool(2) {
				return false
			}
			kind = o386ShiftBy
		case ins.Op == mBinary && ins.Bits == 64 && ins.Arithmetic == opMul:
			if stack != abi.StackPlatform || words(ins.A) != 2 || words(ins.B) != 2 || !inPool(0) || !inPool(2) {
				return false
			}
			kind = o386Mul
		case ins.Op == mDivide && dst2:
			if !inPool(0) || !inPool(2) {
				return false
			}
			kind = o386Divide
		case ins.Op == mIntToFloat && words(ins.A) == 2:
			kind = o386IntToFloat
		case ins.Op == mFloatToInt && dst2:
			if !inPool(0) || !inPool(2) {
				return false
			}
			kind = o386FloatToInt
		case ins.Op == mMove && dst2 && words(ins.A) == 2:
			kind = o386Move
		case dst2 && (ins.Op == mSignExtend || ins.Op == mMove && words(ins.A) == 1):
			kind = o386Widen
		case ins.Op == mBinary && ins.Bits == 64 && dst2 &&
			(ins.Arithmetic == opAdd || ins.Arithmetic == opSub || ins.Arithmetic == opAnd || ins.Arithmetic == opOr || ins.Arithmetic == opXor):
			kind = o386Binary
			t.online386Scratch++
		default:
			// The common path takes single words, but for the low word of
			// a narrowed or partly stored pair.
			if ins.Dst >= 0 && words(ins.Dst) != 1 {
				return false
			}
			for i, id := range ins.InputsInto(buffer[:0]) {
				if words(id) != 1 && !(ins.Op == mNarrow && i == 0 || ins.Op == mLoad16 || ins.Op == mLoad32 || ins.Op == mStore16 || ins.Op == mStore32) {
					return false
				}
			}
		}
		kinds[pos] = kind
	}
	return true
}

// patch386 gives the temporaries of the walk's expansions slot, adding it
// to each patched instruction's word offset.
func patch386(s *onlineSetup, slot int) {
	stream := s.emitter.(*physicalEmitter).machineStream()
	for _, at := range s.temps.online386Patches {
		ins := stream.Instructions()[at]
		ins.Slot += slot
		stream.ReplaceAt(at, ins)
	}
}

// onlineHomeInRegister reports whether ins's variable has a register home in
// any region: a pair never goes to one.
func onlineHomeInRegister(plan *machine.HomePlan, ins *machineInstruction) bool {
	if plan == nil {
		return false
	}
	homes := plan.Regions
	if homes == nil {
		homes = plan.RegisterOverrides
	}
	for _, region := range homes {
		if h, ok := region[ins.Variable]; ok && h.Register >= 0 {
			return true
		}
	}
	return false
}

// describe386 appends the operands of the 386 expansion of kind at pos to
// ops: its inputs, its result and the scratch values from *scratch on, which
// it advances.
func describe386(s *onlineSetup, b onlineBuilder, pos int, kind uint8, inputs []int, lastUse []int, scratch *int, ops []regalloc.Operand) []regalloc.Operand {
	p := &s.program
	ins := &p.Instructions[pos]
	words := func(id int) int { return online386Words(p, id) }
	dies := func(id int) bool { u := lastUse[id]; return u == pos+1 }
	use := func(id int) { ops = append(ops, regalloc.Operand{Value: id}) }
	memory := func(id int) { ops = append(ops, regalloc.Operand{Value: id, Memory: true}) }
	def := func(x regalloc.Operand) {
		x.Value, x.Def = ins.Dst, true
		ops = append(ops, x)
	}
	scratches := func(n int) {
		for range n {
			ops = append(ops, regalloc.Operand{Value: *scratch, Def: true})
			*scratch++
		}
	}
	pair := func(a, b physReg) regalloc.Group { return regalloc.Group{Registers: [2]physReg{a, b}, Words: 2} }
	switch kind {
	case o386Vector64, o386FloatMove:
		for _, id := range inputs {
			use(id)
		}
		def(regalloc.Operand{EarlyClobber: true})
	case o386Symbol:
		def(regalloc.Operand{})
	case o386StreamMul:
		memory(ins.A)
		memory(ins.B)
		def(regalloc.Operand{Fixed: pair(0, 2)})
	case o386Stream:
		for _, id := range inputs {
			memory(id)
		}
		n := 3
		if ins.Op != mStore64 {
			def(regalloc.Operand{})
			n = 3 - words(ins.Dst)
		}
		scratches(n)
	case o386ShiftImm:
		use(ins.A)
		rotation := ins.Arithmetic == opRotateLeft || ins.Arithmetic == opRotateRight
		if dies(ins.A) && !rotation {
			def(regalloc.Operand{Tied: true})
		} else {
			def(regalloc.Operand{EarlyClobber: true})
		}
	case o386Wide:
		use(ins.A)
		if ins.Op == mStore64 {
			use(ins.B)
		} else {
			def(regalloc.Operand{EarlyClobber: true})
		}
	case o386Byte:
		use(ins.A)
		switch ins.Op {
		case mStore8:
			use(ins.B)
		case mScaled:
			use(ins.B)
			def(regalloc.Operand{EarlyClobber: true})
		default:
			def(regalloc.Operand{EarlyClobber: true})
		}
	case o386Select:
		for _, id := range inputs {
			memory(id)
		}
		result := regalloc.SingleRegister(2)
		if words(ins.Dst) == 2 {
			result = pair(2, 6)
		}
		def(regalloc.Operand{Fixed: result})
	case o386ShiftBy:
		memory(ins.A)
		memory(ins.B)
		if ins.Dst >= 0 {
			result := regalloc.SingleRegister(0)
			if words(ins.Dst) == 2 {
				result = pair(0, 2)
			}
			def(regalloc.Operand{Fixed: result})
		}
	case o386Mul:
		memory(ins.A)
		memory(ins.B)
		def(regalloc.Operand{Fixed: pair(0, 2)})
	case o386Move:
		use(ins.A)
		def(regalloc.Operand{Tied: dies(ins.A)})
	case o386Widen:
		use(ins.A)
		def(regalloc.Operand{Tied: words(ins.A) == 2 && dies(ins.A)})
	case o386Binary:
		memory(ins.A)
		memory(ins.B)
		def(regalloc.Operand{})
		scratches(1)
	case o386Divide:
		memory(ins.A)
		memory(ins.B)
		def(regalloc.Operand{Fixed: pair(0, 2)})
	case o386IntToFloat:
		use(ins.A)
		def(regalloc.Operand{})
	case o386FloatToInt:
		use(ins.A)
		def(regalloc.Operand{Fixed: pair(0, 2)})
	}
	at := b.AddPosition(ops...)
	if clobbers := clobbers386(s, kind); clobbers.Len() != 0 {
		b.SetClobbers(at, clobbers)
	}
	return ops
}

// clobbers386 returns the fixed registers the expansion of kind overwrites
// besides its result: the multiply's, the shift's, every general register a
// pair select reuses, and the scratch register of a constant pair shift.
func clobbers386(s *onlineSetup, kind uint8) backend.RegisterSet {
	switch kind {
	case o386StreamMul:
		return onlineGeneralSet(s)
	case o386Select:
		return onlineGeneralSet(s)
	case o386ShiftBy, o386Mul:
		return backend.NewRegisterSet(0, 1, 2, 3)
	case o386ShiftImm:
		return backend.NewRegisterSet(s.target.ScratchRegister())
	case o386Divide:
		return onlineGeneralSet(s)
	case o386FloatToInt:
		return backend.NewRegisterSet(0, 1, 2)
	}
	return backend.RegisterSet{}
}

// onlineGeneralSet returns the allocatable general registers.
func onlineGeneralSet(s *onlineSetup) backend.RegisterSet {
	var list [64]backend.Register
	n := 0
	for r := range s.config.Registers.All() {
		if !backend.IsVectorRegister(r) {
			list[n] = r
			n++
		}
	}
	return backend.NewRegisterSet(list[:n]...)
}

// emit386 emits the 386 expansion of kind at pos, whose operands Online has
// placed.
func emit386(s *onlineSetup, a onlineWalk, pos int, kind uint8) {
	p, emitter, written := &s.program, s.emitter, s.written
	physical := emitter.(*physicalEmitter)
	ins := &p.Instructions[pos]
	words := func(id int) int { return online386Words(p, id) }
	operand := func(i int) regalloc.Group { return a.Operand(pos, i) }
	// The Memory uses are the inputs, in order.
	var buffer [4]int
	inputs := ins.InputsInto(buffer[:0])
	// load writes word of the value of Memory use i to r.
	load := func(r physReg, i int, word int) {
		loc, value := a.Memory(pos, i), inputs[i]
		switch {
		case word >= words(value):
			emitter.constant(r, 0)
		case p.Values[value].Constant:
			emitter.constant(r, uint64(uint32(p.Values[value].Imm>>(32*word))))
		case loc.Slot >= 0:
			emitter.reload(r, loc.Slot+word)
		default:
			emitter.incomingWord(r, loc.Incoming[word])
		}
		written[r] = true
	}
	mark := func(g regalloc.Group) {
		for w := range int(g.Words) {
			written[g.Registers[w]] = true
		}
	}
	// The temporary's slot is patched after the walk; word offsets the
	// temporary words above it.
	stream := physical.machineStream()
	spillWord := func(r physReg, word int) {
		s.temps.online386Patches = append(s.temps.online386Patches, len(stream.Instructions()))
		emitter.spill(r, word)
	}
	spill := func(r physReg) { spillWord(r, 0) }
	appendPatched := func(insn physicalInsn) {
		s.temps.online386Patches = append(s.temps.online386Patches, len(stream.Instructions()))
		physical.append(insn)
	}
	reload := func(r physReg) {
		s.temps.online386Patches = append(s.temps.online386Patches, len(stream.Instructions()))
		emitter.reload(r, 0)
	}
	switch kind {
	case o386Vector64:
		n := len(inputs)
		dst := operand(n)
		src := operand(0)
		insn := physicalInsn{Op: machine.OpVector, Arithmetic: ins.Arithmetic, Bits: ins.Bits, VectorType: ins.Type, A: dst.Registers[0], B: src.Registers[0], Imm: ins.Imm}
		if dst.Words == 2 {
			insn.HighA = dst.Registers[1]
		}
		if src.Words == 2 {
			insn.HighB = src.Registers[1]
		}
		if n > 1 {
			c := operand(1)
			insn.C = c.Registers[0]
			if c.Words == 2 {
				insn.HighC = c.Registers[1]
			}
		}
		emitter.vectorInstruction(insn)
		mark(dst)
	case o386FloatMove:
		src, dst := operand(0), operand(1)
		move := physicalInsn{Op: machine.OpFloatMove, Bits: ins.Bits, A: dst.Registers[0], B: src.Registers[0]}
		if src.Words == 2 {
			move.HighB = src.Registers[1]
		}
		if dst.Words == 2 {
			move.HighA = dst.Registers[1]
		}
		physical.append(move)
		mark(dst)
	case o386Symbol:
		g := operand(0)
		physical.append(physicalInsn{Op: machine.OpSymbol, Bits: 64, A: g.Registers[0], HighA: g.Registers[1], Symbol: ins.Symbol})
		mark(g)
	case o386StreamMul:
		load(0, 0, 0)
		load(1, 1, 0)
		emitter.mulWide(1)
		spill(0)
		load(0, 0, 1)
		emitter.binary(opMul, 32, 0, 0, 1)
		emitter.binary(opAdd, 32, 2, 2, 0)
		load(0, 0, 0)
		load(1, 1, 1)
		emitter.binary(opMul, 32, 0, 0, 1)
		emitter.binary(opAdd, 32, 2, 2, 0)
		reload(0)
		written[0], written[1], written[2] = true, true, true
		emitter.forgetFlags()
	case o386Stream:
		n := len(inputs)
		var lo, hi, scratch physReg
		count := 2
		if ins.Op != mStore64 {
			count = words(ins.Dst)
			g := operand(n)
			lo = g.Registers[0]
			if count == 2 {
				hi, scratch = g.Registers[1], operand(n + 1).Registers[0]
			} else {
				hi, scratch = operand(n + 1).Registers[0], operand(n + 2).Registers[0]
			}
		} else {
			lo, hi, scratch = operand(n).Registers[0], operand(n + 1).Registers[0], operand(n + 2).Registers[0]
		}
		switch {
		case ins.Op == mLoad64 || ins.Op == mStore64:
			load(scratch, 0, 0)
			if ins.Op == mLoad64 {
				emitter.loadOffset(lo, scratch, 0, ins.ByteOrder)
				emitter.loadOffset(hi, scratch, 4, ins.ByteOrder)
			} else {
				load(lo, 1, 0)
				load(hi, 1, 1)
				emitter.storeOffset(scratch, lo, 0, ins.ByteOrder)
				emitter.storeOffset(scratch, hi, 4, ins.ByteOrder)
			}
		case ins.Op == mShift:
			load(lo, 0, 0)
			load(hi, 0, 1)
			physical.append(physicalInsn{Op: machine.OpShift, Arithmetic: ins.Arithmetic, Bits: 64, A: lo, HighA: hi, B: lo, HighB: hi, C: scratch, Imm: ins.Imm})
		default: // a select
			load(scratch, 0, 0)
			if words(ins.A) == 2 {
				load(lo, 0, 1)
				emitter.binary(opOr, 32, scratch, scratch, lo)
			}
			emitter.constant(lo, 0xffffffff)
			emitter.selectValue(scratch, scratch, lo, scratch) // 0 or all ones
			load(lo, 1, 0)
			load(hi, 2, 0)
			emitter.binary(opXor, 32, lo, lo, hi)
			emitter.binary(opAnd, 32, lo, lo, scratch)
			emitter.binary(opXor, 32, lo, lo, hi)
			if count == 2 {
				spill(lo)
				load(hi, 1, 1)
				load(lo, 2, 1)
				emitter.binary(opXor, 32, hi, hi, lo)
				emitter.binary(opAnd, 32, hi, hi, scratch)
				emitter.binary(opXor, 32, hi, hi, lo)
				reload(lo)
			}
		}
		written[lo], written[hi], written[scratch] = true, true, true
		emitter.forgetFlags()
	case o386ShiftImm:
		emitShift386(s, ins, operand(0), operand(1))
	case o386Wide:
		address := operand(0).Registers[0]
		if ins.Op == mStore64 {
			value := operand(1)
			emitter.storeOffset(address, value.Registers[0], 0, ins.ByteOrder)
			emitter.storeOffset(address, value.Registers[1], 4, ins.ByteOrder)
			break
		}
		dst := operand(1)
		emitter.loadOffset(dst.Registers[0], address, 0, ins.ByteOrder)
		emitter.loadOffset(dst.Registers[1], address, 4, ins.ByteOrder)
		mark(dst)
	case o386Byte:
		address := operand(0)
		switch ins.Op {
		case mStore8:
			addrReg, valueReg := address.Registers[0], operand(1).Registers[0]
			if valueReg < 4 {
				emitter.store8(addrReg, valueReg)
				break
			}
			// An x86 byte store can only name AL, CL, DL or BL.
			byteReg := physReg(0)
			if addrReg == byteReg {
				byteReg = 1
			}
			spill(byteReg)
			emitter.move(byteReg, valueReg)
			emitter.store8(addrReg, byteReg)
			reload(byteReg)
		case mScaled:
			index, dst := operand(1), operand(2)
			baseLow, baseHigh := address.Registers[0], address.Registers[1]
			indexLow, indexHigh := index.Registers[0], index.Registers[1]
			dstLow, dstHigh := dst.Registers[0], dst.Registers[1]
			if ins.Imm == 1 {
				emitter.binary(opAdd, 32, dstLow, baseLow, indexLow)
				emitter.binaryCarry(opAdd, dstHigh, baseHigh, indexHigh)
			} else {
				shift := uint8(0)
				switch ins.Imm {
				case 2:
					shift = 1
				case 4:
					shift = 2
				case 8:
					shift = 3
				}
				emitter.shift(opShiftLeft, 32, dstHigh, indexHigh, shift)
				emitter.shift(opShiftRight, 32, dstLow, indexLow, 32-shift)
				emitter.binary(opOr, 32, dstHigh, dstHigh, dstLow)
				emitter.shift(opShiftLeft, 32, dstLow, indexLow, shift)
				emitter.binary(opAdd, 32, dstLow, dstLow, baseLow)
				emitter.binaryCarry(opAdd, dstHigh, dstHigh, baseHigh)
			}
			mark(dst)
		default:
			dst := operand(1)
			emitter.load8(dst.Registers[0], address.Registers[0])
			if dst.Words == 2 {
				emitter.constant(dst.Registers[1], 0)
			}
			mark(dst)
		}
	case o386Select:
		load(0, 0, 0)
		if words(ins.A) == 2 {
			load(1, 0, 1)
			emitter.binary(opOr, 32, 0, 0, 1)
		}
		load(3, 1, 0)
		load(1, 2, 0)
		emitter.selectValue(2, 0, 3, 1)
		written[2] = true
		if words(ins.Dst) == 2 {
			load(3, 1, 1)
			load(1, 2, 1)
			emitter.selectValue(6, 0, 3, 1)
			written[6] = true
		}
		emitter.forgetFlags()
	case o386ShiftBy:
		load(0, 0, 0)
		load(2, 0, 1)
		load(1, 1, 0)
		load(3, 1, 1)
		physical.append(physicalInsn{Op: machine.OpShiftBy, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: 0, B: 0, C: 1, D: 3, E: 2})
	case o386Mul:
		// Only the low 64 bits are needed:
		// a*b = a0*b0 + (a1*b0+a0*b1)<<32 (mod 2^64).
		load(0, 0, 0)
		load(3, 1, 0)
		emitter.mulWide(3) // EDX:EAX = a0*b0
		load(1, 0, 1)
		emitter.binary(opMul, 32, 1, 1, 3)
		emitter.binary(opAdd, 32, 2, 2, 1)
		load(1, 0, 0)
		load(3, 1, 1)
		emitter.binary(opMul, 32, 1, 1, 3)
		emitter.binary(opAdd, 32, 2, 2, 1)
		written[0], written[2] = true, true
		emitter.forgetFlags()
	case o386Divide:
		// Both pairs go to four temporary words, which the expansion
		// reads and overwrites.
		for i := range 2 {
			for word := range 2 {
				load(0, i, word)
				spillWord(0, 2*i+word)
			}
		}
		appendPatched(physicalInsn{Op: machine.OpDivide, Arithmetic: ins.Arithmetic, Bits: 64, A: 0, HighA: 2, Imm: ins.Imm})
		for _, r := range i386DivideClobbers {
			written[r] = true
		}
		emitter.forgetFlags()
	case o386IntToFloat:
		// The two temporary words hold the conversion's memory operands.
		src, dst := operand(0), operand(1)
		spillWord(src.Registers[0], 0)
		spillWord(src.Registers[1], 1)
		appendPatched(physicalInsn{Op: machine.OpIntToFloat, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: dst.Registers[0], B: src.Registers[0], HighB: src.Registers[1], Imm: 64})
		mark(dst)
		emitter.forgetFlags()
	case o386FloatToInt:
		physical.append(physicalInsn{Op: machine.OpFloatToInt, Arithmetic: ins.Arithmetic, Bits: 64, A: 0, HighA: 2, B: operand(0).Registers[0], Imm: ins.Imm, Disp: ins.Disp})
		for _, r := range i386ConvertClobbers {
			written[r] = true
		}
		emitter.forgetFlags()
	case o386Move:
		src, dst := operand(0), operand(1)
		emitPairMove386(s, dst, src)
	case o386Widen:
		src, dst := operand(0), operand(1)
		low, high := dst.Registers[0], dst.Registers[1]
		if ins.Op == mSignExtend {
			emitter.signExtend(low, src.Registers[0], uint8(ins.Imm))
			emitter.shift(opShiftRightArithmetic, 32, high, low, 31)
			emitter.forgetFlags()
		} else {
			emitter.move(low, src.Registers[0])
			emitter.constant(high, 0)
		}
		mark(dst)
	case o386Binary:
		dst, right := operand(2), operand(3).Registers[0]
		for word := range 2 {
			reg := dst.Registers[word]
			load(reg, 0, word)
			load(right, 1, word)
			if word == 1 && (ins.Arithmetic == opAdd || ins.Arithmetic == opSub) {
				emitter.binaryCarry(ins.Arithmetic, reg, reg, right)
			} else {
				emitter.binary(ins.Arithmetic, 32, reg, reg, right)
			}
		}
		emitter.forgetFlags()
	}
}

// emitPairMove386 copies the pair src to dst, word by word in an order that
// reads each word before it is overwritten.
func emitPairMove386(s *onlineSetup, dst, src regalloc.Group) {
	emitter, written := s.emitter, s.written
	lo, hi := 0, 1
	if dst.Registers[0] == src.Registers[1] {
		lo, hi = 1, 0 // the high word first
		if dst.Registers[1] == src.Registers[0] {
			// A swap: go through the scratch register.
			scratch := s.target.ScratchRegister()
			emitter.move(scratch, src.Registers[0])
			emitter.move(dst.Registers[0], src.Registers[1])
			emitter.move(dst.Registers[1], scratch)
			written[dst.Registers[0]], written[dst.Registers[1]], written[scratch] = true, true, true
			return
		}
	}
	for _, w := range [2]int{lo, hi} {
		if dst.Registers[w] != src.Registers[w] {
			emitter.move(dst.Registers[w], src.Registers[w])
			written[dst.Registers[w]] = true
		}
	}
}

// emitShift386 emits a pair shift or rotation by a constant from source to
// dst through the scratch register, as the direct walk does.
func emitShift386(s *onlineSetup, ins *machineInstruction, source, dst regalloc.Group) {
	emitter, written := s.emitter, s.written
	low, high := source.Registers[0], source.Registers[1]
	outLow, outHigh := dst.Registers[0], dst.Registers[1]
	count := uint8(ins.Imm)
	scratch := s.target.ScratchRegister()
	shiftOrMove := func(operation opcode, dst, src physReg, amount uint8) {
		if amount == 0 {
			emitter.move(dst, src)
		} else {
			emitter.shift(operation, 32, dst, src, amount)
		}
	}
	switch ins.Arithmetic {
	case opShiftLeft:
		if count == 0 {
			emitter.move(outLow, low)
			emitter.move(outHigh, high)
		} else if count < 32 {
			emitter.shift(opShiftRight, 32, scratch, low, 32-count)
			emitter.shift(opShiftLeft, 32, outHigh, high, count)
			emitter.binary(opOr, 32, outHigh, outHigh, scratch)
			emitter.shift(opShiftLeft, 32, outLow, low, count)
			written[scratch] = true
		} else {
			shiftOrMove(opShiftLeft, outHigh, low, count-32)
			emitter.constant(outLow, 0)
		}
	case opShiftRight, opShiftRightArithmetic:
		if count == 0 {
			emitter.move(outLow, low)
			emitter.move(outHigh, high)
		} else if count < 32 {
			emitter.shift(opShiftLeft, 32, scratch, high, 32-count)
			emitter.shift(opShiftRight, 32, outLow, low, count)
			emitter.binary(opOr, 32, outLow, outLow, scratch)
			emitter.shift(ins.Arithmetic, 32, outHigh, high, count)
			written[scratch] = true
		} else {
			shiftOrMove(ins.Arithmetic, outLow, high, count-32)
			if ins.Arithmetic == opShiftRightArithmetic {
				emitter.shift(opShiftRightArithmetic, 32, outHigh, high, 31)
			} else {
				emitter.constant(outHigh, 0)
			}
		}
	default: // a rotation
		if count >= 32 {
			low, high = high, low
			count -= 32
		}
		if count == 0 {
			emitter.move(outLow, low)
			emitter.move(outHigh, high)
		} else if ins.Arithmetic == opRotateLeft {
			emitter.shift(opShiftRight, 32, scratch, high, 32-count)
			emitter.shift(opShiftLeft, 32, outLow, low, count)
			emitter.binary(opOr, 32, outLow, outLow, scratch)
			emitter.shift(opShiftRight, 32, scratch, low, 32-count)
			emitter.shift(opShiftLeft, 32, outHigh, high, count)
			emitter.binary(opOr, 32, outHigh, outHigh, scratch)
			written[scratch] = true
		} else {
			emitter.shift(opShiftLeft, 32, scratch, high, 32-count)
			emitter.shift(opShiftRight, 32, outLow, low, count)
			emitter.binary(opOr, 32, outLow, outLow, scratch)
			emitter.shift(opShiftLeft, 32, scratch, low, 32-count)
			emitter.shift(opShiftRight, 32, outHigh, high, count)
			emitter.binary(opOr, 32, outHigh, outHigh, scratch)
			written[scratch] = true
		}
	}
	emitter.forgetFlags()
	written[outLow], written[outHigh] = true, true
}
