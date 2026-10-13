package compile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// The baseline engine is architecture-independent except for this file's
// interface. Registers use the engine's numbering: 0-31 are general registers
// and 32-63 floating-point registers, so one uint64 mask covers both banks.

const (
	bankGP uint8 = 0
	bankFP uint8 = 1
	fpBase       = 32
	noReg  int8  = -1
)

func regBank(r int8) uint8 {
	if r >= fpBase {
		return bankFP
	}
	return bankGP
}

func regBit(r int8) uint64 { return 1 << (uint8(r) & 63) }

// baselineModel is a target's register model for one Func.
type baselineModel struct {
	// allocatable registers, both banks.
	allocatable uint64
	// calleeSaved are the allocatable registers a C callee preserves, in the
	// order Vars are pinned to them (general registers first).
	calleeSavedGP, calleeSavedFP []int8
	// params are the entry argument registers in order; results the
	// result registers (low, high).
	params  []int8
	results [2]int8
	// scratch registers the engine never allocates: transit for
	// memory-to-memory moves, cycle breaking, and the floating-point
	// scratch.
	transit, cycle, scratchFP int8
	// callTarget receives an outbound call's address.
	callTarget int8
	// callPush is the stack an outbound call pushes (amd64's return
	// address).
	callPush int
	// saved is the mask of calleeSavedGP and calleeSavedFP.
	saved uint64
	// pinAvoid are allocatable caller-saved registers that never hold a
	// pinned Var, such as those fixed-register operations vacate.
	pinAvoid uint64
	// goHomes is the number of Go argument home words below spill slots.
	goHomes int
	// spillLimit bounds frame or workspace words for spills and homes.
	spillLimit int
	// banks, when set, adjusts the banks of floating-point record r: its
	// operand banks in r.fpu and its result bank in rfDefFP. A target sets
	// it when its FP registers hold some values in a format other than
	// their bits, so those values live in general registers.
	banks func(e *baselineEngine, r *bRec)
	// wide keeps every 64-bit value in the floating-point bank, on a target
	// whose general registers hold 32 bits (386: XMM registers).
	wide bool
}

// baselineArch is the architecture-specific part of the baseline engine:
// the register model, per-operation lowering with directly encoded sizes,
// and the encoding of the engine's physical records, including branches with
// their fixups and the frame.
type baselineArch interface {
	// model returns the register model for options.
	model(options compileOptions) baselineModel
	// lower appends the physical records for record r, whose operands are
	// in src (or folded immediates) and whose result goes to dst.
	lower(e *baselineEngine, r *bRec, dst int8, src [3]int8) error
	// foldable reports whether operand k of r may be the constant imm,
	// folded into the instruction instead of materialized.
	foldable(r *bRec, k int, imm uint64) bool
	// size returns the encoded size of p in bytes.
	size(e *baselineEngine, p *bPhys) int
	// longBranch reports whether conditional branch p at pc must use its
	// long form to reach target.
	longBranch(p *bPhys, pc, target int) bool
	// frame returns the frame size in bytes for slots spill words, or
	// an error when the frame exceeds the Func's stack budget.
	frame(e *baselineEngine, slots int, hasCall bool) (int, error)
	// encoder returns a fresh or reset encoder for native bytes or text.
	encoder(e *baselineEngine, text bool) baselineEncoder
	// encode emits ps from pc; labels maps label IDs to PCs. It returns the
	// PC after the last record.
	encode(e *baselineEngine, enc baselineEncoder, ps []bPhys, pc int, labels []int32) (int, error)
	// registerName names engine register r for reports.
	registerName(r int8) string
	// register maps an ABI register name to an engine register.
	register(name string) (int8, bool)
}

// baselineRelaxer is implemented by an architecture whose unconditional
// jumps or long conditional branches have more than one form. relax may
// grow p, a pkJump or pkJumpIfLong at pc, to reach target; it updates p.sz
// and reports whether it changed p. Forms only grow, so layout converges.
type baselineRelaxer interface {
	relax(e *baselineEngine, p *bPhys, pc, target int) bool
}

// baselineCallFixer is implemented by an architecture that adjusts an
// outbound call's arguments once they are in their registers dsts (dsts[0]
// is the target or system call number), just before the call.
type baselineCallFixer interface {
	fixCallArguments(e *baselineEngine, r *bRec, args []uint32, dsts []int8)
}

// baselineReporter is implemented by an architecture whose report differs
// from the one the engine builds; adjustReport corrects r in place. Without
// it, register 8 is the workspace pointer that a StackNoUse report omits.
type baselineReporter interface {
	adjustReport(u *baselineUnit, r *abi.EmissionReport)
}

// baselineStackArch is implemented by an architecture that passes entry
// arguments, results and outbound call arguments in memory (386). The engine
// then leaves those three steps to it.
type baselineStackArch interface {
	// enterParams gives the entry block's live parameters their locations.
	enterParams(e *baselineEngine)
	// returnValue moves block blk's result to the result location.
	returnValue(e *baselineEngine, blk *bBlock) error
	// call lowers call record r, whose value is i.
	call(e *baselineEngine, i uint32, r *bRec) error
}

// baselineLiteralArch is implemented by an architecture that loads
// floating-point constants from a literal pool after the code instead of
// building them in a general register. The pool holds 64-bit entries from
// a 16-byte-aligned base.
type baselineLiteralArch interface {
	// literalsLong reports whether loads must use their long form in code
	// of at most size bytes, pool included.
	literalsLong(size int) bool
	// encodePool emits the pool's padding from pc to base, then its
	// entries.
	encodePool(e *baselineEngine, enc baselineEncoder, pc, base int, pool []uint64) error
}

// baselineFloatBrancher is implemented by an architecture whose
// floating-point compare leaves flags that decide the comparison. For
// OpFCompare record r, it returns the integer branch condition that holds
// after r's code exactly when r's result is 1. between are the records
// emitted since r, which must not change the flags.
type baselineFloatBrancher interface {
	floatBranch(e *baselineEngine, r *bRec, between []bPhys) (op.BranchCondition, bool)
}

// baselineEncoder is the output of an encoding pass: bytes or a listing.
type baselineEncoder interface {
	AssembledSize() int
	Err() error
}

func baselineArchFor(architecture string) baselineArch {
	switch architecture {
	case "arm64":
		return arm64BaselineArch{}
	case "riscv64":
		return riscv64BaselineArch{}
	case "ppc64le":
		return ppc64BaselineArch{}
	case "loong64":
		return loong64BaselineArch{}
	case "amd64":
		return amd64BaselineArch{}
	case "386":
		return i386BaselineArch{}
	}
	return nil
}

// baselineGoHomes is the number of Go argument home words below the spill
// slots of a Go-ABI entry with a platform stack, as the optimized tier
// reserves them.
func baselineGoHomes(options compileOptions) int {
	if options.Stack == abi.StackPlatform && options.ABI == abi.Go {
		return 9
	}
	return 0
}
