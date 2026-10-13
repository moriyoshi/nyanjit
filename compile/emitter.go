package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

type physicalOp = machine.Opcode
type physicalInsn = machine.Instruction

const (
	pConst            = machine.OpConst
	pSymbol           = machine.OpSymbol
	pMove             = machine.OpMove
	pBinary           = machine.OpBinary
	pFloatBinary      = machine.OpFloatBinary
	pFloatFMA         = machine.OpFloatFMA
	pFloatUnary       = machine.OpFloatUnary
	pBinaryImmediate  = machine.OpBinaryImmediate
	pBinaryCarry      = machine.OpWordCarry
	pInc              = machine.OpInc
	pDec              = machine.OpDec
	pNarrow           = machine.OpNarrow
	pSignExtend       = machine.OpSignExtend
	pShift            = machine.OpShift
	pScaled           = machine.OpScaled
	pMulAdd           = machine.OpMulAdd
	pSelect           = machine.OpSelect
	pSelectFlags      = machine.OpSelectFlags
	pLoad64           = machine.OpLoad64
	pStore64          = machine.OpStore64
	pLoad8            = machine.OpLoad8
	pStore8           = machine.OpStore8
	pLoadAddress      = machine.OpLoadAddress
	pStoreAddress     = machine.OpStoreAddress
	pLoadOffset       = machine.OpLoadOffset
	pStoreOffset      = machine.OpStoreOffset
	pSyscall          = machine.OpSyscall
	pCallC            = machine.OpCallC
	pCallGo           = machine.OpCallGo
	pSaveLink         = machine.OpSaveLink
	pRestoreLink      = machine.OpRestoreLink
	pSpill            = machine.OpSpill
	pReload           = machine.OpReload
	pSaveCFloat       = machine.OpSaveCFloat
	pRestoreCFloat    = machine.OpRestoreCFloat
	pLoadIncoming     = machine.OpLoadIncoming
	pFrameEnter       = machine.OpFrameEnter
	pFrameLeave       = machine.OpFrameLeave
	pReturn           = machine.OpReturn
	pCompareImmediate = machine.OpCompareImmediate
	pCompareRegister  = machine.OpCompareRegister
	pJump             = machine.OpJump
	pJumpIf           = machine.OpJumpIf
	pBranchZero       = machine.OpBranchZero
	pBranchBit        = machine.OpBranchBit
	pLoop             = machine.OpLoop
	pMulWide          = machine.OpMulHigh
)

type locatedInsn struct {
	ins physicalInsn
	pc  int
}

// physicalProgram is the finalized, allocated instruction stream. Its PCs
// are assigned by the emitter, before any assembler sees an instruction.
type physicalProgram struct {
	insns           []locatedInsn
	codeBytes       int
	stackBytes      int
	callAreaBytes   int
	exportSlots     []int
	exportHomes     []exportLocation
	resultPhysical  physReg
	resultWords     [2]physReg // low word first; resultWordCount is one or two
	resultWordCount uint8
	flags           flagState
}

type flagState = machine.FlagState

// physicalEmitter adapts allocator operations to a machine.Stream and handles
// ABI frame and result placement. It never assembles code.
type physicalEmitter struct {
	literalFrame       bool // PPC managed literals need a saved LR for asynchronous unwind.
	hasLiteral         bool
	vectorHomes        int
	stream             *machine.Stream
	hasCall            bool
	callAreaBytes      int
	size               func(physicalInsn) int
	flagEffect         func(physicalInsn) bool
	flagFacts          func(physicalInsn) flagState
	frameBytes         func(int, bool) int
	callBytes          func(bool) int
	stack              StackPractice
	budget             int
	resultReg          physReg
	binarySetsFlags    bool
	decSetsCarry       bool
	linkInFrame        bool
	longJump           bool
	longJumpIf         bool
	farJumpIf          bool
	entrySaves         []physReg
	allocatedSaves     []physReg
	entryFloatSaves    []physReg
	entryResources     []entryResourceSave
	entrySavesForCalls bool
	goReturnOffset     int // 386 Go ABI result slot above the entry SP; zero disables it
}

func (e *physicalEmitter) machineStream() *machine.Stream {
	if e.stream == nil {
		e.stream = machine.NewStream(machine.StreamOptions{Size: e.size, FlagEffect: e.flagEffect, FlagFacts: e.flagFacts,
			BinarySetsFlags: e.binarySetsFlags, DecSetsCarry: e.decSetsCarry})
	}
	return e.stream
}

func savedByEntry(emitter machineEmitter, reg physReg) bool {
	physical, ok := emitter.(*physicalEmitter)
	if !ok {
		return false
	}
	for _, saved := range physical.entrySaves {
		if saved == reg {
			return true
		}
	}
	return false
}

func (e *physicalEmitter) append(ins physicalInsn) {
	if e.longJump && ins.Op == pJump {
		ins.Long = true
	}
	if e.longJumpIf && ins.Op == pJumpIf {
		ins.Long = true
		ins.Far = e.farJumpIf
	}
	e.machineStream().Append(ins)
}

func (e *physicalEmitter) instructionSize(ins physicalInsn) int { return e.size(ins) }

func (e *physicalEmitter) bindFlagValue(value, left, right int, operation op, immediateRight bool) {
	e.machineStream().BindFlagValue(value, left, right, operation, immediateRight)
}

func (e *physicalEmitter) aliasCarryValue(value, source int, width uint8) {
	e.machineStream().AliasCarryValue(value, source, width)
}

func (e *physicalEmitter) forgetFlags() { e.machineStream().ForgetFlags() }

func (e *physicalEmitter) flagsClobbered() bool { return e.machineStream().FlagsClobbered() }
func (e *physicalEmitter) callStackBytes() int  { return e.callBytes(e.hasCall) }
func (e *physicalEmitter) linkSavedInFrame() bool {
	return e.linkInFrame && e.stack == StackPlatform
}
func (e *physicalEmitter) constant(dst physReg, value uint64) {
	e.append(physicalInsn{Op: pConst, A: dst, Imm: value})
}
func (e *physicalEmitter) symbol(dst physReg, name string) {
	e.append(physicalInsn{Op: pSymbol, A: dst, Symbol: name})
}
func (e *physicalEmitter) move(dst, src physReg) {
	e.append(physicalInsn{Op: pMove, A: dst, B: src})
}
func (e *physicalEmitter) binary(operation op, bits uint8, dst, left, right physReg) {
	e.append(physicalInsn{Op: pBinary, Arithmetic: operation, Bits: bits, A: dst, B: left, C: right})
}
func (e *physicalEmitter) floatBinary(operation op, bits uint8, dst, left, right physReg) {
	e.append(physicalInsn{Op: pFloatBinary, Arithmetic: operation, Bits: bits, A: dst, B: left, C: right})
}
func (e *physicalEmitter) floatUnary(operation op, bits uint8, dst, input physReg) {
	e.append(physicalInsn{Op: pFloatUnary, Arithmetic: operation, Bits: bits, A: dst, B: input})
}
func (e *physicalEmitter) floatFMA(bits uint8, dst, x, y, addend physReg) {
	e.append(physicalInsn{Op: pFloatFMA, Bits: bits, A: dst, B: x, C: y, D: addend})
}
func (e *physicalEmitter) binaryImmediate(operation op, bits uint8, dst, left physReg, immediate uint64) {
	e.append(physicalInsn{Op: pBinaryImmediate, Arithmetic: operation, Bits: bits, A: dst, B: left, Imm: immediate})
}
func (e *physicalEmitter) binaryCarry(operation op, dst, left, right physReg) {
	e.append(physicalInsn{Op: pBinaryCarry, Arithmetic: operation, Bits: 32, A: dst, B: left, C: right, CarryMode: machine.CarryFlags})
}
func (e *physicalEmitter) mulWide(right physReg) {
	e.append(physicalInsn{Op: pMulWide, Bits: 32, A: 2, B: 0, C: right})
}
func (e *physicalEmitter) increment(dst, src physReg) {
	e.append(physicalInsn{Op: pInc, A: dst, B: src})
}
func (e *physicalEmitter) decrement(dst, src physReg) {
	e.append(physicalInsn{Op: pDec, A: dst, B: src})
}
func (e *physicalEmitter) narrow(dst, src physReg, bits uint8) {
	var request machine.FlagMask
	if e.flagFacts != nil {
		insns := e.machineStream().Instructions()
		if len(insns) != 0 {
			last := insns[len(insns)-1]
			// Parameter and FP normalization preserve integer flags. Integer
			// arithmetic truncation can establish a fresh width-correct zero.
			if last.A == src && (last.Op == pBinary || last.Op == pBinaryImmediate || last.Op == pInc || last.Op == pDec) {
				request = machine.FlagZero
			}
		}
	}
	e.append(physicalInsn{Op: pNarrow, A: dst, B: src, Imm: uint64(bits), FlagRequest: request})
}
func (e *physicalEmitter) signExtend(dst, src physReg, bits uint8) {
	var request machine.FlagMask
	if e.flagFacts != nil {
		request = machine.FlagSign | machine.FlagZero
	}
	e.append(physicalInsn{Op: pSignExtend, A: dst, B: src, Imm: uint64(bits), FlagRequest: request})
}
func (e *physicalEmitter) shift(operation op, width uint8, dst, src physReg, count uint8) {
	e.append(physicalInsn{Op: pShift, Arithmetic: operation, Bits: width, A: dst, B: src, Imm: uint64(count)})
}
func (e *physicalEmitter) shiftBy(operation op, width uint8, dst, src, count physReg) {
	e.append(physicalInsn{Op: machine.OpShiftBy, Arithmetic: operation, Bits: width, A: dst, B: src, C: count})
}

func (e *physicalEmitter) scaled(dst, base, index physReg, scale, disp uint64) {
	e.append(physicalInsn{Op: pScaled, A: dst, B: base, C: index, Imm: scale, Disp: disp})
}

func (e *physicalEmitter) mulAdd(dst, base, index, factor physReg) {
	e.append(physicalInsn{Op: pMulAdd, A: dst, B: base, C: index, D: factor})
}
func (e *physicalEmitter) selectValue(dst, condition, ifTrue, ifFalse physReg) {
	e.append(physicalInsn{Op: pSelect, A: dst, B: condition, C: ifTrue, D: ifFalse})
}
func (e *physicalEmitter) selectFlags(dst, ifTrue, ifFalse physReg, condition BranchCondition) {
	e.append(physicalInsn{Op: pSelectFlags, A: dst, C: ifTrue, D: ifFalse, Condition: condition})
}
func (e *physicalEmitter) currentFlags() flagState { return e.machineStream().Flags() }
func (e *physicalEmitter) load64(dst, address physReg, order ir.ByteOrder) {
	e.append(physicalInsn{Op: pLoad64, A: dst, B: address, ByteOrder: order})
}
func (e *physicalEmitter) store64(address, value physReg, order ir.ByteOrder) {
	e.append(physicalInsn{Op: pStore64, A: address, B: value, ByteOrder: order})
}
func (e *physicalEmitter) load8(dst, address physReg) {
	e.append(physicalInsn{Op: pLoad8, A: dst, B: address})
}
func (e *physicalEmitter) store8(address, value physReg) {
	e.append(physicalInsn{Op: pStore8, A: address, B: value})
}
func (e *physicalEmitter) loadAddress(dst, base, index physReg, scale, disp uint64, order ir.ByteOrder) {
	e.append(physicalInsn{Op: pLoadAddress, A: dst, B: base, C: index, Imm: scale, Disp: disp, ByteOrder: order})
}
func (e *physicalEmitter) storeAddress(base, index, value physReg, scale, disp uint64, order ir.ByteOrder) {
	e.append(physicalInsn{Op: pStoreAddress, A: base, B: index, C: value, Imm: scale, Disp: disp, ByteOrder: order})
}
func (e *physicalEmitter) loadOffset(dst, base physReg, disp uint64, order ir.ByteOrder) {
	e.append(physicalInsn{Op: pLoadOffset, A: dst, B: base, Disp: disp, ByteOrder: order})
}
func (e *physicalEmitter) storeOffset(base, value physReg, disp uint64, order ir.ByteOrder) {
	e.append(physicalInsn{Op: pStoreOffset, A: base, B: value, Disp: disp, ByteOrder: order})
}
func (e *physicalEmitter) syscall() { e.append(physicalInsn{Op: pSyscall}) }
func (e *physicalEmitter) callC(effect CallEffect) {
	e.hasCall = true
	e.append(physicalInsn{Op: pCallC, PPC64CABI: effect.PPC64CABI})
}
func (e *physicalEmitter) callGo() {
	e.hasCall = true
	e.append(physicalInsn{Op: pCallGo})
}
func (e *physicalEmitter) saveLink(slot int) {
	e.append(physicalInsn{Op: pSaveLink, Slot: slot})
}
func (e *physicalEmitter) restoreLink(slot int) {
	e.append(physicalInsn{Op: pRestoreLink, Slot: slot})
}
func (e *physicalEmitter) spill(src physReg, slot int) {
	e.append(physicalInsn{Op: pSpill, A: src, Slot: slot})
}
func (e *physicalEmitter) reload(dst physReg, slot int) {
	e.append(physicalInsn{Op: pReload, A: dst, Slot: slot})
}
func (e *physicalEmitter) incomingWord(dst physReg, entrySPOffset int) {
	e.append(physicalInsn{Op: pLoadIncoming, A: dst, Imm: uint64(entrySPOffset)})
}

// body finalizes a fragment's result register without adding a function frame
// or return. The control scheduler owns the shared frame and branches.
func (e *physicalEmitter) body(result physReg, slots int) (physicalProgram, error) {
	return e.bodyMode(result, slots, true)
}

func (e *physicalEmitter) bodyBare(result physReg, slots int) (physicalProgram, error) {
	return e.bodyMode(result, slots, false)
}

func (e *physicalEmitter) bodyMode(result physReg, slots int, copyResult bool) (physicalProgram, error) {
	frame := 0
	if e.stack == StackPlatform {
		frame = e.frameBytes(slots, e.hasCall || e.literalFrame && e.hasLiteral)
		if frame+e.callStackBytes() > e.budget {
			return physicalProgram{}, ErrStackRequired
		}
	}
	if copyResult && result != e.resultReg {
		e.move(e.resultReg, result)
	}
	stream := e.machineStream()
	program := physicalProgram{insns: make([]locatedInsn, 0, len(stream.Instructions())), stackBytes: frame, callAreaBytes: e.callAreaBytes, flags: stream.Flags()}
	for _, ins := range stream.Instructions() {
		program.insns = append(program.insns, locatedInsn{ins: ins, pc: program.codeBytes})
		program.codeBytes += e.size(ins)
	}
	return program, nil
}

func (e *physicalEmitter) finish(result physReg, slots int) (physicalProgram, error) {
	return e.finishRegisters(result, slots, e.resultReg)
}

func (e *physicalEmitter) finishRegisters(result physReg, slots int, destination physReg) (physicalProgram, error) {
	e.planEntryPreservation(e.hasCall)
	resources, endSlots := e.entryResourcePlan(slots + len(e.entrySaves) + len(e.entryFloatSaves))
	body, err := e.bodyMode(result, endSlots, false)
	if err != nil {
		return physicalProgram{}, err
	}
	if result != destination {
		e.move(destination, result)
	}
	frame := body.stackBytes
	stream := e.machineStream()
	insns := make([]physicalInsn, 0, len(stream.Instructions())+4)
	if frame != 0 {
		insns = append(insns, physicalInsn{Op: pFrameEnter, Imm: uint64(frame), CallAreaBytes: e.callAreaBytes})
	}
	for i, reg := range e.entrySaves {
		insns = append(insns, physicalInsn{Op: pSpill, A: reg, Slot: slots + i})
	}
	for i, reg := range e.entryFloatSaves {
		insns = append(insns, physicalInsn{Op: pSaveCFloat, A: reg, Slot: slots + len(e.entrySaves) + i})
	}
	for _, save := range resources {
		insns = append(insns, physicalInsn{Op: save.Save, A: save.Register, Slot: save.Slot})
	}
	insns = append(insns, stream.Instructions()...)
	for i := len(resources) - 1; i >= 0; i-- {
		save := resources[i]
		insns = append(insns, physicalInsn{Op: save.Restore, A: save.Register, Slot: save.Slot})
	}
	for i := len(e.entryFloatSaves) - 1; i >= 0; i-- {
		insns = append(insns, physicalInsn{Op: pRestoreCFloat, A: e.entryFloatSaves[i], Slot: slots + len(e.entrySaves) + i})
	}
	for i := len(e.entrySaves) - 1; i >= 0; i-- {
		insns = append(insns, physicalInsn{Op: pReload, A: e.entrySaves[i], Slot: slots + i})
	}
	if frame != 0 {
		insns = append(insns, physicalInsn{Op: pFrameLeave, Imm: uint64(frame)})
	}
	if e.goReturnOffset != 0 {
		insns = append(insns,
			physicalInsn{Op: pStoreOffset, A: 4, B: 0, Disp: uint64(e.goReturnOffset)},
			physicalInsn{Op: pStoreOffset, A: 4, B: 2, Disp: uint64(e.goReturnOffset + 4)},
		)
	}
	insns = append(insns, physicalInsn{Op: pReturn})
	program := physicalProgram{insns: make([]locatedInsn, 0, len(insns)), stackBytes: frame}
	for _, ins := range insns {
		program.insns = append(program.insns, locatedInsn{ins: ins, pc: program.codeBytes})
		program.codeBytes += e.size(ins)
	}
	stream.Replace(insns)
	return program, nil
}

// finishPair transfers a two-word result to its ABI registers. A swapped
// source pair uses one allocator spill slot to preserve the first word.
func (e *physicalEmitter) finishPair(low, high, abiLow, abiHigh physReg, slots int) (physicalProgram, int, error) {
	if abiLow == abiHigh || low == high {
		return physicalProgram{}, 0, ErrInvalidFunction
	}
	if low == abiHigh && high == abiLow {
		e.spill(low, slots)
		e.move(abiHigh, high)
		e.reload(abiLow, slots)
		slots++
	} else if high == abiLow {
		e.move(abiHigh, high)
		e.move(abiLow, low)
	} else {
		e.move(abiLow, low)
		e.move(abiHigh, high)
	}
	program, err := e.finishRegisters(abiLow, slots, abiLow)
	if err != nil {
		return physicalProgram{}, 0, err
	}
	program.resultWords = [2]physReg{abiLow, abiHigh}
	program.resultWordCount = 2
	return program, slots, nil
}

// finishNarrowPair zero-extends a one-word result across a two-word ABI result.
// Copy the low word first because it may currently occupy the high ABI register.
func (e *physicalEmitter) finishNarrowPair(source, abiLow, abiHigh physReg, slots int) (physicalProgram, error) {
	if abiLow == abiHigh || e.resultReg != abiLow {
		return physicalProgram{}, ErrInvalidFunction
	}
	if source != abiLow {
		e.move(abiLow, source)
	}
	e.constant(abiHigh, 0)
	program, err := e.finish(abiLow, slots)
	if err != nil {
		return physicalProgram{}, err
	}
	program.resultWords = [2]physReg{abiLow, abiHigh}
	program.resultWordCount = 2
	return program, nil
}

// entryResourceSave describes ABI preservation planned before physical emission.
// Words and Align are measured in native allocator spill words.
type entryResourceSave struct {
	Save, Restore machine.Opcode
	Register      physReg
	Words, Align  int
}

type locatedEntryResource struct {
	entryResourceSave
	Slot int
}

func (e *physicalEmitter) entryResourcePlan(base int) ([]locatedEntryResource, int) {
	next := base
	plan := make([]locatedEntryResource, 0, len(e.entryResources))
	for _, save := range e.entryResources {
		next = (next + save.Align - 1) &^ (save.Align - 1)
		plan = append(plan, locatedEntryResource{entryResourceSave: save, Slot: next})
		next += save.Words
	}
	return plan, next
}

func (e *physicalEmitter) planEntryPreservation(hasCall bool) {
	if e.entrySavesForCalls && !hasCall {
		e.entrySaves, e.entryFloatSaves, e.entryResources = nil, nil, nil
	}
	for _, r := range e.allocatedSaves {
		exists := false
		for _, old := range e.entrySaves {
			exists = exists || old == r
		}
		if !exists {
			e.entrySaves = append(e.entrySaves, r)
		}
	}
}

func (e *physicalEmitter) mulHigh(width uint8, dst, left, right physReg) {
	e.append(physicalInsn{Op: machine.OpMulHigh, Bits: width, A: dst, B: left, C: right})
}

func (e *physicalEmitter) carryBit(operation op, width uint8, dst, left, right physReg, mode machine.CarryMode) {
	e.append(physicalInsn{Op: machine.OpCarryBit, Arithmetic: operation, Bits: width, A: dst, B: left, C: right, CarryMode: mode})
}
func (e *physicalEmitter) wordCarry(operation op, width uint8, dst, left, right, carry, lowRight physReg, mode machine.CarryMode) {
	e.append(physicalInsn{Op: machine.OpWordCarry, Arithmetic: operation, Bits: width, A: dst, B: left, C: right, D: carry, E: lowRight, CarryMode: mode})
}

func (e *physicalEmitter) aliasFlagValue(value, source int, width uint8) {
	e.machineStream().AliasFlagValue(value, source, width)
}

func (e *physicalEmitter) vectorInstruction(ins machine.Instruction) { e.append(ins) }
func (e *physicalEmitter) vectorSpill(reg physReg, slot int) {
	e.append(machine.Instruction{Op: machine.OpVectorSpill, A: reg, Slot: slot})
}
func (e *physicalEmitter) vectorReload(reg physReg, slot int) {
	e.append(machine.Instruction{Op: machine.OpVectorReload, A: reg, Slot: slot})
}
