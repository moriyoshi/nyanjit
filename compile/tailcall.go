package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Tail calls (§9) in the optimized tier. A TermTailCall segment ends with an
// ordinary OpCallC as its result (controlSegment), so selection and both
// allocators place the target and arguments as for any C call. Its exit
// step then replaces the call: nothing after it can run, so the fragment is
// cut at the call, and the exit restores what the entry saved and ends with
// machine.OpTailCall, which leaves the frame and jumps to the callee.

// tailCallCut is the index of the call that ends a tail-call fragment, its
// last OpCallC, or len(fragment) when it has none.
func tailCallCut(fragment []physicalInsn) int {
	for n := len(fragment) - 1; n >= 0; n-- {
		if fragment[n].Op == machine.OpCallC {
			return n
		}
	}
	return len(fragment)
}

// tailCallWords is the number of 386 stack words that call op of segment
// ops passes: one per argument, two for a 64-bit one. Other targets pass
// tail call arguments in registers and take zero.
func tailCallWords(architecture string, ops []instruction, call int) int {
	if architecture != "386" || call < 0 || call >= len(ops) || len(ops[call].Args) == 0 {
		return 0
	}
	words := 0
	for _, id := range ops[call].Args[1:] {
		words++
		if instructionBits(ops[id]) > 32 {
			words++
		}
	}
	return words
}

// emitTailCallExit ends a tail-call exit: the entry's saves are restored in
// the order a return restores them, then OpTailCall leaves frame and jumps.
// words is tailCallWords' count.
func emitTailCallExit(stream *physicalEmitter, resources []locatedEntryResource, entrySaveBase, frame, words int) {
	for i := len(resources) - 1; i >= 0; i-- {
		save := resources[i]
		stream.append(physicalInsn{Op: save.Restore, A: save.Register, Slot: save.Slot})
	}
	for i := len(stream.entryFloatSaves) - 1; i >= 0; i-- {
		stream.append(physicalInsn{Op: pRestoreCFloat, A: stream.entryFloatSaves[i], Slot: entrySaveBase + len(stream.entrySaves) + i})
	}
	for i := len(stream.entrySaves) - 1; i >= 0; i-- {
		stream.append(physicalInsn{Op: pReload, A: stream.entrySaves[i], Slot: entrySaveBase + i})
	}
	stream.append(physicalInsn{Op: machine.OpTailCall, Imm: uint64(frame), Disp: uint64(words)})
}

// hasTailCall reports whether p's control ends a block with TermTailCall.
func hasTailCall(p *ir.Program) bool {
	if p == nil || p.Control == nil {
		return false
	}
	for _, step := range p.Control.Steps {
		if !step.IsLabel && step.Term == ir.TermTailCall {
			return true
		}
	}
	return false
}

// checkTailCall applies the rules of one tail call that depend on f and the
// target, for a call of args argument widths from an entry whose parameters
// take incoming 386 stack words: the C ABI in ContextForeign or
// ContextExternal, register arguments, and on 386 no more argument words
// than the entry's own, which the callee reuses.
func checkTailCall(f Func, arch string, args []uint8, incoming int) error {
	if f.ABI != abi.C {
		return fmt.Errorf("tail call: entry ABI %d is not the C ABI: %w", f.ABI, ErrUnsupported)
	}
	if f.Context != ContextForeign && f.Context != ContextExternal {
		return fmt.Errorf("tail call in call context %d: only ContextForeign and ContextExternal functions make tail calls: %w", f.Context, ErrUnsupported)
	}
	if limit := abi.MaxArgumentsFor(arch); len(args) > limit {
		return &LimitError{Kind: LimitArguments, Needed: len(args), Limit: limit, Arch: arch, Detail: "tail call arguments"}
	}
	if arch == "386" {
		words := 0
		for _, bits := range args {
			words += i386ArgumentWords(bits)
		}
		if words > incoming {
			return &LimitError{Kind: LimitArguments, Needed: words, Limit: incoming, Arch: arch, Detail: "386 tail call argument words, which reuse the entry's own"}
		}
	}
	return nil
}

// i386ArgumentWords is the number of 386 stack words a C argument of bits
// takes.
func i386ArgumentWords(bits uint8) int {
	if bits > 32 {
		return 2
	}
	return 1
}

// checkTailCalls applies checkTailCall to every tail call of p.
func checkTailCalls(p *ir.Program, f Func, arch string) error {
	if !hasTailCall(p) {
		return nil
	}
	incoming := 0
	for i := range p.Instructions {
		if p.Instructions[i].Op == ir.OpParam {
			incoming += i386ArgumentWords(instructionBits(p.Instructions[i]))
		}
	}
	var widths []uint8
	for _, step := range p.Control.Steps {
		if step.IsLabel || step.Term != ir.TermTailCall {
			continue
		}
		widths = widths[:0]
		for _, id := range step.Args[1:] {
			widths = append(widths, instructionBits(p.Instructions[id]))
		}
		if err := checkTailCall(f, arch, widths, incoming); err != nil {
			return err
		}
	}
	return nil
}
