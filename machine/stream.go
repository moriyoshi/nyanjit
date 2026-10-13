package machine

import "github.com/moriyoshi/nyanjit/ir"

// FlagFact describes the value and operands whose arithmetic established a
// surviving CPU flag. A fact is valid only until another instruction changes
// that flag or the allocator invalidates the correspondence.
type FlagFact struct {
	Valid, NoBorrow, ImmediateRight bool
	Value, Left, Right              int
	Operation                       ir.Opcode
	// Width is the logical arithmetic width; zero means unspecified.
	Width uint8
	// Immediate contains the width-normalized right operand for an immediate.
	Immediate uint64
	// Condition identifies the target's branch when this flag is set.
	// A target can supply a mapping independently of its compare conditions.
	Condition      ir.BranchCondition
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
type Stream struct {
	instructions    []Instruction
	pc              int
	flagsClobbered  bool
	flags           FlagState
	previousFlags   FlagState
	last            Instruction
	flagFacts       func(Instruction) FlagState
	pending         FlagMask
	size            func(Instruction) int
	flagEffect      func(Instruction) bool
	binarySetsFlags bool
	decSetsCarry    bool
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
}

func NewStream(options StreamOptions) *Stream {
	return &Stream{size: options.Size, flagEffect: options.FlagEffect, flagFacts: options.FlagFacts,
		binarySetsFlags: options.BinarySetsFlags, decSetsCarry: options.DecSetsCarry}
}

// Instructions returns the current sequence. Callers must treat it as
// read-only; Replace is the supported way to install a rewritten sequence.
func (s *Stream) Instructions() []Instruction       { return s.instructions }
func (s *Stream) PC() int                           { return s.pc }
func (s *Stream) Size(ins Instruction) int          { return s.size(ins) }
func (s *Stream) ChangesFlags(ins Instruction) bool { return s.flagEffect(ins) }

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

func (s *Stream) FlagsClobbered() bool { return s.flagsClobbered }
func (s *Stream) Flags() FlagState {
	flags := s.flags
	flags.Sign.Aliases = append([]int(nil), flags.Sign.Aliases...)
	flags.Zero.Aliases = append([]int(nil), flags.Zero.Aliases...)
	flags.Carry.Aliases = append([]int(nil), flags.Carry.Aliases...)
	return flags
}

// Append records an instruction and advances PC using the target's encoded
// size. Facts remain unbound until BindFlagValue identifies the source value.
func (s *Stream) Append(ins Instruction) {
	s.pending = 0
	s.previousFlags, s.last = s.flags, ins
	changesFlags := s.flagEffect(ins)
	if s.flagFacts != nil {
		effect := s.flagFacts(ins)
		s.flags.Modified = effect.Modified
		for _, entry := range []struct {
			mask FlagMask
			dst  *FlagFact
			fact FlagFact
		}{{FlagSign, &s.flags.Sign, effect.Sign}, {FlagZero, &s.flags.Zero, effect.Zero}, {FlagCarry, &s.flags.Carry, effect.Carry}} {
			if effect.Modified&entry.mask != 0 {
				*entry.dst = entry.fact
				if entry.fact.Valid {
					s.pending |= entry.mask
				}
			}
		}
		changesFlags = changesFlags || effect.Modified != 0
	} else if changesFlags {
		switch ins.Op {
		case OpBinaryCarry, OpWordCarry:
			// The high word's flags do not describe the whole paired value.
			s.flags = FlagState{Modified: FlagSign | FlagZero | FlagCarry}
		case OpBinary, OpBinaryImmediate:
			if s.binarySetsFlags && ins.Arithmetic != ir.OpMul {
				s.flags = FlagState{Sign: FlagFact{Valid: true}, Zero: FlagFact{Valid: true}, Modified: FlagSign | FlagZero | FlagCarry}
				s.pending = FlagSign | FlagZero
				if ins.Arithmetic == ir.OpAdd || ins.Arithmetic == ir.OpSub {
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
	s.pc += s.size(ins)
	s.flagsClobbered = s.flagsClobbered || changesFlags
}

func (s *Stream) BindFlagValue(value, left, right int, operation ir.Opcode, immediateRight bool) {
	bind := func(fact *FlagFact) {
		fact.Value, fact.Left, fact.Right = value, left, right
		fact.Operation, fact.ImmediateRight = operation, immediateRight
		fact.Aliases = nil
		if immediateRight {
			fact.Immediate = s.last.Imm
			if s.last.Op == OpInc || s.last.Op == OpDec {
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
		previous := s.previousFlags.Zero
		if previous.MatchesValue(left) && (operation == ir.OpSignExtend && previous.Width != 0 && previous.Width <= uint8(s.last.Imm) ||
			operation == ir.OpCastUnsigned && previous.Width == s.flags.Zero.Width) {
			s.flags.Zero.Aliases = append([]int{previous.Value}, previous.Aliases...)
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
		s.flags.Carry.Aliases = append([]int{source}, s.flags.Carry.Aliases...)
		s.flags.Carry.Value = value
	}
}

func (s *Stream) ForgetFlags() {
	s.flags = FlagState{Modified: s.flags.Modified}
	s.pending = 0
}

// Replace installs a rewritten instruction stream and recomputes positions.
// Flag facts are invalidated because rewriting can change their provenance.
func (s *Stream) Replace(instructions []Instruction) {
	s.instructions = nil
	s.pc = 0
	s.flagsClobbered = false
	s.flags = FlagState{}
	s.pending = 0
	for _, ins := range instructions {
		s.Append(ins)
	}
	s.ForgetFlags()
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
		fact.Aliases = append([]int{fact.Value}, fact.Aliases...)
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
		f.Aliases = append([]int{f.Value}, f.Aliases...)
		f.Value = value
		// Keep the producer width: an old narrow alias must not acquire
		// the sign predicate of an unsigned widening to resultWidth.
	}
	alias(&s.flags.Sign, s.flags.Sign.Width == sourceWidth)
	alias(&s.flags.Zero, s.flags.Zero.Width != 0 && s.flags.Zero.Width <= sourceWidth)
}
