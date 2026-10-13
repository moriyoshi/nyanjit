package compile

import (
	"fmt"
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// Float passing at the baseline tier (Func.Floats and the Imm of OpCallC
// and OpCallGo, an abi.FloatPassing).
//
// abi.PreflightCall names the FP registers of both boundaries: an entry
// takes the registers an outbound call of its convention passes. A binary64
// value has the same bits in both banks, so it moves as any value does. A
// binary32 value crosses into the engine through pkFloatABI (machine
// OpFloatMove of 32 bits, which gives its bits zero-extended in a general
// register), so the FP bank's invariants for binary32 (cleared upper bits,
// or NaN-boxing on riscv64) hold whatever the caller or callee left in the
// register's other bits. Out of the engine, the existing moves already give
// the ABI's form, except on ppc64le, whose FPRs hold binary32 in double
// format (baselineModel.singleABI): those moves convert (mvConv). 386 passes
// floats in stack words; only a C result changes, to x87 ST(0).

// baselineFloatEntry is the entry's float passing: each parameter's entry
// register and the result register, from abi.PreflightCall.
type baselineFloatEntry struct {
	floats abi.FloatPassing
	params []int8
	result int8
	layout abi.CallEffect
}

// Move flags (bMove.fl).
const (
	// mvConv converts a binary32 general-register image to the FP ABI's
	// representation (ppc64le double format).
	mvConv uint8 = 1 << iota
	// mvCall marks an outbound call's argument move: an FP constant is
	// built in the cycle register, since the transit register may be the
	// call target.
	mvCall
)

// callFloats returns call record r's float passing.
func (e *baselineEngine) callFloats(r *bRec) abi.FloatPassing {
	if r.op == ir.OpSyscall {
		return 0
	}
	if r.fl&rfEffect != 0 {
		return e.effects[r.imm].Floats
	}
	return abi.FloatPassing(r.imm)
}

// baselineCallEffect is cachedCallEffect, with more than eight float
// arguments an argument limit.
func baselineCallEffect(spec abi.CallSpec) (abi.CallEffect, error) {
	if n := countFloatArguments(spec.Floats); n > 8 {
		return abi.CallEffect{}, fmt.Errorf("%d float arguments, limit 8: %w", n, ErrBackendLimit)
	}
	return cachedCallEffect(spec)
}

// checkFloats checks Func.Floats against the program and records the
// entry's float registers: marked parameters are 32- or 64-bit, and a
// float result requires every returned value to have its width.
func (e *baselineEngine) checkFloats() error {
	f := &e.floatEntry
	f.floats, f.params, f.result = e.options.Floats, f.params[:0], noReg
	floats := f.floats
	if floats == 0 {
		return nil
	}
	if floats&abi.FloatResult32 != 0 && floats&abi.FloatResult64 != 0 {
		return fmt.Errorf("Func.Floats marks both result widths: %w", ErrInvalidFunction)
	}
	n := len(e.params)
	if n < 62 && floats&(abi.FloatResult32-1)>>n != 0 {
		return fmt.Errorf("Func.Floats marks a parameter beyond the %d parameters: %w", n, ErrInvalidFunction)
	}
	for k, v := range e.params {
		if w := e.recs[v].bits; floats.Argument(k) && w != 32 && w != 64 {
			return fmt.Errorf("float parameter %d has %d bits: %w", k, w, ErrInvalidFunction)
		}
	}
	if w := floats.ResultBits(); w != 0 {
		for b := range e.blocks {
			blk := &e.blocks[b]
			if blk.term != ir.TermReturn {
				continue
			}
			if blk.parts || e.valueBits(blk.result) != w {
				return fmt.Errorf("a float result of %d bits returns a value of %d bits: %w", w, e.valueBits(blk.result), ErrInvalidFunction)
			}
		}
	}
	effect, err := baselineCallEffect(abi.CallSpec{Architecture: e.target.architecture, Kind: abi.FunctionCall, ABI: e.options.ABI, ArgumentCount: n, Floats: floats})
	if err != nil {
		return err
	}
	f.layout = effect
	if e.target.architecture == "386" {
		return nil
	}
	for _, name := range effect.ArgumentRegisters {
		r, ok := e.arch.register(name)
		if !ok {
			return ErrBackendLimit
		}
		f.params = append(f.params, r)
	}
	if floats.ResultBits() != 0 {
		r, ok := e.arch.register(effect.ResultRegister)
		if !ok {
			return ErrBackendLimit
		}
		f.result = r
	}
	return nil
}

// floatEntryLayout replaces an entry layout's locations with the float
// passing's: FP argument and result registers (386: a C result in ST(0)).
func (e *baselineEngine) floatEntryLayout(entry abiEntryLayout) abiEntryLayout {
	f := &e.floatEntry
	if f.floats == 0 {
		return entry
	}
	if e.target.architecture != "386" {
		entry.arguments = make([]abi.ValueLocation, len(f.layout.ArgumentRegisters))
		for k, name := range f.layout.ArgumentRegisters {
			entry.arguments[k] = registerABIValue(name)
		}
	}
	// A 386 Go result stays in the caller's stack slot.
	if f.floats.ResultBits() != 0 && len(f.layout.ResultLocation.Words) != 0 && (e.target.architecture != "386" || e.options.ABI == abi.C) {
		entry.result = f.layout.ResultLocation
	}
	return entry
}

// enterFloatParams binds the live entry parameters of a function with float
// passing: integers as enterBlock does, binary64 values in their FP
// register (moved to an allocatable one if needed) and binary32 values,
// through pkFloatABI, in a general register.
func (e *baselineEngine) enterFloatParams() {
	a := &e.alloc
	f := &e.floatEntry
	lock := uint64(0)
	for _, r := range f.params {
		lock |= regBit(r)
	}
	for k, v := range e.params {
		if k >= len(f.params) {
			break
		}
		r := f.params[k]
		if a.vs[v].next == noValue || a.vstamp[v] != a.stamp {
			continue
		}
		bits := e.recs[v].bits
		switch {
		case !f.floats.Argument(k):
			e.bind(v, r)
			if bits < 64 {
				// Callers need not extend narrow arguments.
				e.put(bPhys{k: pkOp, sub: uint8(ir.OpCastUnsigned), w: bits, a: r, b: r, d: 64})
			}
		case bits == 32:
			d := e.allocReg(bankGP, lock, -1, false)
			e.put(bPhys{k: pkFloatABI, w: 32, a: d, b: r})
			a.used |= regBit(d) | regBit(e.m.scratchFP)
			e.bind(v, d)
			lock |= regBit(d)
		case e.m.allocatable&regBit(r) == 0:
			d := e.allocReg(bankFP, lock, -1, false)
			e.put(bPhys{k: pkMove, w: 64, a: d, b: r})
			a.used |= regBit(d)
			e.bind(v, d)
			lock |= regBit(d)
		default:
			e.bind(v, r)
		}
		e.defined(v)
	}
}

// floatResult binds call record r's FP result, value i, which the callee
// left in register res: binary64 in the FP bank, binary32 through
// pkFloatABI in a general register.
func (e *baselineEngine) floatResult(i uint32, r *bRec, res int8) {
	a := &e.alloc
	if r.bits == 32 {
		d := e.def(i, bankGP, 0)
		e.put(bPhys{k: pkFloatABI, w: 32, a: d, b: res})
		a.used |= regBit(d) | regBit(e.m.scratchFP)
		e.defined(i)
		return
	}
	if e.m.allocatable&regBit(res) == 0 {
		d := e.def(i, bankFP, 0)
		e.put(bPhys{k: pkMove, w: 64, a: d, b: res})
		a.used |= regBit(d)
		e.defined(i)
		return
	}
	e.bind(i, res)
	e.defined(i)
}

// floatMoveFlags returns the move flags for value v moving to register dst
// at a float boundary.
func (e *baselineEngine) floatMoveFlags(dst int8, v uint32, call bool) uint8 {
	fl := uint8(0)
	if call && dst >= fpBase {
		fl |= mvCall
	}
	if e.m.singleABI && dst >= fpBase && e.valueBits(v) == 32 {
		fl |= mvConv
	}
	return fl
}

// emitConvMove emits a mvConv move of a binary32 value to FP register dst
// from src, a general register, a slot or (rarely) an FP register.
func (e *baselineEngine) emitConvMove(dst, src int16) {
	a := &e.alloc
	switch {
	case src < fpBase:
		e.put(bPhys{k: pkFloatABI, w: 32, a: int8(dst), b: int8(src)})
	case src < bSlotLoc:
		// The FP bank holds the raw bits: through a general register.
		e.put(bPhys{k: pkMove, w: 64, a: e.m.singleTemp, b: int8(src)})
		e.put(bPhys{k: pkFloatABI, w: 32, a: int8(dst), b: e.m.singleTemp})
		a.used |= regBit(e.m.singleTemp)
	default:
		// A converting load of the slot's low word.
		e.put(bPhys{k: pkReload, sub: 1, w: 32, a: int8(dst), imm: uint64(src - bSlotLoc)})
	}
	a.used |= regBit(int8(dst))
}

// floatConstant materializes constant v in FP register dst for a call or
// conversion move, building it in the cycle register.
func (e *baselineEngine) floatConstant(dst int8, v uint32, fl uint8) {
	a := &e.alloc
	c := &e.recs[v]
	t := e.m.cycle
	if c.fl&rfSymbol != 0 {
		e.put(bPhys{k: pkSymbol, w: 64, a: t, imm: c.imm})
	} else {
		e.put(bPhys{k: pkConst, w: 64, a: t, imm: c.imm})
	}
	if fl&mvConv != 0 {
		e.put(bPhys{k: pkFloatABI, w: 32, a: dst, b: t})
	} else {
		// At the value's width: riscv64 NaN-boxes a binary32.
		e.put(bPhys{k: pkMove, w: e.valueBits(v), a: dst, b: t})
	}
	a.used |= regBit(t) | regBit(dst)
}

// countFloatArguments returns the number of float arguments of floats.
func countFloatArguments(floats abi.FloatPassing) int {
	return bits.OnesCount64(uint64(floats & (abi.FloatResult32 - 1)))
}
