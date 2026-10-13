package nyanjit

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

// Builder records construction errors and reports the first one at Finish.
// Once an operation fails, later operations leave the underlying program alone.
type Builder struct {
	inner    *builder.Builder
	err      error
	finished bool
}

// NewBuilder creates a Builder whose operations do not return errors.
func NewBuilder() *Builder { return &Builder{inner: builder.NewBuilder()} }

func (b *Builder) ready() bool {
	if b == nil {
		return false
	}
	if b.finished || b.inner == nil {
		if b.err == nil {
			b.err = fmt.Errorf("Builder: already finished or uninitialized: %w", ErrInvalidFunction)
		}
		return false
	}
	return b.err == nil
}

func (b *Builder) fail(err error) {
	if b != nil && b.err == nil {
		b.err = err
	}
}

func operationError(err error) error {
	if err == nil {
		return nil
	}
	pc, _, _, ok := runtime.Caller(2)
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

func takePredicate(b *Builder, run func() (Predicate, error)) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	predicate, err := run()
	if err != nil {
		b.fail(operationError(err))
		return Predicate{}
	}
	return predicate
}

func takeValues(b *Builder, count int, run func() ([]Value, error)) []Value {
	values := make([]Value, max(count, 0))
	if !b.ready() {
		return values
	}
	result, err := run()
	if err != nil {
		b.fail(operationError(err))
		return values
	}
	return result
}

func do(b *Builder, run func() error) {
	if b.ready() {
		b.fail(operationError(run()))
	}
}

// Finish consumes the Builder and returns its first construction error, if any.
func (b *Builder) Finish() (*ir.Program, error) {
	if b == nil || b.finished {
		return nil, ErrInvalidFunction
	}
	b.finished = true
	if b.err != nil {
		return nil, b.err
	}
	if b.inner == nil {
		return nil, ErrInvalidFunction
	}
	return b.inner.Finish()
}

// Valid reports whether a value is currently usable by this Builder.
func (b *Builder) Valid(value Value) bool {
	return b != nil && b.ready() && b.inner.Valid(value)
}

// NewLabel reserves a control-flow label in the current scope.
func (b *Builder) NewLabel() Label {
	if !b.ready() {
		return -1
	}
	label := b.inner.NewLabel()
	if label < 0 {
		b.fail(fmt.Errorf("NewLabel: cannot create a label after Finish or Return: %w", ErrInvalidFunction))
	}
	return label
}

// IndexedAddress returns an address and its unsigned bounds predicate.
func (b *Builder) IndexedAddress(base, index, count Value, scale uint64) (Value, Predicate) {
	if !b.ready() {
		return Value{}, Predicate{}
	}
	address, predicate, err := b.inner.IndexedAddress(base, index, count, scale)
	if err != nil {
		b.fail(err)
		return Value{}, Predicate{}
	}
	return address, predicate
}

// Param adds a 64-bit entry argument.
func (b *Builder) Param() Value {
	return takeValue(b, func() (Value, error) { return b.inner.Param() })
}

// ParamBits adds an entry argument with the given integer width.
func (b *Builder) ParamBits(bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ParamBits(bits) })
}

// ParamNative adds a pointer-width entry argument.
func (b *Builder) ParamNative() Value {
	return takeValue(b, func() (Value, error) { return b.inner.ParamNative() })
}

// Const creates a 64-bit integer constant.
func (b *Builder) Const(value uint64) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Const(value) })
}

// Literal creates a pure constant expression, materialized at its use site.
func (b *Builder) Literal(value uint64) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Literal(value) })
}

// LiteralBits creates a pure width-limited constant expression.
func (b *Builder) LiteralBits(bits uint8, value uint64) Value {
	return takeValue(b, func() (Value, error) { return b.inner.LiteralBits(bits, value) })
}

// ConstBits creates an integer constant truncated to bits.
func (b *Builder) ConstBits(bits uint8, value uint64) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ConstBits(bits, value) })
}

// ConstNative creates a pointer-width constant.
func (b *Builder) ConstNative(value uintptr) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ConstNative(value) })
}

// ConstBytes creates an integer constant from little-endian bytes.
func (b *Builder) ConstBytes(bits uint8, data []byte) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ConstBytes(bits, data) })
}

// Symbol creates a constant address resolved when the program is bound.
func (b *Builder) Symbol(name string) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Symbol(name) })
}

// Add adds equal-width values modulo their width.
func (b *Builder) Add(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Add(x, y) })
}

// Sub subtracts equal-width values modulo their width.
func (b *Builder) Sub(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Sub(x, y) })
}

// Mul multiplies equal-width values modulo their width.
func (b *Builder) Mul(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Mul(x, y) })
}

// Float32 creates a 32-bit IEEE 754 bit-pattern constant.
func (b *Builder) Float32(value float32) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Float32(value) })
}

// Float64 creates a 64-bit IEEE 754 bit-pattern constant.
func (b *Builder) Float64(value float64) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Float64(value) })
}

// FAdd adds two equal-width floating point bit patterns.
func (b *Builder) FAdd(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.FAdd(x, y) })
}

// FSub subtracts two equal-width floating point bit patterns.
func (b *Builder) FSub(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.FSub(x, y) })
}

// FMul multiplies two equal-width floating point bit patterns.
func (b *Builder) FMul(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.FMul(x, y) })
}

// FDiv divides two equal-width floating point bit patterns.
func (b *Builder) FDiv(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.FDiv(x, y) })
}

// FSqrt computes the square root of a 32- or 64-bit floating point bit pattern.
func (b *Builder) FSqrt(x Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.FSqrt(x) })
}

// FConvert converts between binary32 and binary64 floating point bit patterns.
func (b *Builder) FConvert(x Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.FConvert(x, bits) })
}

// FMA computes x*y+addend with one IEEE 754 rounding step.
func (b *Builder) FMA(x, y, addend Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.FMA(x, y, addend) })
}

// And computes bitwise AND of equal-width values.
func (b *Builder) And(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.And(x, y) })
}

// Or computes bitwise OR of equal-width values.
func (b *Builder) Or(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Or(x, y) })
}

// Xor computes bitwise XOR of equal-width values.
func (b *Builder) Xor(x, y Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Xor(x, y) })
}

// Select chooses ifTrue when condition is nonzero, otherwise ifFalse.
func (b *Builder) Select(condition, ifTrue, ifFalse Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Select(condition, ifTrue, ifFalse) })
}

// ShiftLeft shifts value left by a constant count.
func (b *Builder) ShiftLeft(value Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ShiftLeft(value, bits) })
}

// ShiftRight shifts an unsigned value right by a constant count.
func (b *Builder) ShiftRight(value Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ShiftRight(value, bits) })
}

// ShiftRightArithmetic shifts a signed value right by a constant count.
func (b *Builder) ShiftRightArithmetic(value Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ShiftRightArithmetic(value, bits) })
}

// RotateLeft rotates value left by a constant count.
func (b *Builder) RotateLeft(value Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.RotateLeft(value, bits) })
}

// RotateRight rotates value right by a constant count.
func (b *Builder) RotateRight(value Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.RotateRight(value, bits) })
}

// ShiftLeftBy shifts value left by a Value count.
func (b *Builder) ShiftLeftBy(value, count Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ShiftLeftBy(value, count) })
}

// ShiftRightBy shifts an unsigned value right by a Value count.
func (b *Builder) ShiftRightBy(value, count Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ShiftRightBy(value, count) })
}

// ShiftRightArithmeticBy shifts a signed value right by a Value count.
func (b *Builder) ShiftRightArithmeticBy(value, count Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ShiftRightArithmeticBy(value, count) })
}

// RotateLeftBy rotates value left by a Value count.
func (b *Builder) RotateLeftBy(value, count Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.RotateLeftBy(value, count) })
}

// RotateRightBy rotates value right by a Value count.
func (b *Builder) RotateRightBy(value, count Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.RotateRightBy(value, count) })
}

// CastUnsigned truncates or zero-extends value to bits.
func (b *Builder) CastUnsigned(value Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.CastUnsigned(value, bits) })
}

// CastNative truncates or zero-extends value to the pointer width.
func (b *Builder) CastNative(value Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.CastNative(value) })
}

// SignExtend sign-extends the low width bits of value to 64 bits.
func (b *Builder) SignExtend(value Value, width uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.SignExtend(value, width) })
}

// Concat joins values with the first part in the low bits.
func (b *Builder) Concat(parts ...Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Concat(parts...) })
}

// ExtractBits extracts bits bits starting at offset.
func (b *Builder) ExtractBits(value Value, offset, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ExtractBits(value, offset, bits) })
}

// Load64 reads eight bytes from a native address.
func (b *Builder) Load64(address Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Load64(address) })
}

// Load8 reads one byte from a native address.
func (b *Builder) Load8(address Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Load8(address) })
}

// Load16 reads two little-endian bytes from a native address.
func (b *Builder) Load16(address Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Load16(address) })
}

// Load32 reads four little-endian bytes from a native address.
func (b *Builder) Load32(address Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Load32(address) })
}

// LoadBits reads a little-endian integer of the requested width from a native address.
func (b *Builder) LoadBits(address Value, bits uint8) Value {
	return takeValue(b, func() (Value, error) { return b.inner.LoadBits(address, bits) })
}

// Store64 writes eight bytes to a native address.
func (b *Builder) Store64(address, value Value) {
	do(b, func() error { return b.inner.Store64(address, value) })
}

// Store8 writes the low byte of an integer value to a native address.
func (b *Builder) Store8(address, value Value) {
	do(b, func() error { return b.inner.Store8(address, value) })
}

// Store16 writes the low two bytes of a 16- or 64-bit value.
func (b *Builder) Store16(address, value Value) {
	do(b, func() error { return b.inner.Store16(address, value) })
}

// Store32 writes the low four bytes of a 32- or 64-bit value.
func (b *Builder) Store32(address, value Value) {
	do(b, func() error { return b.inner.Store32(address, value) })
}

// StoreBits writes a width-matched integer in little-endian order to a native address.
func (b *Builder) StoreBits(address, value Value) {
	do(b, func() error { return b.inner.StoreBits(address, value) })
}

// Syscall records a raw Linux syscall for an external execution context.
func (b *Builder) Syscall(number Value, args ...Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Syscall(number, args...) })
}

// CallC records an indirect C ABI call.
func (b *Builder) CallC(target Value, args ...Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.CallC(target, args...) })
}

// CallGo records an indirect Go ABI call.
func (b *Builder) CallGo(target Value, args ...Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.CallGo(target, args...) })
}

// CallCWithEffect records a C ABI call with a supplied effect summary.
func (b *Builder) CallCWithEffect(target Value, effect CallEffect, args ...Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.CallCWithEffect(target, effect, args...) })
}

// CallGoWithEffect records a Go ABI call with a supplied effect summary.
func (b *Builder) CallGoWithEffect(target Value, effect CallEffect, args ...Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.CallGoWithEffect(target, effect, args...) })
}

// Compare compares two Values and returns a deferred predicate.
func (b *Builder) Compare(left Value, condition BranchCondition, right Value) Predicate {
	return takePredicate(b, func() (Predicate, error) { return b.inner.Compare(left, condition, right) })
}

// CompareImmediate compares a Value with an immediate and returns a predicate.
func (b *Builder) CompareImmediate(value Value, condition BranchCondition, immediate int64) Predicate {
	return takePredicate(b, func() (Predicate, error) { return b.inner.CompareImmediate(value, condition, immediate) })
}

// IsZero tests whether value is zero.
func (b *Builder) IsZero(value Value) Predicate {
	return takePredicate(b, func() (Predicate, error) { return b.inner.IsZero(value) })
}

// IsNegative tests whether value is negative at its width.
func (b *Builder) IsNegative(value Value) Predicate {
	return takePredicate(b, func() (Predicate, error) { return b.inner.IsNegative(value) })
}

// Carry tests carry from Add or borrow from Sub.
func (b *Builder) Carry(value Value) Predicate {
	return takePredicate(b, func() (Predicate, error) { return b.inner.Carry(value) })
}

// Enter opens a nested value scope.
func (b *Builder) Enter() {
	do(b, func() error { return b.inner.Enter() })
}

// Leave closes a scope and promotes the listed values to its parent.
func (b *Builder) Leave(exports ...Value) {
	do(b, func() error { return b.inner.Leave(exports...) })
}

// Bind starts the block at label.
func (b *Builder) Bind(label Label) {
	do(b, func() error { return b.inner.Bind(label) })
}

// BindParams binds label and returns count incoming values; on failure it returns count invalid Values.
func (b *Builder) BindParams(label Label, count int) []Value {
	return takeValues(b, count, func() ([]Value, error) { return b.inner.BindParams(label, count) })
}

// BindParamsBits binds label with typed incoming values; on failure it returns len(widths) invalid Values.
func (b *Builder) BindParamsBits(label Label, widths []uint8) []Value {
	return takeValues(b, len(widths), func() ([]Value, error) { return b.inner.BindParamsBits(label, widths) })
}

// Jump ends the block with an unconditional jump.
func (b *Builder) Jump(label Label) {
	do(b, func() error { return b.inner.Jump(label) })
}

// JumpArgs ends the block and passes values to label.
func (b *Builder) JumpArgs(label Label, args ...Value) {
	do(b, func() error { return b.inner.JumpArgs(label, args...) })
}

// Branch branches on a predicate; one target falls through on false, and an empty predicate jumps unconditionally.
func (b *Builder) Branch(predicate Predicate, ifTrue Label, ifFalse ...Label) {
	do(b, func() error { return b.inner.Branch(predicate, ifTrue, ifFalse...) })
}

// BranchArgs branches with values passed separately to each target.
func (b *Builder) BranchArgs(predicate Predicate, ifTrue Label, trueArgs []Value, ifFalse Label, falseArgs []Value) {
	do(b, func() error { return b.inner.BranchArgs(predicate, ifTrue, trueArgs, ifFalse, falseArgs) })
}

// Return ends the block or function with value as its result.
func (b *Builder) Return(value Value) {
	do(b, func() error { return b.inner.Return(value) })
}

// Var identifies mutable storage in a Builder scope.
type Var = builder.Var

// Var declares initialized mutable storage in the current scope entry sequence.
func (b *Builder) Var(initial Value) Var {
	if !b.ready() {
		return Var{}
	}
	v, err := b.inner.Var(initial)
	if err != nil {
		b.fail(operationError(err))
		return Var{}
	}
	return v
}

// Get returns a block-local snapshot of v's current value.
func (b *Builder) Get(v Var) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Get(v) })
}

// Set replaces v's value; x must have the same type.
func (b *Builder) Set(v Var, x Value) { do(b, func() error { return b.inner.Set(v, x) }) }
