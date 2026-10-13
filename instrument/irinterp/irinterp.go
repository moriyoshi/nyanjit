// Package irinterp is a reference interpreter for nyanjit IR.
//
// Run executes a sealed program directly from its instructions and labeled
// control, with the semantics the backends implement: integer values wrap at
// their width, floating-point operations are IEEE 754 binary32/binary64 with
// round-to-nearest-even, loads and stores access real memory at the addresses
// the program computes, and vectors and masks operate lane by lane.
//
// A value index always holds its most recent definition, so a use reads the
// value from whichever block defined it. Execution does not depend on values
// being local to their block.
//
// Some results are not fully specified by the IR. Backends choose NaN
// payloads, so a NaN produced by a floating-point operation is reported as
// Result.NaN rather than as particular bits; anything computed from such a
// payload (integer operations on it, comparisons or branches on it, memory
// that stores it) makes the whole result Result.Unspecified. Result.Matches
// compares a native result with these rules.
//
// The package is instrumentation: tests and tools may import it, production
// code must not.
package irinterp

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

var (
	// ErrUnsupported means the program uses something the interpreter cannot
	// execute: Go aggregate or vector entry parameters, a call or system call
	// without Options.Call, or an unbound symbol.
	ErrUnsupported = errors.New("irinterp: unsupported program")
	// ErrStepLimit means execution exceeded Options.MaxSteps.
	ErrStepLimit = errors.New("irinterp: step limit exceeded")
)

// DefaultMaxSteps bounds execution when Options.MaxSteps is zero.
const DefaultMaxSteps = 1 << 28

// Call describes one OpCallC, OpCallGo or OpSyscall instruction being
// executed.
type Call struct {
	Op    ir.Opcode // OpCallC, OpCallGo or OpSyscall
	Index int       // the instruction's value index
	// Target is the call target address, or the system call number.
	Target uint64
	Args   []uint64 // the remaining arguments, each at its value width
}

// Options configures Run. A nil *Options is the zero value.
type Options struct {
	// Call executes calls and system calls. The result is truncated to the
	// instruction's width. Without it, a program that reaches a call fails
	// with ErrUnsupported.
	Call func(Call) (uint64, error)
	// Symbols binds OpConst symbols, as compile.Func.Constants or
	// execution.Template.Bind would. ir.MathPi4Symbol defaults to a table
	// owned by this package.
	Symbols map[string]uint64
	// Contract fuses an FAdd marked Contract with a single-use FMul operand
	// (A first, then B) into one FMA, as targets with FMA do. Without it the
	// product is rounded separately, as targets without FMA do.
	Contract bool
	// MaxSteps bounds the number of executed instructions and control
	// edges; zero means DefaultMaxSteps.
	MaxSteps int
}

// Result is a program's return value.
type Result struct {
	// Lo holds the result, zero-extended from its width. For a 128-bit
	// result (ResultParts), Lo and Hi are its low and high 64-bit words.
	Lo, Hi uint64
	Bits   int // 8, 16, 32 or 64, or 128 for a two-part result
	// NaN reports a floating-point NaN of width Bits whose payload the
	// target chooses. Lo holds the default quiet NaN.
	NaN bool
	// Unspecified reports a result that depends on a NaN payload. Any
	// value matches it.
	Unspecified bool
}

// Matches reports whether a native result agrees with r. lo and hi are the
// raw result words; bits above r.Bits are ignored.
func (r Result) Matches(lo, hi uint64) bool {
	switch {
	case r.Unspecified:
		return true
	case r.Bits == 128:
		return lo == r.Lo && hi == r.Hi
	case r.NaN && r.Bits == 32:
		return math.IsNaN(float64(math.Float32frombits(uint32(lo))))
	case r.NaN:
		return math.IsNaN(math.Float64frombits(lo))
	}
	return lo&widthMask(uint8(r.Bits)) == r.Lo
}

// String formats the result for test failures.
func (r Result) String() string {
	switch {
	case r.Unspecified:
		return "unspecified"
	case r.NaN:
		return fmt.Sprintf("NaN/%d", r.Bits)
	case r.Bits == 128:
		return fmt.Sprintf("%#x:%#x", r.Hi, r.Lo)
	}
	return fmt.Sprintf("%#x/%d", r.Lo, r.Bits)
}

// taint records how much of a value the IR specifies.
type taint uint8

const (
	exact      taint = iota
	nanPayload       // a NaN whose payload the target chooses
	unknown          // derived from a NaN payload
)

type value struct {
	x     uint64
	lanes []uint64 // vector and mask lanes; nil for scalars
	t     taint
}

type machine struct {
	program  *ir.SealedProgram
	ops      []ir.Instruction
	vals     []value
	vars     [][2]value
	opts     Options
	steps    int
	incoming []value
	params   []uint64
	// memory is set once a NaN-derived value reaches memory: loads after
	// that are unspecified.
	memory bool
	// control is set once a branch depends on a NaN-derived value.
	control   bool
	immutable map[int][]int // scope ID -> immutable variables it captures
	uses      []int
	paramAt   []int // OpParam value index -> argument index
	result    Result
}

// Run executes program with the given entry arguments, one per OpParam in
// instruction order, each truncated to its parameter width. program is sealed
// first unless it already is. An invalid program returns the validation error.
func Run(program ir.Input, args []uint64, opts *Options) (Result, error) {
	sealed, ok := program.(*ir.SealedProgram)
	if !ok {
		if program == nil {
			return Result{}, ir.ErrInvalidProgram
		}
		var err error
		if sealed, err = ir.SealOwned(program.Clone()); err != nil {
			return Result{}, err
		}
	}
	m := &machine{program: sealed, ops: sealed.Instructions()}
	if opts != nil {
		m.opts = *opts
	}
	if m.opts.MaxSteps == 0 {
		m.opts.MaxSteps = DefaultMaxSteps
	}
	if len(sealed.ParameterTypes()) != 0 {
		for _, typ := range sealed.ParameterTypes() {
			if typ != nil {
				return Result{}, fmt.Errorf("Go aggregate parameter %s: %w", typ, ErrUnsupported)
			}
		}
	}
	if len(args) != sealed.Arity() {
		return Result{}, fmt.Errorf("%d arguments for arity %d: %w", len(args), sealed.Arity(), ErrUnsupported)
	}
	m.params = args
	m.paramAt = make([]int, len(m.ops))
	for i, n := 0, 0; i < len(m.ops); i++ {
		if m.ops[i].Op == ir.OpParam {
			m.paramAt[i] = n
			n++
		}
	}
	m.vals = make([]value, len(m.ops))
	m.vars = make([][2]value, len(sealed.Variables()))
	for id, v := range sealed.Variables() {
		if v.Immutable {
			if m.immutable == nil {
				m.immutable = make(map[int][]int)
			}
			m.immutable[v.Region] = append(m.immutable[v.Region], id)
		}
	}
	if m.opts.Contract {
		m.countUses()
	}
	if err := m.run(); err != nil {
		return Result{}, err
	}
	return m.result, nil
}

func (m *machine) countUses() {
	m.uses = make([]int, len(m.ops))
	var buffer [4]int
	for _, ins := range m.ops {
		for _, id := range ins.InputsInto(buffer[:0]) {
			m.uses[id]++
		}
	}
	if c := m.program.Control(); c != nil {
		for _, step := range c.Steps {
			for _, list := range [][]int{step.Values, step.Args, step.OtherArgs, step.ResultParts} {
				for _, id := range list {
					m.uses[id]++
				}
			}
			if !step.IsLabel && step.Term != ir.TermJump && len(step.ResultParts) == 0 {
				m.uses[step.Result]++
			}
		}
	} else {
		m.uses[m.program.Result()]++
		for _, id := range m.program.ResultParts() {
			m.uses[id]++
		}
	}
}

func widthMask(bits uint8) uint64 {
	if bits == 0 || bits >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<bits - 1
}

func width(ins *ir.Instruction) uint8 {
	if ins.Bits == 0 {
		return 64
	}
	return ins.Bits
}

func signExtend(x uint64, bits uint8) uint64 {
	if bits >= 64 {
		return x
	}
	shift := 64 - bits
	return uint64(int64(x<<shift) >> shift)
}

// merge returns the taint of a result computed from inputs: anything other
// than a floating-point operation turns a NaN payload into unknown bits.
func merge(inputs ...value) taint {
	t := exact
	for _, v := range inputs {
		if v.t != exact {
			t = unknown
		}
	}
	return t
}

func (m *machine) run() error {
	control := m.program.Control()
	if control == nil {
		if err := m.execute(0, len(m.ops)); err != nil {
			return err
		}
		m.finish(m.program.Result(), m.program.ResultParts())
		return nil
	}
	// Each label leads to the first segment after it.
	segment := make([]int, len(control.Labels))
	for i, step := range control.Steps {
		if !step.IsLabel {
			continue
		}
		for j := i + 1; j < len(control.Steps); j++ {
			if !control.Steps[j].IsLabel {
				segment[step.Label] = j
				break
			}
		}
	}
	current := segment[control.Steps[0].Label]
	for {
		step := &control.Steps[current]
		if err := m.execute(step.Start, step.End); err != nil {
			return err
		}
		to, args := step.To, step.Args
		switch step.Term {
		case ir.TermReturn:
			m.finish(step.Result, step.ResultParts)
			return nil
		case ir.TermBranch:
			if !m.predicate(step.Predicate) {
				to, args = step.Other, step.OtherArgs
			}
		}
		// Count each edge, so loops through empty blocks stop too.
		m.steps++
		if m.steps > m.opts.MaxSteps {
			return ErrStepLimit
		}
		incoming := make([]value, len(args))
		for i, id := range args {
			incoming[i] = m.vals[id]
		}
		m.incoming = incoming
		current = segment[to]
	}
}

func (m *machine) finish(result int, parts []int) {
	r := &m.result
	if len(parts) == 2 {
		lo, hi := m.vals[parts[0]], m.vals[parts[1]]
		r.Lo, r.Hi, r.Bits = lo.x, hi.x, 128
		r.Unspecified = lo.t != exact || hi.t != exact
	} else {
		v := m.vals[result]
		r.Lo, r.Bits = v.x, int(m.ops[result].ValueType().LaneBits)
		r.NaN = v.t == nanPayload
		r.Unspecified = v.t == unknown
	}
	if m.control {
		r.Unspecified = true
	}
}

// predicate evaluates a branch condition with short-circuit And and Or.
func (m *machine) predicate(node *ir.PredicateExpr) bool {
	switch node.Kind {
	case ir.PredicateAnd:
		return m.predicate(node.Left) && m.predicate(node.Right)
	case ir.PredicateOr:
		return m.predicate(node.Left) || m.predicate(node.Right)
	case ir.PredicateNot:
		return !m.predicate(node.Left)
	}
	left := m.vals[node.Value]
	right := uint64(node.Immediate)
	if node.WithValue {
		other := m.vals[node.Other]
		right = other.x
		if other.t != exact {
			m.control = true
		}
	}
	if left.t != exact {
		m.control = true
	}
	return compare(node.Condition, left.x, right, 64)
}

// compare applies condition to a and b at the given width. Signed conditions
// sign-extend from that width.
func compare(condition ir.BranchCondition, a, b uint64, bits uint8) bool {
	sa, sb := int64(signExtend(a, bits)), int64(signExtend(b, bits))
	a, b = a&widthMask(bits), b&widthMask(bits)
	switch condition {
	case ir.BranchEqual:
		return a == b
	case ir.BranchNotEqual:
		return a != b
	case ir.BranchLess:
		return sa < sb
	case ir.BranchGreaterEqual:
		return sa >= sb
	case ir.BranchLessEqual:
		return sa <= sb
	case ir.BranchGreater:
		return sa > sb
	case ir.BranchBelow:
		return a < b
	case ir.BranchAboveEqual:
		return a >= b
	case ir.BranchBelowEqual:
		return a <= b
	default: // BranchAbove
		return a > b
	}
}

func (m *machine) execute(start, end int) error {
	for i := start; i < end; i++ {
		m.steps++
		if m.steps > m.opts.MaxSteps {
			return ErrStepLimit
		}
		ins := &m.ops[i]
		switch {
		case ins.Op == ir.OpParam || ins.Op == ir.OpLabelParam || ins.Op == ir.OpGet || ins.Op == ir.OpSet:
		case ins.IsVector():
			if err := m.vector(i, ins); err != nil {
				return err
			}
			continue
		}
		if err := m.scalar(i, ins); err != nil {
			return err
		}
	}
	return nil
}

func (m *machine) scalar(i int, ins *ir.Instruction) error {
	w := width(ins)
	mask := widthMask(w)
	vals := m.vals
	a := func() value { return vals[ins.A] }
	b := func() value { return vals[ins.B] }
	c := func() value { return vals[ins.C] }
	set := func(x uint64, t taint) { vals[i] = value{x: x & mask, t: t} }
	switch ins.Op {
	case ir.OpParam:
		index := m.paramAt[i]
		if ins.Type.Kind == ir.VectorKind || ins.Type.Kind == ir.MaskKind {
			return fmt.Errorf("vector parameter %d: %w", index, ErrUnsupported)
		}
		set(m.params[index], exact)
	case ir.OpConst:
		x := ins.Imm
		if ins.Symbol != "" {
			bound, ok := m.opts.Symbols[ins.Symbol]
			if !ok && ins.Symbol == ir.MathPi4Symbol {
				bound, ok = pi4Address(), true
			}
			if !ok {
				return fmt.Errorf("unbound symbol %q: %w", ins.Symbol, ErrUnsupported)
			}
			x = bound
		}
		set(x, exact)
	case ir.OpLabelParam:
		vals[i] = m.incoming[ins.Imm]
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		set(integer(ins.Op, a().x, b().x, w), merge(a(), b()))
	case ir.OpAddScaled:
		set(a().x+b().x*ins.Imm, merge(a(), b()))
	case ir.OpSelect:
		pick := c()
		if a().x != 0 {
			pick = b()
		}
		t := pick.t
		if a().t != exact {
			t = unknown
		}
		set(pick.x, t)
	case ir.OpCastUnsigned:
		set(a().x, merge(a()))
	case ir.OpSignExtend:
		set(signExtend(a().x, uint8(ins.Imm)), merge(a()))
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
		set(shift(ins.Op, a().x, ins.Imm, w), merge(a()))
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		immediate, _ := op.ShiftByOperation(ins.Op)
		set(shift(immediate, a().x, b().x, w), merge(a(), b()))
	case ir.OpMulHigh:
		hi, lo := bits.Mul64(a().x, b().x)
		if w < 64 {
			hi = lo >> w
		}
		set(hi, merge(a(), b()))
	case ir.OpMulHighSigned:
		x, y := a().x, b().x
		if w < 64 {
			set(uint64(int64(signExtend(x, w))*int64(signExtend(y, w))>>w), merge(a(), b()))
			break
		}
		// The signed high half subtracts each operand where the other is
		// negative from the unsigned one.
		hi, _ := bits.Mul64(x, y)
		set(hi-x&-(y>>63)-y&-(x>>63), merge(a(), b()))
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		x, defined := divide(ins.Op, ir.DivisionSemantics(ins.Imm), a().x, b().x, w)
		t := merge(a(), b())
		if !defined {
			t = unknown
		}
		set(x, t)
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		vals[i] = floatResult(w, fromInteger(ins.Op == ir.OpFFromSigned, a().x, width(&m.ops[ins.A]), w), a())
		if a().t != exact {
			vals[i].t = unknown
		}
	case ir.OpFToSigned, ir.OpFToUnsigned:
		x, defined := toInteger(ins.Op == ir.OpFToSigned, ir.ConversionSemantics(ins.Imm), a().x, width(&m.ops[ins.A]), w)
		t := exact
		// A NaN's payload does not matter, only that it is a NaN.
		if !defined || a().t == unknown || a().t == nanPayload && ir.ConversionSemantics(ins.Imm) != ir.ConversionSaturate {
			t = unknown
		}
		set(x, t)
	case ir.OpFCompare:
		t := exact
		// A NaN's payload does not matter, only that it is a NaN.
		if a().t == unknown || b().t == unknown {
			t = unknown
		}
		var x uint64
		if floatCompare(ir.FloatCondition(ins.Imm), w, a().x, b().x) {
			x = 1
		}
		set(x, t)
	case ir.OpCarryAdd:
		sum, carry := bits.Add64(a().x, b().x, 0)
		if w < 64 {
			carry = sum >> w & 1
		}
		set(carry, merge(a(), b()))
	case ir.OpBorrowSub:
		var borrow uint64
		if a().x < b().x {
			borrow = 1
		}
		set(borrow, merge(a(), b()))
	case ir.OpAddWithCarry:
		set(a().x+b().x+c().x&1, merge(a(), b(), c()))
	case ir.OpSubWithBorrow:
		set(a().x-b().x-c().x&1, merge(a(), b(), c()))
	case ir.OpLeadingZeros:
		set(uint64(bits.LeadingZeros64(a().x)-(64-int(w))), merge(a()))
	case ir.OpTrailingZeros:
		count := uint64(w)
		if a().x != 0 {
			count = uint64(bits.TrailingZeros64(a().x))
		}
		set(count, merge(a()))
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
		if ins.Op == ir.OpFAdd && ins.Contract && m.opts.Contract {
			for _, pair := range [2][2]int{{ins.A, ins.B}, {ins.B, ins.A}} {
				mul := &m.ops[pair[0]]
				if mul.Op == ir.OpFMul && m.uses[pair[0]] == 1 {
					vals[i] = fused(w, vals[mul.A], vals[mul.B], vals[pair[1]], false)
					return nil
				}
			}
		}
		vals[i] = float(ins.Op, w, a(), b())
	case ir.OpFMA:
		vals[i] = fused(w, a(), b(), c(), false)
	case ir.OpFProductError:
		vals[i] = fused(w, a(), b(), c(), true)
	case ir.OpFSqrt:
		vals[i] = floatResult(w, func() uint64 {
			if w == 32 {
				return uint64(math.Float32bits(float32(math.Sqrt(float64(math.Float32frombits(uint32(a().x)))))))
			}
			return math.Float64bits(math.Sqrt(math.Float64frombits(a().x)))
		}(), a())
	case ir.OpFConvert:
		var x uint64
		if w == 32 {
			x = uint64(math.Float32bits(float32(math.Float64frombits(a().x))))
		} else {
			x = math.Float64bits(float64(math.Float32frombits(uint32(a().x))))
		}
		vals[i] = floatResult(w, x, a())
	case ir.OpLoad8, ir.OpLoad16, ir.OpLoad32, ir.OpLoad64:
		size := map[ir.Opcode]int{ir.OpLoad8: 1, ir.OpLoad16: 2, ir.OpLoad32: 4, ir.OpLoad64: 8}[ins.Op]
		t := merge(a())
		if m.memory {
			t = unknown
		}
		set(load(a().x, size, ins.ByteOrder), t)
	case ir.OpStore8, ir.OpStore16, ir.OpStore32, ir.OpStore64:
		size := map[ir.Opcode]int{ir.OpStore8: 1, ir.OpStore16: 2, ir.OpStore32: 4, ir.OpStore64: 8}[ins.Op]
		if a().t != exact || b().t != exact {
			m.memory = true
		}
		store(a().x, size, ins.ByteOrder, b().x)
	case ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
		if m.opts.Call == nil {
			return fmt.Errorf("%s at value %d without Options.Call: %w", ins.Op, i, ErrUnsupported)
		}
		args := make([]uint64, len(ins.Args)-1)
		t := exact
		for n, id := range ins.Args {
			if vals[id].t != exact {
				t = unknown
			}
			if n > 0 {
				args[n-1] = vals[id].x
			}
		}
		x, err := m.opts.Call(Call{Op: ins.Op, Index: i, Target: vals[ins.Args[0]].x, Args: args})
		if err != nil {
			return err
		}
		set(x, t)
	case ir.OpGet:
		vals[i] = m.vars[ins.Variable][ins.Imm]
	case ir.OpSet:
		m.vars[ins.Variable][ins.Imm] = vals[ins.A]
	case ir.OpScopeEnter:
		for _, id := range m.immutable[int(ins.Imm)] {
			for n, x := range m.program.Variables()[id].Initial {
				m.vars[id][n] = vals[x]
			}
		}
	case ir.OpScopeLeave:
	default:
		return fmt.Errorf("%s at value %d: %w", ins.Op, i, ErrUnsupported)
	}
	return nil
}

func integer(op ir.Opcode, a, b uint64, w uint8) uint64 {
	var x uint64
	switch op {
	case ir.OpAdd:
		x = a + b
	case ir.OpSub:
		x = a - b
	case ir.OpMul:
		x = a * b
	case ir.OpAnd:
		x = a & b
	case ir.OpOr:
		x = a | b
	case ir.OpXor:
		x = a ^ b
	}
	return x & widthMask(w)
}

// shift applies an immediate-form shift with any count: shifts by at least
// the width give zero (or all sign bits), rotations reduce the count.
func shift(op ir.Opcode, x, count uint64, w uint8) uint64 {
	mask := widthMask(w)
	x &= mask
	switch op {
	case ir.OpRotateLeft, ir.OpRotateRight:
		count %= uint64(w)
		if op == ir.OpRotateRight {
			count = (uint64(w) - count) % uint64(w)
		}
		if count == 0 {
			return x
		}
		return (x<<count | x>>(uint64(w)-count)) & mask
	case ir.OpShiftRightArithmetic:
		if count >= uint64(w) {
			count = uint64(w) - 1
		}
		return uint64(int64(signExtend(x, w))>>count) & mask
	}
	if count >= uint64(w) {
		return 0
	}
	if op == ir.OpShiftLeft {
		return x << count & mask
	}
	return x >> count
}

// divide divides w-bit x by y as op does with semantics s, and reports
// whether the IR defines the result: an unchecked division by zero or of
// signed MIN by -1 does not.
func divide(op ir.Opcode, s ir.DivisionSemantics, x, y uint64, w uint8) (uint64, bool) {
	mask := widthMask(w)
	rem := op == ir.OpRem || op == ir.OpRemSigned
	if y == 0 {
		switch {
		case s == ir.DivisionUnchecked:
			return 0, false
		case rem:
			return x, true
		case s == ir.DivisionAllOnes:
			return mask, true
		default:
			return 0, true
		}
	}
	if op == ir.OpDiv {
		return x / y, true
	}
	if op == ir.OpRem {
		return x % y, true
	}
	sx, sy := int64(signExtend(x, w)), int64(signExtend(y, w))
	if sy == -1 && sx == -1<<(w-1) {
		if s == ir.DivisionUnchecked {
			return 0, false
		}
		if rem {
			return 0, true
		}
		return x, true // MIN
	}
	if rem {
		return uint64(sx%sy) & mask, true
	}
	return uint64(sx/sy) & mask, true
}

// fromInteger converts integer x of in bits to a binary32 or binary64 (out)
// bit pattern, rounding to nearest even.
func fromInteger(signed bool, x uint64, in, out uint8) uint64 {
	if signed {
		v := int64(signExtend(x, in))
		if out == 32 {
			if v < 0 {
				return uint64(math.Float32bits(-float32FromMagnitude(-uint64(v))))
			}
			return uint64(math.Float32bits(float32FromMagnitude(uint64(v))))
		}
		return math.Float64bits(float64(v))
	}
	if out == 32 {
		return uint64(math.Float32bits(float32FromMagnitude(x)))
	}
	return math.Float64bits(float64(x))
}

// float32FromMagnitude rounds m to binary32 once. Go's 386 runtime rounds
// some 64-bit integers wrongly when converting them to float32, so m is
// first rounded to odd at 53 bits; that binary64 value is exact, and its
// conversion to binary32 rounds as m itself would.
func float32FromMagnitude(m uint64) float32 {
	if n := bits.Len64(m); n > 53 {
		shift := uint(n - 53)
		odd := m&(1<<shift-1) != 0
		m >>= shift
		if odd {
			m |= 1
		}
		return float32(math.Ldexp(float64(m), int(shift)))
	}
	return float32(float64(m))
}

// toInteger truncates the binary32 or binary64 (in) bit pattern x toward
// zero to an integer of out bits, and reports whether the IR defines the
// result: with native semantics, a NaN or out-of-range value has none.
func toInteger(signed bool, s ir.ConversionSemantics, x uint64, in, out uint8) (uint64, bool) {
	f := math.Float64frombits(x)
	if in == 32 {
		f = float64(math.Float32frombits(uint32(x)))
	}
	mask := widthMask(out)
	if math.IsNaN(f) {
		return 0, s == ir.ConversionSaturate
	}
	t := math.Trunc(f)
	if signed {
		lo, hi := -math.Ldexp(1, int(out)-1), math.Ldexp(1, int(out)-1)
		switch {
		case t < lo:
			return uint64(1) << (out - 1), s == ir.ConversionSaturate
		case t >= hi:
			return mask >> 1, s == ir.ConversionSaturate
		}
		return uint64(int64(t)) & mask, true
	}
	switch {
	case t < 0:
		return 0, s == ir.ConversionSaturate
	case t >= math.Ldexp(1, int(out)):
		return mask, s == ir.ConversionSaturate
	}
	return uint64(t) & mask, true
}

// floatResult replaces a NaN with the default quiet NaN and marks its
// payload as target-chosen. Any result of NaN-derived inputs is unknown: the
// reference's stand-in bits for them may be a NaN where the target's are not,
// or the reverse.
func floatResult(w uint8, x uint64, inputs ...value) value {
	for _, v := range inputs {
		if v.t == unknown {
			return value{x: x, t: unknown}
		}
	}
	nan := w == 32 && math.IsNaN(float64(math.Float32frombits(uint32(x)))) || w == 64 && math.IsNaN(math.Float64frombits(x))
	if nan {
		if w == 32 {
			return value{x: 0x7fc00000, t: nanPayload}
		}
		return value{x: 0x7ff8000000000000, t: nanPayload}
	}
	return value{x: x}
}

// floatCompare evaluates condition c for w-bit floats a and b. Widening
// binary32 to binary64 is exact, so it preserves every relation.
func floatCompare(c ir.FloatCondition, w uint8, a, b uint64) bool {
	p, q := math.Float64frombits(a), math.Float64frombits(b)
	if w == 32 {
		p, q = float64(math.Float32frombits(uint32(a))), float64(math.Float32frombits(uint32(b)))
	}
	var r bool
	switch c &^ 1 {
	case ir.FloatEqual:
		r = p == q
	case ir.FloatLess:
		r = p < q
	case ir.FloatLessEqual:
		r = p <= q
	case ir.FloatGreater:
		r = p > q
	case ir.FloatGreaterEqual:
		r = p >= q
	case ir.FloatOrdered:
		r = p == p && q == q
	case ir.FloatOrderedNotEqual:
		r = p < q || p > q
	}
	return r != (c&1 != 0)
}

func float(op ir.Opcode, w uint8, a, b value) value {
	var x uint64
	if w == 32 {
		p, q := math.Float32frombits(uint32(a.x)), math.Float32frombits(uint32(b.x))
		var r float32
		switch op {
		case ir.OpFAdd:
			r = float32(p + q)
		case ir.OpFSub:
			r = float32(p - q)
		case ir.OpFMul:
			r = float32(p * q)
		default:
			r = float32(p / q)
		}
		x = uint64(math.Float32bits(r))
	} else {
		p, q := math.Float64frombits(a.x), math.Float64frombits(b.x)
		var r float64
		switch op {
		case ir.OpFAdd:
			r = float64(p + q)
		case ir.OpFSub:
			r = float64(p - q)
		case ir.OpFMul:
			r = float64(p * q)
		default:
			r = float64(p / q)
		}
		x = math.Float64bits(r)
	}
	return floatResult(w, x, a, b)
}

// fused returns a*b+c with one rounding, or the residual a*b-c when
// residual is set.
func fused(w uint8, a, b, c value, residual bool) value {
	if w == 64 {
		p, q, r := math.Float64frombits(a.x), math.Float64frombits(b.x), math.Float64frombits(c.x)
		if residual {
			r = -r
		}
		return floatResult(w, math.Float64bits(math.FMA(p, q, r)), a, b, c)
	}
	p, q, r := math.Float32frombits(uint32(a.x)), math.Float32frombits(uint32(b.x)), math.Float32frombits(uint32(c.x))
	return floatResult(w, uint64(math.Float32bits(fma32(p, q, r))), a, b, c)
}

// fma32 rounds p*q+r once to binary32. The binary64 product is exact; when
// all operands are finite and nonzero the sum is formed exactly too.
func fma32(p, q, r float32) float32 {
	x, y, z := float64(p), float64(q), float64(r)
	product := x * y
	if product == 0 || z == 0 || math.IsInf(product, 0) || math.IsNaN(product) || math.IsInf(z, 0) || math.IsNaN(z) {
		return float32(math.FMA(x, y, z))
	}
	sum := new(big.Float).SetPrec(512).SetFloat64(product)
	sum.Add(sum, new(big.Float).SetPrec(512).SetFloat64(z))
	if sum.Sign() == 0 {
		return 0
	}
	f, _ := sum.Float32()
	return f
}

// pointer turns an address the program computed into a pointer.
func pointer(address uint64) unsafe.Pointer {
	p := uintptr(address)
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

func load(address uint64, size int, order ir.ByteOrder) uint64 {
	memory := unsafe.Slice((*byte)(pointer(address)), size)
	var x uint64
	for n := range size {
		shift := 8 * n
		if order == ir.ByteOrderBigEndian {
			shift = 8 * (size - 1 - n)
		}
		x |= uint64(memory[n]) << shift
	}
	return x
}

func store(address uint64, size int, order ir.ByteOrder, x uint64) {
	memory := unsafe.Slice((*byte)(pointer(address)), size)
	for n := range size {
		shift := 8 * n
		if order == ir.ByteOrderBigEndian {
			shift = 8 * (size - 1 - n)
		}
		memory[n] = byte(x >> shift)
	}
}
