package nyanjit

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Builder records construction errors and reports the first one at Finish.
// Once an operation fails, later operations leave the underlying program alone.
type Builder struct {
	inner    *builder.Builder
	stream   *compile.Stream // set for a Builder from NewBuilderFor
	bound    bool
	err      error
	finished bool
}

// NewBuilder creates a Builder whose operations do not return errors.
func NewBuilder() *Builder { return &Builder{inner: builder.NewBuilder()} }

// NewBuilderFor creates a Builder bound to t and f. Lower compiles its
// function at t's tier: TierBaseline without building SSA, TierOptimized as
// Lower does for a sealed program. Both tiers accept the same programs. An
// error from compile.NewStream is recorded and returned by Lower.
func NewBuilderFor(t compile.Target, f compile.Func) *Builder {
	b := &Builder{bound: true}
	stream, err := compile.NewStream(t, f)
	if err != nil {
		b.err = fmt.Errorf("NewBuilderFor: %w", err)
		b.inner = builder.NewSegmentBuilder(func(*ir.Segment) error { return nil })
		return b
	}
	b.stream = stream
	b.inner = builder.NewSegmentBuilder(stream.Segment)
	return b
}

// Lower ends a Builder from NewBuilderFor and returns its Unit, or the first
// error recorded while building or lowering. Misuse wraps
// compile.ErrInvalidFunction; backend limits match compile.ErrBackendLimit,
// with a *compile.LimitError when the limit is a number; an operation or Func
// the baseline tier does not implement yet matches compile.ErrUnsupported. On
// a Builder from NewBuilder it returns an error wrapping
// compile.ErrInvalidFunction.
func (b *Builder) Lower() (*compile.Unit, error) {
	if b == nil || !b.bound {
		return nil, fmt.Errorf("Lower: Builder is not bound to a Target; use Finish: %w", compile.ErrInvalidFunction)
	}
	if b.finished {
		return nil, fmt.Errorf("Lower: Builder already finished: %w", compile.ErrInvalidFunction)
	}
	b.finished = true
	if b.err != nil {
		return nil, b.err
	}
	if err := b.inner.End(); err != nil {
		return nil, limitError(err)
	}
	return b.stream.Unit()
}

func (b *Builder) ready() bool {
	if b != nil && !b.finished && b.inner != nil && b.err == nil {
		return true
	}
	return b.notReady()
}

// notReady is ready's slow path: it records misuse of a finished or
// uninitialized Builder.
func (b *Builder) notReady() bool {
	if b == nil {
		return false
	}
	if b.finished || b.inner == nil {
		if b.err == nil {
			b.err = fmt.Errorf("Builder: already finished or uninitialized: %w", compile.ErrInvalidFunction)
		}
		return false
	}
	return b.err == nil
}

// okValue, okPred, okVar and okDo finish an operation on a ready Builder:
// an error is recorded and the result is the zero value. They stay small
// enough to inline; failOp records the error.
func (b *Builder) okValue(v Value, err error) Value {
	if err != nil {
		b.failOp(err)
		return Value{}
	}
	return v
}

func (b *Builder) okPred(p Predicate, err error) Predicate {
	if err != nil {
		b.failOp(err)
		return Predicate{}
	}
	return p
}

func (b *Builder) okVar(v Var, err error) Var {
	if err != nil {
		b.failOp(err)
		return Var{}
	}
	return v
}

func (b *Builder) okDo(err error) {
	if err != nil {
		b.failOp(err)
	}
}

// failOp records err of the exported operation that called okValue, okPred,
// okVar or okDo.
//
//go:noinline
func (b *Builder) failOp(err error) { b.fail(operationErrorAt(err, 3)) }

func (b *Builder) fail(err error) {
	if err != nil && b != nil && b.err == nil {
		b.err = limitError(err)
	}
}

// limitError reports a numeric limit the builder package checked as a
// *compile.LimitError. Other errors are returned unchanged.
func limitError(err error) error {
	// errors.As moves its target to the heap. Keep the nil error of every
	// successful operation from paying for it.
	if err == nil {
		return nil
	}
	var limit interface {
		BackendLimit() (kind string, needed, limit int, detail string)
	}
	if !errors.As(err, &limit) {
		return err
	}
	name, needed, max, detail := limit.BackendLimit()
	for kind := compile.LimitSpillWords; kind <= compile.LimitArguments; kind++ {
		if kind.String() == name {
			return &compile.LimitError{Kind: kind, Needed: needed, Limit: max, Detail: detail}
		}
	}
	return err
}

// operationError prefixes err with the name of the exported operation that
// called the take helper or do, unless err already carries it.
func operationError(err error) error { return operationErrorAt(err, 3) }

// operationErrorAt is operationError for the operation skip frames above
// it.
func operationErrorAt(err error, skip int) error {
	if err == nil {
		return nil
	}
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return err
	}
	name := runtime.FuncForPC(pc).Name()
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:]
	}
	if strings.HasPrefix(err.Error(), name+":") {
		return err
	}
	return fmt.Errorf("%s: %w", name, err)
}

// takeValue, takeValues and do run one operation through a closure, for the
// operations that do more than call the inner Builder (most call it directly
// and finish with okValue and its siblings). Exported operations call them
// directly, so operationError can name the operation. They are not generic: a
// generic helper inlined into another package moved every operation's closure
// to the heap.
func takeValue(b *Builder, run func() (Value, error)) Value {
	if !b.ready() {
		return Value{}
	}
	value, err := run()
	if err != nil {
		b.fail(operationError(err))
		return Value{}
	}
	return value
}

func do(b *Builder, run func() error) {
	if !b.ready() {
		return
	}
	if err := run(); err != nil {
		b.fail(operationError(err))
	}
}

// takeValues returns count invalid Values on failure.
func takeValues(b *Builder, count int, run func() ([]Value, error)) []Value {
	if !b.ready() {
		return make([]Value, max(count, 0))
	}
	result, err := run()
	if err != nil {
		b.fail(operationError(err))
		return make([]Value, max(count, 0))
	}
	return result
}

// Finish consumes the Builder and returns its first construction error, if any.
// Misuse, such as an unbound label, an open scope, a value used outside its
// scope or a second Finish, returns an error wrapping compile.ErrInvalidFunction.
// A program beyond a backend limit the Builder checks itself (entry
// parameters, call or system-call arguments, predicate size)
// returns a *compile.LimitError wrapping compile.ErrBackendLimit, as
// compile.Lower does for the limits that depend on the target. On a Builder
// from NewBuilderFor, Finish returns an error wrapping
// compile.ErrInvalidFunction; use Lower.
func (b *Builder) Finish() (*ir.SealedProgram, error) {
	if b == nil || b.finished {
		return nil, compile.ErrInvalidFunction
	}
	if b.bound {
		b.finished = true
		return nil, fmt.Errorf("Finish: Builder is bound to a Target; use Lower: %w", compile.ErrInvalidFunction)
	}
	b.finished = true
	if b.err != nil {
		return nil, b.err
	}
	if b.inner == nil {
		return nil, compile.ErrInvalidFunction
	}
	program, err := b.inner.Finish()
	return program, limitError(err)
}

// Valid reports whether a value is currently usable by this Builder.
func (b *Builder) Valid(value Value) bool {
	return b != nil && b.ready() && b.inner.Valid(value)
}

// NewLabel reserves a control-flow label in the current scope. The Label
// belongs to this Builder: another Builder rejects it.
func (b *Builder) NewLabel() Label {
	if !b.ready() {
		return Label{}
	}
	label := b.inner.NewLabel()
	if label == (Label{}) {
		b.fail(fmt.Errorf("NewLabel: cannot create a label after Finish or Return: %w", compile.ErrInvalidFunction))
	}
	return label
}

// IndexedAddress checks unsigned bounds, branches to outOfBounds on failure,
// and returns the address in a fresh in-bounds block, which earlier values
// dominate. The returned address and aliases cannot cross control edges or be
// stored in Vars; take a fresh checked address in each destination block.
func (b *Builder) IndexedAddress(base, index, count Value, scale uint64, outOfBounds Label) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.IndexedAddress(base, index, count, scale, outOfBounds))
}

// Param adds a 64-bit entry argument.
func (b *Builder) Param() Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Param())
}

// ParamBits adds an entry argument with the given integer width.
func (b *Builder) ParamBits(bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ParamBits(bits))
}

// ParamNative adds a pointer-width entry argument.
func (b *Builder) ParamNative() Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ParamNative())
}

// Const creates a 64-bit integer constant.
func (b *Builder) Const(value uint64) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Const(value))
}

// Literal creates a pure constant expression, materialized at its use site.
func (b *Builder) Literal(value uint64) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Literal(value))
}

// LiteralBits creates a pure width-limited constant expression.
func (b *Builder) LiteralBits(bits uint8, value uint64) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.LiteralBits(bits, value))
}

// ConstBits creates an integer constant truncated to bits.
func (b *Builder) ConstBits(bits uint8, value uint64) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ConstBits(bits, value))
}

// ConstNative creates a pointer-width constant.
func (b *Builder) ConstNative(value uintptr) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ConstNative(value))
}

// ConstBytes creates an integer constant from little-endian bytes.
func (b *Builder) ConstBytes(bits uint8, data []byte) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ConstBytes(bits, data))
}

// Symbol creates a constant address resolved when the program is bound.
func (b *Builder) Symbol(name string) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Symbol(name))
}

// Add adds equal-width values modulo their width.
func (b *Builder) Add(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Add(x, y))
}

// Sub subtracts equal-width values modulo their width.
func (b *Builder) Sub(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Sub(x, y))
}

// Mul multiplies equal-width values modulo their width.
func (b *Builder) Mul(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Mul(x, y))
}

// Float32 creates a 32-bit IEEE 754 bit-pattern constant.
func (b *Builder) Float32(value float32) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Float32(value))
}

// Float64 creates a 64-bit IEEE 754 bit-pattern constant.
func (b *Builder) Float64(value float64) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Float64(value))
}

// FAdd adds two equal-width floating point bit patterns.
func (b *Builder) FAdd(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAdd(x, y))
}

// FSub subtracts two equal-width floating point bit patterns.
func (b *Builder) FSub(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FSub(x, y))
}

// FMul multiplies two equal-width floating point bit patterns.
func (b *Builder) FMul(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FMul(x, y))
}

// FDiv divides two equal-width floating point bit patterns.
func (b *Builder) FDiv(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FDiv(x, y))
}

// FSqrt computes the square root of a 32- or 64-bit floating point bit pattern.
func (b *Builder) FSqrt(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FSqrt(x))
}

// FConvert converts between binary32 and binary64 floating point bit patterns.
func (b *Builder) FConvert(x Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FConvert(x, bits))
}

// FMA computes x*y+addend with one IEEE 754 rounding step.
func (b *Builder) FMA(x, y, addend Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FMA(x, y, addend))
}

// And computes bitwise AND of equal-width values.
func (b *Builder) And(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.And(x, y))
}

// Or computes bitwise OR of equal-width values.
func (b *Builder) Or(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Or(x, y))
}

// Xor computes bitwise XOR of equal-width values.
func (b *Builder) Xor(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Xor(x, y))
}

// Select chooses ifTrue when condition is nonzero, otherwise ifFalse.
func (b *Builder) Select(condition, ifTrue, ifFalse Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Select(condition, ifTrue, ifFalse))
}

// ShiftLeft shifts value left by a constant count.
func (b *Builder) ShiftLeft(value Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ShiftLeft(value, bits))
}

// ShiftRight shifts an unsigned value right by a constant count.
func (b *Builder) ShiftRight(value Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ShiftRight(value, bits))
}

// ShiftRightArithmetic shifts a signed value right by a constant count.
func (b *Builder) ShiftRightArithmetic(value Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ShiftRightArithmetic(value, bits))
}

// RotateLeft rotates value left by a constant count.
func (b *Builder) RotateLeft(value Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.RotateLeft(value, bits))
}

// RotateRight rotates value right by a constant count.
func (b *Builder) RotateRight(value Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.RotateRight(value, bits))
}

// ShiftLeftBy shifts value left by a Value count.
func (b *Builder) ShiftLeftBy(value, count Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ShiftLeftBy(value, count))
}

// ShiftRightBy shifts an unsigned value right by a Value count.
func (b *Builder) ShiftRightBy(value, count Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ShiftRightBy(value, count))
}

// ShiftRightArithmeticBy shifts a signed value right by a Value count.
func (b *Builder) ShiftRightArithmeticBy(value, count Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ShiftRightArithmeticBy(value, count))
}

// RotateLeftBy rotates value left by a Value count.
func (b *Builder) RotateLeftBy(value, count Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.RotateLeftBy(value, count))
}

// RotateRightBy rotates value right by a Value count.
func (b *Builder) RotateRightBy(value, count Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.RotateRightBy(value, count))
}

// CastUnsigned truncates or zero-extends value to bits.
func (b *Builder) CastUnsigned(value Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CastUnsigned(value, bits))
}

// CastNative truncates or zero-extends value to the pointer width.
func (b *Builder) CastNative(value Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CastNative(value))
}

// SignExtend sign-extends the low width bits of value to 64 bits.
func (b *Builder) SignExtend(value Value, width uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.SignExtend(value, width))
}

// Concat joins values with the first part in the low bits.
func (b *Builder) Concat(parts ...Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Concat(parts...))
}

// ExtractBits extracts bits bits starting at offset.
func (b *Builder) ExtractBits(value Value, offset, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ExtractBits(value, offset, bits))
}

// Load64 reads eight bytes from a native or 64-bit address with target alignment.
func (b *Builder) Load64(address Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Load64(address))
}

// Load8 reads one byte from a 64-bit address at any byte alignment.
func (b *Builder) Load8(address Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Load8(address))
}

// Load16 reads a little-endian Scalar(16) at any byte alignment in ordinary
// user memory. The address has native or 64-bit width. The access is not atomic.
func (b *Builder) Load16(address Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Load16(address))
}

// Load32 is Load16's 32-bit counterpart, returning Scalar(32).
func (b *Builder) Load32(address Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Load32(address))
}

// LoadBits reads a little-endian integer of the requested width. Widths up to
// 32 bits permit any byte alignment; wider accesses require target alignment.
func (b *Builder) LoadBits(address Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.LoadBits(address, bits))
}

// Store64 writes eight bytes to a native address.
func (b *Builder) Store64(address, value Value) {
	if b.ready() {
		b.okDo(b.inner.Store64(address, value))
	}
}

// Store8 writes the low byte of an integer value to a native address.
func (b *Builder) Store8(address, value Value) {
	if b.ready() {
		b.okDo(b.inner.Store8(address, value))
	}
}

// Store16 permits any byte alignment in ordinary user memory and writes the low two bytes of a 16- or 64-bit value.
func (b *Builder) Store16(address, value Value) {
	if b.ready() {
		b.okDo(b.inner.Store16(address, value))
	}
}

// Store32 permits any byte alignment in ordinary user memory and writes the low four bytes of a 32- or 64-bit value.
func (b *Builder) Store32(address, value Value) {
	if b.ready() {
		b.okDo(b.inner.Store32(address, value))
	}
}

// StoreBits writes a width-matched integer in little-endian order to a native address.
func (b *Builder) StoreBits(address, value Value) {
	if b.ready() {
		b.okDo(b.inner.StoreBits(address, value))
	}
}

// Syscall records a raw Linux syscall for an external execution context.
func (b *Builder) Syscall(number Value, args ...Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Syscall(number, args...))
}

// CallC records an indirect C ABI call. Integer argument extension follows the
// target psABI: loong64/riscv64 sign-extend 32-bit IR values, including uint32.
// Pass a 64-bit IR value for a 64-bit C parameter.
func (b *Builder) CallC(target Value, args ...Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CallC(target, args...))
}

// CallGo records a Go ABI call. Callable compilation requires a fixed ordinary splittable
// Go function constant or symbol; dynamic and generated targets are rejected.
func (b *Builder) CallGo(target Value, args ...Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CallGo(target, args...))
}

// CallCWithEffect records a C ABI call with a supplied effect summary.
// Arguments follow CallC's target psABI width/extension rules.
func (b *Builder) CallCWithEffect(target Value, effect abi.CallEffect, args ...Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CallCWithEffect(target, effect, args...))
}

// CallGoWithEffect records a Go ABI call with a supplied effect summary.
func (b *Builder) CallGoWithEffect(target Value, effect abi.CallEffect, args ...Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CallGoWithEffect(target, effect, args...))
}

// Compare compares two Values and returns a deferred predicate.
func (b *Builder) Compare(left Value, condition ir.BranchCondition, right Value) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	return b.okPred(b.inner.Compare(left, condition, right))
}

// CompareImmediate compares a Value with an immediate and returns a predicate.
func (b *Builder) CompareImmediate(value Value, condition ir.BranchCondition, immediate int64) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	return b.okPred(b.inner.CompareImmediate(value, condition, immediate))
}

// IsZero tests whether value is zero.
func (b *Builder) IsZero(value Value) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	return b.okPred(b.inner.IsZero(value))
}

// IsNegative tests whether value is negative at its width.
func (b *Builder) IsNegative(value Value) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	return b.okPred(b.inner.IsNegative(value))
}

// Carry tests carry from Add or borrow from Sub.
func (b *Builder) Carry(value Value) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	return b.okPred(b.inner.Carry(value))
}

// Enter opens a nested value scope.
// It also terminates the preceding block when the scope contains a jump or
// branch, allowing Bind immediately after Enter without an explicit Jump.
func (b *Builder) Enter() {
	if b.ready() {
		b.okDo(b.inner.Enter())
	}
}

// Leave closes a scope and promotes the listed values to its parent.
func (b *Builder) Leave(exports ...Value) {
	if b.ready() {
		b.okDo(b.inner.Leave(exports...))
	}
}

// Bind starts the block at label, supplying an implicit edge from the
// preceding block if it is still open and nonempty.
func (b *Builder) Bind(label Label) {
	if b.ready() {
		b.okDo(b.inner.Bind(label))
	}
}

// Jump ends the block with an unconditional jump.
func (b *Builder) Jump(label Label) {
	if b.ready() {
		b.okDo(b.inner.Jump(label))
	}
}

// Branch continues at target when predicate holds. Otherwise execution
// continues after the call, in a new block. For a two-way branch, follow it
// with Jump; lowering emits the pair as one conditional branch. An empty
// Predicate always holds, so Branch then acts as Jump. Predicate chains have
// no separate depth limit. Finish reports an error wrapping
// compile.ErrBackendLimit above ir.MaxPredicateNodes (16,384) expanded
// predicate nodes, counting each use of a shared subtree.
func (b *Builder) Branch(predicate Predicate, target Label) {
	if b.ready() {
		b.okDo(b.inner.Branch(predicate, target))
	}
}

// Return ends the block or function with value as its result.
func (b *Builder) Return(value Value) {
	if b.ready() {
		b.okDo(b.inner.Return(value))
	}
}

// Var identifies mutable storage in a Builder scope.
type Var = builder.Var

// Var declares initialized mutable storage in the current scope entry sequence.
func (b *Builder) Var(initial Value) Var {
	if !b.ready() {
		return Var{}
	}
	return b.okVar(b.inner.Var(initial))
}

// Get returns a block-local snapshot of v's current value.
func (b *Builder) Get(v Var) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Get(v))
}

// Set replaces v's value; x must have the same type.
func (b *Builder) Set(v Var, x Value) {
	if b.ready() {
		b.okDo(b.inner.Set(v, x))
	}
}

// FCompare compares equal-width floating point bit patterns, returning 1 when
// condition holds and 0 otherwise.
func (b *Builder) FCompare(x Value, condition ir.FloatCondition, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FCompare(x, condition, y))
}

// FComparePredicate is the branch predicate of FCompare(x, condition, y).
func (b *Builder) FComparePredicate(x Value, condition ir.FloatCondition, y Value) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	return b.okPred(b.inner.FComparePredicate(x, condition, y))
}
