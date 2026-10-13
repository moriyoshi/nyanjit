package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Tail calls (§9) in the baseline tier. A TermTailCall block places the
// target and arguments where a C call takes them, then jumps to a tail-call
// epilogue: finish emits one per 386 argument-word count after the
// function's epilogue. It restores the saved registers as the epilogue does
// and ends with pkTailCall, which encodes machine.OpTailCall.

// bTailEpilogue is a tail-call epilogue: its label, and the 386 argument
// words its pkTailCall copies to the incoming ones (zero elsewhere).
type bTailEpilogue struct {
	label int32
	words uint8
}

// tailCallTerminator records block blk's TermTailCall step: its target and
// arguments, which value checks, are the block's argument list.
func (e *baselineEngine) tailCallTerminator(blk *bBlock, step *ir.ControlStep, value func(int, string) (uint32, error)) error {
	if len(step.Args) == 0 {
		return baselineInvalid(-1, 0, "tail call without a target")
	}
	blk.argStart = uint32(len(e.args))
	for _, x := range step.Args {
		v, err := value(x, "tail call operand")
		if err != nil {
			return err
		}
		e.args = append(e.args, v)
	}
	blk.nArgs = uint32(len(step.Args))
	incoming := 0
	for _, p := range e.params {
		incoming += i386ArgumentWords(e.valueBits(p))
	}
	var widths [24]uint8
	n := 0
	for _, v := range e.args[blk.argStart+1:] {
		if n < len(widths) {
			widths[n] = e.valueBits(v)
		}
		n++
	}
	if n > len(widths) {
		return &LimitError{Kind: LimitArguments, Needed: n, Limit: abi.MaxArgumentsFor(e.target.architecture), Arch: e.target.architecture, Detail: "tail call arguments"}
	}
	if err := checkTailCall(e.f, e.target.architecture, widths[:n], incoming); err != nil {
		return err
	}
	e.hasCall, e.hasFunctionCall = true, true
	e.memRead, e.memWrite = true, true
	// A tail call returns the callee's scalar result.
	e.returns++
	return nil
}

// tailLabel returns the label of the tail-call epilogue for words.
func (e *baselineEngine) tailLabel(words uint8) int32 {
	a := &e.alloc
	for _, t := range a.tails {
		if t.words == words {
			return t.label
		}
	}
	label := e.newLabel()
	a.tails = append(a.tails, bTailEpilogue{label: label, words: words})
	return label
}

// tailCall lowers block b's tail call.
func (e *baselineEngine) tailCall(b int32) error {
	a := &e.alloc
	blk := &e.blocks[b]
	if e.options.Stack != abi.StackPlatform {
		return ErrStackRequired
	}
	r := bRec{op: ir.OpCallC, x: blk.argStart, n: uint16(blk.nArgs)}
	effect, err := e.callEffect(&r)
	if err != nil {
		return err
	}
	// The callee's clobbers are this function's, for its report.
	for _, list := range [2][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters} {
		for _, name := range list {
			if reg, ok := e.arch.register(name); ok {
				a.used |= regBit(reg)
			}
		}
	}
	args := e.args[blk.argStart : blk.argStart+blk.nArgs]
	words := uint8(0)
	if _, ok := e.arch.(baselineStackArch); ok {
		words = i386TailArguments(e, args)
	} else {
		var dsts [24]int8
		if len(effect.ArgumentRegisters)+1 > len(dsts) || len(effect.ArgumentRegisters)+1 != len(args) {
			return ErrBackendLimit
		}
		dsts[0] = e.m.callTarget
		for k, name := range effect.ArgumentRegisters {
			reg, ok := e.arch.register(name)
			if !ok {
				return ErrBackendLimit
			}
			dsts[k+1] = reg
		}
		for k, v := range args {
			e.addMove(int16(dsts[k]), v)
			a.used |= regBit(dsts[k])
		}
		e.resolveMoves()
		if fixer, ok := e.arch.(baselineCallFixer); ok {
			fixer.fixCallArguments(e, &r, args, dsts[:len(args)])
		}
	}
	a.hasCall, a.flags = true, true
	e.put(bPhys{k: pkJump, imm: uint64(e.tailLabel(words))})
	return nil
}

// i386TailArguments stores a 386 tail call's arguments to the outgoing area,
// as a call does, and loads the target into EAX. It returns the argument
// words, which the tail-call epilogue copies to the incoming ones.
func i386TailArguments(e *baselineEngine, args []uint32) uint8 {
	a := &e.alloc
	off := uint64(0)
	for _, v := range args[1:] {
		wide := e.valueBits(v) == 64
		s := &a.vs[v]
		w := uint8(32)
		if wide {
			w = 64
		}
		switch {
		case s.reg >= 0:
			e.put(bPhys{k: pkOp, sub: i386ArgStore, w: w, a: s.reg, imm: off})
		case e.isConst(v) && e.recs[v].fl&rfSymbol == 0:
			c := e.recs[v].imm
			e.put(bPhys{k: pkConst, a: x86EAX, imm: uint64(uint32(c))})
			e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: off})
			if wide {
				e.put(bPhys{k: pkConst, a: x86EAX, imm: c >> 32})
				e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: off + 4})
			}
		default:
			t := x86EAX
			if wide {
				t = x86X0
			}
			e.load(t, v)
			e.put(bPhys{k: pkOp, sub: i386ArgStore, w: w, a: t, imm: off})
		}
		off += 4 * uint64(i386Words(e, v))
	}
	e.load(x86EAX, args[0])
	a.used |= i386Temps | regBit(x86X0)
	return uint8(off / 4)
}

// baselineTailCall encodes pkTailCall through a target encoder: leave a frame
// of frame bytes, copying words 386 argument words first, and jump.
func baselineTailCall(a any, frame uint64, words uint8) error {
	encoder, ok := a.(interface {
		TailCall(machine.Instruction) error
	})
	if !ok {
		return fmt.Errorf("baseline tier: tail call: %w", ErrUnsupported)
	}
	return encoder.TailCall(machine.Instruction{Op: machine.OpTailCall, Imm: frame, Disp: uint64(words)})
}

// tailEpilogue reports whether label begins a tail-call epilogue, which, like
// the epilogue, takes no speculation barrier: only exits reach it.
func (e *baselineEngine) tailEpilogue(label uint64) bool {
	for _, t := range e.alloc.tails {
		if uint64(t.label) == label {
			return true
		}
	}
	return false
}
