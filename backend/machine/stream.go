package machine

import "github.com/moriyoshi/nyanjit/backend/op"

// FlagFact describes the value and operands whose arithmetic established a
// surviving CPU flag. A fact is valid only until another instruction changes
// that flag or the allocator invalidates the correspondence.
type FlagFact struct {
	Valid, NoBorrow, ImmediateRight bool
	Value, Left, Right              int
	Operation                       op.Opcode
	// Width is the logical arithmetic width; zero means unspecified.
	Width uint8
	// Immediate contains the width-normalized right operand for an immediate.
	Immediate uint64
	// Condition identifies the target's branch when this flag is set.
	// A target can supply a mapping independently of its compare conditions.
	Condition      op.BranchCondition
	ConditionValid bool
	// Aliases names other values proven to have this same flag predicate.
	Aliases []int
}

// MatchesValue recognizes a primary value or a proven flag-equivalent alias.
func (fact FlagFact) MatchesValue(value int) bool {
	if !fact.Valid {
		return false
	}
	if fact.Value == value {
		return true
	}
	for _, alias := range fact.Aliases {
		if alias == value {
			return true
		}
	}
	return false
}

// FlagMask records which flags the last flag-writing instruction modified.
type FlagMask uint8

const (
	FlagSign FlagMask = 1 << iota
	FlagZero
	FlagCarry
)

// FlagState is the arithmetic flag information available to a following
// branch. Modified includes flags that were written but have no useful fact.
type FlagState struct {
	Sign, Zero, Carry FlagFact
	Modified          FlagMask
}

// Stream owns physical instruction order, byte positions, and the flag facts
// produced by emission. Assemblers consume its instructions after lowering.
//
// The stream keeps each fact's Aliases oldest first, the reverse of the order
// Flags reports, so following a chain of copies appends to the list. Each
// list's array belongs to one fact: previousZero shares the current Zero
// fact's array only until the next flag write replaces that fact.
type Stream struct {
	pooled                          bool
	borrow                          layoutBorrowState
	instructionHistory, sizeHistory BufferHistory
	markHistory                     BufferHistory
	layout                          layoutBuffers
	instructions                    []Instruction
	sizes                           []int
	marks                           []uint8 // ModifiedFlags of each instruction, plus flagWriter
	pc                              int
	flagWriters                     int // instructions that change flags
	flags                           FlagState
	previousZero                    FlagFact
	lastOp                          Opcode
	lastImm                         uint64
	flagFacts                       func(Instruction) FlagState
	exactFlagFacts                  bool
	pending                         FlagMask
	size                            func(Instruction) int
	flagEffect                      func(Instruction) bool
	binarySetsFlags                 bool
	decSetsCarry                    bool

	// aliasArena backs fact alias lists in chunks; each list owns its
	// region. Lists never leave the stream uncopied, and the arena is dropped,
	// not reused, when the stream's work is released.
	aliasArena []int
}

// StreamOptions supplies the architecture's size and flag behavior.
type StreamOptions struct {
	Size       func(Instruction) int
	FlagEffect func(Instruction) bool
	// FlagFacts supplies exact per-flag effects and unbound arithmetic facts.
	// Flags outside Modified survive. Nil uses the legacy x86/arm64 behavior.
	FlagFacts       func(Instruction) FlagState
	BinarySetsFlags bool
	DecSetsCarry    bool
	// ExactFlagFacts makes FlagFacts authoritative and avoids calling FlagEffect twice.
	ExactFlagFacts bool
}

// NewStream returns an empty stream that sizes instructions and tracks flags
// as options describe. Workspace streams reuse storage instead.
func NewStream(options StreamOptions) *Stream {
	return &Stream{exactFlagFacts: options.ExactFlagFacts, size: options.Size, flagEffect: options.FlagEffect, flagFacts: options.FlagFacts,
		binarySetsFlags: options.BinarySetsFlags, decSetsCarry: options.DecSetsCarry}
}

// Instructions returns the current sequence. Callers must treat it as
// read-only; Replace is the supported way to install a rewritten sequence.
// The slice is borrowed until the next stream mutation or layout pass, or its
// Workspace Reset, Rewind, Trim or Release. Copy it to retain it.
func (s *Stream) Instructions() []Instruction { s.borrow.mark(); return s.instructions }

// Reserve preallocates instruction and size storage before emission.
func (s *Stream) Reserve(count int) {
	s.instructionHistory.Observe(len(s.instructions) + count)
	s.sizeHistory.Observe(len(s.sizes) + count)
	if cap(s.instructions)-len(s.instructions) < count {
		v := make([]Instruction, len(s.instructions), len(s.instructions)+count)
		copy(v, s.instructions)
		s.instructions = v
	}
	if cap(s.sizes)-len(s.sizes) < count {
		v := make([]int, len(s.sizes), len(s.sizes)+count)
		copy(v, s.sizes)
		s.sizes = v
	}
	s.markHistory.Observe(len(s.marks) + count)
	if cap(s.marks)-len(s.marks) < count {
		v := make([]uint8, len(s.marks), len(s.marks)+count)
		copy(v, s.marks)
		s.marks = v
	}
}

// SizeAt returns the cached size of an unchanged instruction.
func (s *Stream) SizeAt(index int) int { return s.sizes[index] }

// PC returns the byte position after the last instruction.
func (s *Stream) PC() int { return s.pc }

// Size returns the encoded size the target gives ins, without appending it.
// A negative size means the target cannot encode ins.
func (s *Stream) Size(ins Instruction) int { return s.size(ins) }

// ChangesFlags reports whether ins overwrites any arithmetic flag. A stream
// without flag metadata reports none.
func (s *Stream) ChangesFlags(ins Instruction) bool { return s.flagEffect != nil && s.flagEffect(ins) }

// ModifiedFlags reports the exact flags overwritten by an instruction.
// Targets without individual flag metadata conservatively overwrite all.
func (s *Stream) ModifiedFlags(ins Instruction) FlagMask {
	if s.flagFacts != nil {
		return s.flagFacts(ins).Modified
	}
	if s.ChangesFlags(ins) {
		return FlagSign | FlagZero | FlagCarry
	}
	return 0
}

// FlagsClobbered reports whether any instruction in the stream changes flags.
func (s *Stream) FlagsClobbered() bool { return s.flagWriters != 0 }

// Flags returns the flag facts after the last instruction. The result is a
// copy, including its alias lists.
func (s *Stream) Flags() FlagState {
	flags := s.flags
	flags.Sign.Aliases = reversedAliases(flags.Sign.Aliases)
	flags.Zero.Aliases = reversedAliases(flags.Zero.Aliases)
	flags.Carry.Aliases = reversedAliases(flags.Carry.Aliases)
	return flags
}

// appendAlias appends value to a fact's alias list, moving a full list into
// a larger region of the alias arena instead of a heap array of its own.
func (s *Stream) appendAlias(list []int, value int) []int {
	if len(list) < cap(list) {
		return append(list, value)
	}
	n := max(2, 2*len(list))
	if cap(s.aliasArena)-len(s.aliasArena) < n {
		s.aliasArena = make([]int, 0, max(n, min(2*cap(s.aliasArena), 4096), 16))
	}
	start := len(s.aliasArena)
	s.aliasArena = s.aliasArena[:start+n]
	return append(append(s.aliasArena[start:start:start+n], list...), value)
}

// reversedAliases copies a stream-ordered alias list into the newest-first
// order of a snapshot.
func reversedAliases(aliases []int) []int {
	if len(aliases) == 0 {
		return nil
	}
	out := make([]int, len(aliases))
	for i, alias := range aliases {
		out[len(aliases)-1-i] = alias
	}
	return out
}

// Append records an instruction and advances PC using the target's encoded
// size. Facts remain unbound until BindFlagValue identifies the source value.
func (s *Stream) Append(ins Instruction) {
	s.pending = 0
	s.previousZero, s.lastOp, s.lastImm = s.flags.Zero, ins.Op, ins.Imm
	changesFlags := false
	if (!s.exactFlagFacts || s.flagFacts == nil) && s.flagEffect != nil {
		changesFlags = s.flagEffect(ins)
	}
	if s.flagFacts != nil {
		effect := s.flagFacts(ins)
		s.flags.Modified = effect.Modified
		if effect.Modified&FlagSign != 0 {
			s.flags.Sign = effect.Sign
			if effect.Sign.Valid {
				s.pending |= FlagSign
			}
		}
		if effect.Modified&FlagZero != 0 {
			s.flags.Zero = effect.Zero
			if effect.Zero.Valid {
				s.pending |= FlagZero
			}
		}
		if effect.Modified&FlagCarry != 0 {
			s.flags.Carry = effect.Carry
			if effect.Carry.Valid {
				s.pending |= FlagCarry
			}
		}
		changesFlags = changesFlags || effect.Modified != 0
	} else if changesFlags {
		switch ins.Op {
		case OpBinaryCarry, OpWordCarry:
			// The high word's flags do not describe the whole paired value.
			s.flags = FlagState{Modified: FlagSign | FlagZero | FlagCarry}
		case OpBinary, OpBinaryImmediate:
			if s.binarySetsFlags && ins.Arithmetic != op.OpMul {
				s.flags = FlagState{Sign: FlagFact{Valid: true}, Zero: FlagFact{Valid: true}, Modified: FlagSign | FlagZero | FlagCarry}
				s.pending = FlagSign | FlagZero
				if ins.Arithmetic == op.OpAdd || ins.Arithmetic == op.OpSub {
					s.flags.Carry = FlagFact{Valid: true}
					s.pending |= FlagCarry
				}
			} else {
				s.flags = FlagState{Modified: FlagSign | FlagZero | FlagCarry}
			}
		case OpInc, OpDec:
			// x86 INC/DEC leave carry untouched; arm64 SUBS updates it.
			s.flags.Sign = FlagFact{Valid: true}
			s.flags.Zero = FlagFact{Valid: true}
			s.flags.Modified = FlagSign | FlagZero
			s.pending = FlagSign | FlagZero
			if ins.Op == OpDec && s.decSetsCarry {
				s.flags.Carry = FlagFact{Valid: true, NoBorrow: true}
				s.flags.Modified |= FlagCarry
				s.pending |= FlagCarry
			}
		default:
			s.flags = FlagState{Modified: FlagSign | FlagZero | FlagCarry}
		}
	}
	s.instructions = append(s.instructions, ins)
	size := s.size(ins)
	s.sizes = append(s.sizes, size)
	s.pc += size
	var mark uint8
	if s.flagFacts != nil {
		mark = uint8(s.flags.Modified)
	} else if changesFlags {
		mark = uint8(FlagSign | FlagZero | FlagCarry)
	}
	if changesFlags {
		mark |= flagWriter
		s.flagWriters++
	}
	s.marks = append(s.marks, mark)
}

// BindFlagValue records that the flags the last instruction set describe
// value, computed by operation from left and right (an immediate when
// immediateRight). Branches that test value can then reuse the flags.
func (s *Stream) BindFlagValue(value, left, right int, operation op.Opcode, immediateRight bool) {
	bind := func(fact *FlagFact) {
		fact.Value, fact.Left, fact.Right = value, left, right
		fact.Operation, fact.ImmediateRight = operation, immediateRight
		fact.Aliases = nil
		if immediateRight {
			fact.Immediate = s.lastImm
			if s.lastOp == OpInc || s.lastOp == OpDec {
				fact.Immediate = 1
			}
			if fact.Width != 0 && fact.Width < 64 {
				fact.Immediate &= ^uint64(0) >> (64 - fact.Width)
			}
		}
	}
	if s.pending&FlagSign != 0 {
		bind(&s.flags.Sign)
	}
	if s.pending&FlagZero != 0 {
		bind(&s.flags.Zero)
		// Sign extension keeps the zero predicate of its source. A narrowing
		// keeps it only when the producer already proved the narrower width.
		previous := s.previousZero
		if previous.MatchesValue(left) && (operation == op.OpSignExtend && previous.Width != 0 && previous.Width <= uint8(s.lastImm) ||
			operation == op.OpCastUnsigned && previous.Width == s.flags.Zero.Width) {
			// The replaced fact's array is previousZero's alone.
			s.flags.Zero.Aliases = s.appendAlias(previous.Aliases, previous.Value)
		}
	}
	if s.pending&FlagCarry != 0 {
		bind(&s.flags.Carry)
	}
	s.pending = 0
}

// AliasCarryValue follows a width-preserving truncation of an arithmetic
// result. XER/flags still describe the same unsigned carry or borrow.
func (s *Stream) AliasCarryValue(value, source int, width uint8) {
	if s.flagFacts != nil && s.flags.Carry.MatchesValue(source) && s.flags.Carry.Width == width {
		s.flags.Carry.Aliases = s.appendAlias(s.flags.Carry.Aliases, source)
		s.flags.Carry.Value = value
	}
}

// ForgetFlags drops every flag fact, keeping only which flags were modified.
// Call it when a rewrite or label makes the facts unreliable.
func (s *Stream) ForgetFlags() {
	s.flags = FlagState{Modified: s.flags.Modified}
	s.pending = 0
}

// Replace installs a rewritten instruction stream and recomputes positions.
// Flag facts are invalidated because rewriting can change their provenance.
// Replace copies instructions into stream-owned storage.
func (s *Stream) Replace(instructions []Instruction) {
	s.instructionHistory.Observe(len(s.instructions))
	s.sizeHistory.Observe(len(s.sizes))
	s.markHistory.Observe(len(s.marks))
	s.instructions = make([]Instruction, 0, len(instructions))
	s.sizes = make([]int, 0, len(instructions))
	s.marks = make([]uint8, 0, len(instructions))
	s.pc = 0
	s.flagWriters = 0
	s.flags = FlagState{}
	s.pending = 0
	for _, ins := range instructions {
		s.Append(ins)
	}
	s.ForgetFlags()
}

// ReplaceKeepingFacts takes ownership of a layout-only rewrite. The caller
// must not modify instructions afterwards. It preserves arithmetic provenance;
// use Replace when a rewrite changes which values established flags.
func (s *Stream) ReplaceKeepingFacts(instructions []Instruction) {
	s.replaceKeepingFacts(instructions, nil)
}

// replaceKeepingFacts commits a private layout rewrite. Non-nil positions must
// come from Size/SizeAt for these exact instructions; changed branch forms and
// operands are measured before committing. Public rewrites supply nil so every
// caller-provided instruction still passes the full target size validation.
func (s *Stream) replaceKeepingFacts(instructions []Instruction, positions []int) {
	s.instructionHistory.Observe(len(s.instructions))
	s.sizeHistory.Observe(len(s.sizes))
	s.markHistory.Observe(len(s.marks))
	s.instructions = instructions
	if cap(s.sizes) < len(instructions) {
		s.sizes = make([]int, len(instructions))
	} else {
		s.sizes = s.sizes[:len(instructions)]
	}
	if cap(s.marks) < len(instructions) {
		s.marks = make([]uint8, len(instructions))
	} else {
		s.marks = s.marks[:len(instructions)]
	}
	s.pc = 0
	s.flagWriters = 0
	for n, ins := range instructions {
		s.marks[n] = s.mark(ins)
		if s.marks[n]&flagWriter != 0 {
			s.flagWriters++
		}
		if positions == nil {
			s.sizes[n] = s.size(ins)
		} else {
			s.sizes[n] = positions[n+1] - positions[n]
		}
		s.pc += s.sizes[n]
	}
}

// ReplaceAt installs ins as the instruction at index and returns its size.
// It is ReplaceKeepingFacts with one instruction changed: only ins is
// measured, later PCs shift by the change in size, and flag facts are kept,
// so a caller whose rewrite changes which value established the flags calls
// ForgetFlags. FlagsClobbered follows the change. A slice from Instructions
// stays valid and shows ins. ReplaceAt panics when index is out of range.
func (s *Stream) ReplaceAt(index int, ins Instruction) int {
	if s.marks[index]&flagWriter != 0 {
		s.flagWriters--
	}
	mark := s.mark(ins)
	if mark&flagWriter != 0 {
		s.flagWriters++
	}
	size := s.size(ins)
	s.pc += size - s.sizes[index]
	s.sizes[index] = size
	s.marks[index] = mark
	s.instructions[index] = ins
	return size
}

// ModifiedFlagsAt reports the flags the instruction at index overwrites, as
// ModifiedFlags reports them, recorded when it was appended or replaced. It
// panics when index is out of range.
func (s *Stream) ModifiedFlagsAt(index int) FlagMask {
	return FlagMask(s.marks[index] &^ flagWriter)
}

// flagWriter marks an instruction that counts toward FlagsClobbered.
const flagWriter uint8 = 0x80

// mark returns ModifiedFlags of ins, with flagWriter when it counts toward
// FlagsClobbered, as Append decides them.
func (s *Stream) mark(ins Instruction) uint8 {
	writes := (!s.exactFlagFacts || s.flagFacts == nil) && s.flagEffect != nil && s.flagEffect(ins)
	var mark uint8
	if s.flagFacts != nil {
		modified := s.flagFacts(ins).Modified
		mark = uint8(modified)
		writes = writes || modified != 0
	} else if writes {
		mark = uint8(FlagSign | FlagZero | FlagCarry)
	}
	if writes {
		mark |= flagWriter
	}
	return mark
}

// AliasFlagValue follows an identity copy or widening zero extension. Width
// bounds are required: narrowing and changing the signed width need new facts.
func (s *Stream) AliasFlagValue(value, source int, width uint8) {
	if width == 0 || value == source {
		return
	}
	alias := func(fact *FlagFact, validWidth bool) {
		if !validWidth || !fact.MatchesValue(source) {
			return
		}
		fact.Aliases = s.appendAlias(fact.Aliases, fact.Value)
		fact.Value = value
	}
	alias(&s.flags.Zero, s.flags.Zero.Width != 0 && s.flags.Zero.Width <= width)
	alias(&s.flags.Sign, s.flags.Sign.Width == width)
	alias(&s.flags.Carry, s.flags.Carry.Width == width)
}

// AliasSignExtendedValue follows signed widening when the surviving flags
// already describe the source width. Signed widening preserves sign and zero,
// while unsigned carry/borrow continues to describe the original operation.
func (s *Stream) AliasSignExtendedValue(value, source int, sourceWidth, resultWidth uint8) {
	if sourceWidth == 0 || resultWidth < sourceWidth {
		return
	}
	alias := func(f *FlagFact, valid bool) {
		if !valid || !f.MatchesValue(source) {
			return
		}
		f.Aliases = s.appendAlias(f.Aliases, f.Value)
		f.Value = value
		// Keep the producer width: an old narrow alias must not acquire
		// the sign predicate of an unsigned widening to resultWidth.
	}
	alias(&s.flags.Sign, s.flags.Sign.Width == sourceWidth)
	alias(&s.flags.Zero, s.flags.Zero.Width != 0 && s.flags.Zero.Width <= sourceWidth)
}
