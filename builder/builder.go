// Package builder builds target-independent scalar programs.
package builder

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"runtime"
	"strconv"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

var backendMaxArgs = abi.MaxArguments()

// NativeBits is the width of a pointer in the Go process that emits the code.
// Native-width programs are meant to run on that same architecture.
const NativeBits = strconv.IntSize

func validScalarBits[T ~uint8 | ~uint16](bits T) bool {
	return bits == 8 || bits == 16 || bits == 32 || bits == 64
}

func integerMask[T ~uint8 | ~uint16](bits T) uint64 {
	if bits == 64 {
		return ^uint64(0)
	}
	return uint64(1)<<bits - 1
}

func instructionBits(ins instruction) uint8 {
	if ins.Bits == 0 {
		return 64
	}
	return ins.Bits
}

type op = ir.Opcode
type instruction = ir.Instruction

const (
	opParam                = ir.OpParam
	opConst                = ir.OpConst
	opAdd                  = ir.OpAdd
	opSub                  = ir.OpSub
	opMul                  = ir.OpMul
	opAnd                  = ir.OpAnd
	opOr                   = ir.OpOr
	opXor                  = ir.OpXor
	opSelect               = ir.OpSelect
	opAddScaled            = ir.OpAddScaled
	opLoad64               = ir.OpLoad64
	opStore64              = ir.OpStore64
	opLoad16               = ir.OpLoad16
	opStore16              = ir.OpStore16
	opLoad32               = ir.OpLoad32
	opStore32              = ir.OpStore32
	opLoad8                = ir.OpLoad8
	opStore8               = ir.OpStore8
	opSyscall              = ir.OpSyscall
	opCallC                = ir.OpCallC
	opCallGo               = ir.OpCallGo
	opLabelParam           = ir.OpLabelParam
	opShiftRight           = ir.OpShiftRight
	opCastUnsigned         = ir.OpCastUnsigned
	opSignExtend           = ir.OpSignExtend
	opShiftLeft            = ir.OpShiftLeft
	opShiftRightArithmetic = ir.OpShiftRightArithmetic
	opRotateLeft           = ir.OpRotateLeft
	opRotateRight          = ir.OpRotateRight
)

type scopeEvent = ir.ScopeExit

// Builder creates a function over scalar values. Its default API uses uint64;
// width-specific methods support 8-, 16-, 32-, and 128-bit values. The current
// backend supports parameters, constants, integer arithmetic and bitwise
// operations, conditional selection, native-memory loads and stores, raw
// Linux syscalls and native calls for external emission, and labeled control flow.
type Builder struct {
	parameterTypes    map[int]reflect.Type
	checkedAddresses  map[int]checkedAddress
	code              []instruction
	codeBlocks        [][]storedInstruction
	codeSize          int
	instructionSlices []int
	// values has one record per instruction ID, in both modes.
	values []valueRecord
	scopes []int
	// ownedStack lists the values owned by the active nested scopes,
	// innermost last; ownedMarks[i] is where scopes[i+1]'s values start.
	// Root values never become hidden, so they are not listed.
	ownedStack      []int
	ownedMarks      []int
	scopeActive     []bool
	scopeTerminated []bool
	captureStarts   []int
	captureEnds     []int
	scopeStarts     []int
	nextScope       int
	leaves          []scopeEvent
	arity           int
	// paramEnd is one past the last OpParam.
	paramEnd    int
	returned    bool
	finished    bool
	result      int
	resultParts []int
	control     *builderControl
	// splitErr is a scope-entry split that the Builder could not make; Finish
	// and End report it.
	splitErr error
	// seg is the segment state of a Builder from NewSegmentBuilder.
	seg *segmentState
	// generation tags this Builder's Labels; see Label.
	generation uint64
	// blockSerial numbers the blocks Bind opens; the entry block is 0.
	blockSerial      int
	expressionArena  *expressionArena
	storageOwner     *Builder
	expressionWork   []Value
	expressionSeen   []*valueExpression
	expressionWalk   uint64
	validationEpoch  uint64
	dataEpoch        uint64
	variables        []ir.Variable
	variableInitials []int
	captures         map[int]map[int]int
	// leaveWork holds the parts Leave marks while it runs.
	leaveWork []int
	// predicateWork and predicateIDs are Branch's reusable predicate walk.
	predicateWork []*predicateNode
	predicateIDs  []int
	// The semantics Div, DivSigned, Rem and RemSigned, and FToSigned and
	// FToUnsigned give operations that do not name their own.
	division   ir.DivisionSemantics
	conversion ir.ConversionSemantics
}

// testSegmentHook, set only by tests, makes NewBuilder return a segment
// Builder, so that generators written against NewBuilder and Finish run in
// segment mode. Finish then calls End and returns finish's result.
var testSegmentHook func() (emit func(*ir.Segment) error, finish func(endErr error) (*ir.SealedProgram, error))

// NewBuilder creates an empty function builder.
func NewBuilder() *Builder {
	if hook := testSegmentHook; hook != nil {
		emit, finish := hook()
		b := NewSegmentBuilder(emit)
		b.seg.testFinish = finish
		return b
	}
	b := &Builder{scopes: []int{0}, scopeStarts: []int{0}, expressionArena: new(expressionArena), generation: newGeneration()}
	b.storageOwner = b
	return b
}

func (b *Builder) canAppend() bool {
	return b != nil && !b.finished && !b.returned && (b.control == nil || b.control.active) && (b.seg == nil || !b.seg.failed)
}

func (b *Builder) ensureRoot() {
	if b.storageOwner == b && len(b.scopeActive) != 0 {
		return
	}
	b.initRoot()
}

func (b *Builder) initRoot() {
	if b.storageOwner != nil && b.storageOwner != b {
		b.detachCopy()
	}
	if b.storageOwner == nil {
		b.storageOwner = b
	}
	if b.generation == 0 {
		b.generation = newGeneration()
	}
	if len(b.scopes) == 0 {
		b.scopes = []int{0}
	}
	if len(b.scopeStarts) == 0 {
		b.scopeStarts = []int{0}
	}
	if len(b.scopeActive) == 0 {
		b.scopeActive = []bool{true}
		b.scopeTerminated = []bool{false}
		b.captureStarts, b.captureEnds = []int{0}, []int{0}
	}
}

// Enter starts a nested value scope. Values from enclosing scopes remain usable.
// If the scope contains a jump or branch, Enter also terminates the preceding
// block. Bind may immediately follow Enter without an explicit Jump.
// Declare entry parameters before Enter for scopes containing control flow.
func (b *Builder) Enter() error {
	if !b.canAppend() {
		return ErrInvalidFunction
	}
	b.ensureRoot()
	start := 0
	if b.control != nil {
		start = b.control.start
	}
	b.nextScope++
	b.validationEpoch++
	b.scopeActive = append(b.scopeActive, true)
	b.scopeTerminated = append(b.scopeTerminated, false)
	b.ownedMarks = append(b.ownedMarks, len(b.ownedStack))
	if b.seg != nil {
		b.seg.enterVars = append(b.seg.enterVars, len(b.variables))
	}
	// Capture eligibility is a range at entry, evaluated lazily on first use.
	// Entry ranges are monotone along the active scope stack.
	b.captureStarts = append(b.captureStarts, start)
	b.captureEnds = append(b.captureEnds, b.codeLen())
	b.scopes = append(b.scopes, b.nextScope)
	b.scopeStarts = append(b.scopeStarts, b.codeLen())
	b.append(instruction{Op: ir.OpScopeEnter, Imm: uint64(b.nextScope)})
	return nil
}

// Leave closes the current scope. Only exported values remain usable outside it.
func (b *Builder) Leave(exports ...Value) error {
	if !b.canAppend() || len(b.scopes) <= 1 {
		return ErrInvalidFunction
	}
	var err error
	exports, err = b.materializeValues(exports)
	if err != nil {
		return err
	}
	current := b.scopes[len(b.scopes)-1]
	parent := b.scopes[len(b.scopes)-2]
	// Mark every export part, so that the owned values can be split into
	// exports and hidden values without a set. The marks are cleared on
	// every return.
	marked := b.leaveWork[:0]
	defer func() {
		for _, id := range marked {
			b.values[id].flags &^= recordExporting
		}
		b.leaveWork = marked[:0]
	}()
	local := 0
	for _, value := range exports {
		parts := value.components()
		localPart := false
		if value.composite != nil {
			if !b.validComposite(value) {
				return ErrInvalidFunction
			}
		}
		for i, part := range parts {
			if repeatedPart(parts[:i], part.id) {
				continue
			}
			if !b.valid(part) || b.values[part.id].flags&recordExporting != 0 {
				return ErrInvalidFunction
			}
			b.values[part.id].flags |= recordExporting
			marked = append(marked, part.id)
			if int(b.values[part.id].scope) == current {
				localPart = true
				local++
			} else if value.composite == nil {
				return ErrInvalidFunction
			}
		}
		if !localPart {
			return ErrInvalidFunction
		}
	}
	b.append(instruction{Op: ir.OpScopeLeave, Imm: uint64(current)})
	mark := b.ownedMarks[len(b.ownedMarks)-1]
	owned := b.ownedStack[mark:]
	ids := b.leaveInts(local)[:0]
	for _, id := range marked {
		if int(b.values[id].scope) == current {
			ids = append(ids, id)
			if b.seg != nil {
				b.seg.use(id, -1)
			}
		}
	}
	hidden := b.leaveInts(len(owned) - local)[:0]
	// Values are recorded in creation order. Promoted child values are later
	// than every value already owned by the suspended parent scope, so the
	// kept prefix stays ordered.
	kept := 0
	for _, id := range owned {
		if b.values[id].flags&recordExporting != 0 {
			b.values[id].scope = int32(parent)
			owned[kept] = id
			kept++
		} else {
			hidden = append(hidden, id)
		}
	}
	if parent == 0 {
		kept = 0
	}
	b.ownedStack = b.ownedStack[:mark+kept]
	b.ownedMarks = b.ownedMarks[:len(b.ownedMarks)-1]
	b.scopeActive[current] = false
	delete(b.captures, current)
	b.appendLeave(scopeEvent{ID: current, Parent: parent, Enter: b.scopeStarts[current], Position: b.codeLen(), Hidden: hidden, Exports: ids})
	b.scopes = b.scopes[:len(b.scopes)-1]
	b.validationEpoch++
	return nil
}

func repeatedPart(parts []Value, id int) bool {
	for _, part := range parts {
		if part.id == id {
			return true
		}
	}
	return false
}

// leaveInts returns storage for n IDs of a ScopeExit: exact-size storage
// the program keeps, or the open segment's reused slab.
func (b *Builder) leaveInts(n int) []int {
	if b.seg != nil {
		return b.seg.allocInts(n)
	}
	return make([]int, n)
}

func (b *Builder) appendLeave(exit scopeEvent) {
	if b.seg != nil {
		b.seg.leaves = append(b.seg.leaves, exit)
		return
	}
	b.leaves = append(b.leaves, exit)
}

// leaveCount is the number of ScopeExits so far.
func (b *Builder) leaveCount() int {
	if b.seg != nil {
		return b.seg.leaveBase + len(b.seg.leaves)
	}
	return len(b.leaves)
}

// append adds operation to the function. It is a small wrapper, so that the
// caller's instruction literal is passed by address.
func (b *Builder) append(operation instruction) Value { return b.appendOp(&operation) }

func (b *Builder) appendOp(operation *instruction) Value {
	b.ensureRoot()
	if b.seg != nil && b.seg.staged {
		b.flushStaged()
	}
	if operation.Op == opCallC || operation.Op == opCallGo || operation.Op == opSyscall {
		b.dataEpoch++
		b.validationEpoch++
	}
	// Only nested scopes can capture operands from an earlier block.
	if b.control != nil && len(b.scopes) > 1 && !b.captureInputs(operation) {
		return Value{}
	}

	if operation.Effect != nil {
		*operation = operation.Clone()
	}
	id := b.codeLen()
	if b.seg != nil {
		b.seg.appendInstruction(operation, id)
	} else {
		if operation.Args != nil || operation.Data != nil || operation.Effect != nil {
			b.instructionSlices = append(b.instructionSlices, id)
		}
		if b.codeBlocks == nil && len(b.code) < instructionPrefixSize {
			if len(b.code) == cap(b.code) {
				b.growInstructions()
			}
			b.code = append(b.code, *operation)
		} else {
			offset := b.codeSize - len(b.code)
			if offset%instructionBlockSize == 0 {
				b.codeBlocks = append(b.codeBlocks, make([]storedInstruction, instructionBlockSize))
				b.growValues(b.codeSize + instructionBlockSize)
			}
			b.codeBlocks[offset/instructionBlockSize][offset%instructionBlockSize] = storeInstruction(*operation)
		}
	}
	b.codeSize++

	owner := b.scopes[len(b.scopes)-1]
	b.appendRecord(operation, owner)
	// Root values never become hidden: only nested scopes need ownership lists.
	if owner != 0 && operation.Op.ProducesValue() {
		b.ownedStack = append(b.ownedStack, id)
	}
	if operation.Op == opParam {
		b.paramEnd = id + 1
	}
	// Most programs never create checked-address borrows. Skip both the
	// inheritance walk and its Instruction argument when no borrows exist.
	if len(b.checkedAddresses) != 0 {
		b.inheritCheckedAddress(id, operation)
	}
	return Value{builder: b, id: id}
}

// operand returns the type of a plain Value that an operation can use as
// it is: one of this Builder's values, in scope, needing no capture and
// borrowing no checked address. Every other Value takes the general path,
// which materializes, captures and reports errors. The caller checks
// canAppend.
func (b *Builder) operand(v Value) (ir.Type, bool) {
	if v.builder != b || v.expression != nil || v.composite != nil || uint(v.id) >= uint(b.codeSize) {
		return ir.Type{}, false
	}
	r := &b.values[v.id]
	if b.nextScope != 0 {
		if !b.scopeActive[r.scope] || b.control != nil && len(b.scopes) > 1 && v.id < b.control.start {
			return ir.Type{}, false
		}
	}
	return r.typ, len(b.checkedAddresses) == 0
}

// captureInputs replaces operands from an earlier block that a nested scope
// captures with a Get of the capture. Every operand is captured in turn, so a
// repeated operand gets a Get per use, of which the last is used. It reports
// false when a capture fails.
func (b *Builder) captureInputs(operation *instruction) bool {
	type rebase struct{ from, to int }
	var inputBuffer [4]int
	var rebaseBuffer [4]rebase
	rebased := rebaseBuffer[:0]
	for _, id := range operation.InputsInto(inputBuffer[:0]) {
		if id < b.control.start && b.captured(id) {
			value, err := b.capture(id)
			if err != nil {
				return false
			}
			rebased = append(rebased, rebase{id, value.id})
		}
	}
	if len(rebased) == 0 {
		return true
	}
	lookup := func(id int) (int, bool) {
		for i := len(rebased) - 1; i >= 0; i-- {
			if rebased[i].from == id {
				return rebased[i].to, true
			}
		}
		return 0, false
	}
	if id, ok := lookup(operation.A); ok {
		operation.A = id
	}
	if id, ok := lookup(operation.B); ok {
		operation.B = id
	}
	if id, ok := lookup(operation.C); ok {
		operation.C = id
	}
	for i, id := range operation.Args {
		if replacement, ok := lookup(id); ok {
			operation.Args[i] = replacement
		}
	}
	return true
}

// Param adds a uint64 parameter and returns its SSA value.
func (b *Builder) Param() (Value, error) {
	return b.ParamBits(64)
}

// ParamNative adds a pointer-width unsigned integer parameter.
func (b *Builder) ParamNative() (Value, error) { return b.ParamBits(NativeBits) }

// ParamBits adds an integer parameter. A 128-bit parameter consumes two
// uint64 argument slots, low word first; narrower values use one slot.
func (b *Builder) ParamBits(bits uint8) (Value, error) {
	if bits == 128 {
		if !b.canAppend() || b.control != nil && b.control.hasTerm {
			return Value{}, ErrInvalidFunction
		}
		if b.arity > backendMaxArgs-2 {
			return Value{}, newLimitError("arguments", b.arity+2, backendMaxArgs, "entry parameters", "ParamBits 128: entry needs %d argument slots, limit %d", b.arity+2, backendMaxArgs)
		}
		low, err := b.ParamBits(64)
		if err != nil {
			return Value{}, err
		}
		high, err := b.ParamBits(64)
		if err != nil {
			return Value{}, err
		}
		return compositeValue(low, high), nil
	}
	if !validScalarBits(bits) || !b.canAppend() || (b.control != nil && b.control.hasTerm) {
		return Value{}, fmt.Errorf("ParamBits %d: width is unsupported or entry parameter list is closed: %w", bits, ErrInvalidFunction)
	}
	if b.arity == backendMaxArgs {
		return Value{}, newLimitError("arguments", b.arity+1, backendMaxArgs, "entry parameters", "ParamBits %d: entry needs %d argument slots, limit %d", bits, b.arity+1, backendMaxArgs)
	}
	b.arity++
	return b.append(instruction{Op: opParam, Bits: bits}), nil
}

// Const adds a uint64 constant and returns its SSA value.
func (b *Builder) Const(value uint64) (Value, error) {
	return b.ConstBits(64, value)
}

// ConstNative creates a pointer-width unsigned integer constant.
func (b *Builder) ConstNative(value uintptr) (Value, error) {
	return b.ConstBits(NativeBits, uint64(value))
}

// ConstBits creates a width-limited integer constant. For 128 bits, value is
// the low word and the remaining bits are zero. Use ConstBytes for a full-width literal.
func (b *Builder) ConstBits(bits uint8, value uint64) (Value, error) {
	if bits == 128 {
		var data [16]byte
		binary.LittleEndian.PutUint64(data[:8], value)
		return b.ConstBytes(128, data[:])
	}
	if !validScalarBits(bits) || !b.canAppend() {
		return Value{}, ErrInvalidFunction
	}
	return b.appendScalar(opConst, bits, 0, 0, value&integerMask(bits), 0), nil
}

// ConstBytes creates an integer of the requested width from little-endian
// bytes. The input may be shorter than the width and is zero-extended.
func (b *Builder) ConstBytes(bits uint8, data []byte) (Value, error) {
	if !b.canAppend() || bits != 8 && bits != 16 && bits != 32 && bits != 64 && bits != 128 || len(data) > int(bits)/8 {
		return Value{}, ErrInvalidFunction
	}
	if bits <= 64 {
		var word [8]byte
		copy(word[:], data)
		return b.ConstBits(bits, binary.LittleEndian.Uint64(word[:]))
	}
	var bytes [16]byte
	copy(bytes[:], data)
	low, err := b.Const(binary.LittleEndian.Uint64(bytes[:8]))
	if err != nil {
		return Value{}, err
	}
	high, err := b.Const(binary.LittleEndian.Uint64(bytes[8:]))
	if err != nil {
		return Value{}, err
	}
	return compositeValue(low, high), nil
}

// Symbol creates a scalar constant whose value is supplied when compiling a
// bound Builder. It is suitable for native data and callback addresses.
func (b *Builder) Symbol(name string) (Value, error) {
	if !b.canAppend() || name == "" {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: opConst, Symbol: name}), nil
}

// Add adds two uint64 values, using modulo-2^64 arithmetic.
func (b *Builder) Add(x, y Value) (Value, error) {
	return b.binary(opAdd, x, y)
}

// Sub subtracts y from x, using modulo-2^64 arithmetic.
func (b *Builder) Sub(x, y Value) (Value, error) {
	return b.binary(opSub, x, y)
}

// ShiftRight shifts an unsigned scalar right. Counts at least its width yield zero.
func (b *Builder) ShiftRight(value Value, bits uint8) (Value, error) {
	return b.shiftImmediate(opShiftRight, value, bits)
}

// ShiftLeft shifts a scalar left, discarding bits beyond its width.
func (b *Builder) ShiftLeft(value Value, bits uint8) (Value, error) {
	return b.shiftImmediate(opShiftLeft, value, bits)
}

// ShiftRightArithmetic shifts right, replicating the value's sign bit.
func (b *Builder) ShiftRightArithmetic(value Value, bits uint8) (Value, error) {
	return b.shiftImmediate(opShiftRightArithmetic, value, bits)
}

// RotateLeft rotates a scalar left. Counts are reduced modulo its width.
func (b *Builder) RotateLeft(value Value, bits uint8) (Value, error) {
	return b.shiftImmediate(opRotateLeft, value, bits)
}

// RotateRight rotates a scalar right. Counts are reduced modulo its width.
func (b *Builder) RotateRight(value Value, bits uint8) (Value, error) {
	return b.shiftImmediate(opRotateRight, value, bits)
}

func (b *Builder) shiftImmediate(operation op, value Value, count uint8) (Value, error) {
	if b.canAppend() {
		if t, ok := b.operand(value); ok && t.Kind == ir.ScalarKind && t.LaneBits <= 64 && count < t.LaneBits {
			return b.appendScalar(operation, t.LaneBits, value.id, 0, uint64(count), 1), nil
		}
	}
	if value.expression != nil {
		if !b.validExpression(value) {
			return Value{}, ErrInvalidFunction
		}
		return value.expressionShift(operation, count), nil
	}
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Value{}, err
	}
	if value.Type().Kind == ir.VectorKind {
		if !b.valid(value) {
			return Value{}, ErrInvalidFunction
		}
		return b.append(instruction{Op: operation, Type: value.Type(), Bits: value.Type().LaneBits, A: value.id, Imm: uint64(count)}), nil
	}
	if value.composite != nil {
		if !b.validComposite(value) {
			return Value{}, ErrInvalidFunction
		}
		return shiftWideImmediate(b, operation, value, count)
	}
	if !b.valid(value) {
		return Value{}, ErrInvalidFunction
	}
	if operation == opRotateLeft || operation == opRotateRight {
		count %= value.Type().LaneBits
	} else if count >= value.Type().LaneBits {
		if operation == opShiftRightArithmetic {
			count = value.Type().LaneBits - 1
		} else {
			return b.ConstBits(value.Type().LaneBits, 0)
		}
	}
	return b.append(instruction{Op: operation, A: value.id, Imm: uint64(count), Bits: value.Type().LaneBits}), nil
}

// CastUnsigned truncates or zero-extends an integer to the requested width.
func (b *Builder) CastUnsigned(value Value, bits uint8) (Value, error) {
	if err := requireScalars("CastUnsigned", value); err != nil {
		return Value{}, err
	}
	if value.expression != nil {
		if !b.validExpression(value) || !validScalarBits(bits) && bits != 128 {
			return Value{}, ErrInvalidFunction
		}
		return value.CastUnsigned(bits), nil
	}
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Value{}, err
	}
	if value.composite != nil {
		if !b.validComposite(value) || bits != 8 && bits != 16 && bits != 32 && bits != 64 && bits != 128 {
			return Value{}, ErrInvalidFunction
		}
		if bits == 128 {
			return value, nil
		}
		lo := wideLow(value)
		return b.CastUnsigned(lo, bits)
	}
	if bits == 128 && b.valid(value) {
		lo, err := b.CastUnsigned(value, 64)
		if err != nil {
			return Value{}, err
		}
		hi, err := b.Const(0)
		if err != nil {
			return Value{}, err
		}
		return b.Concat(lo, hi)
	}
	if !validScalarBits(bits) || !b.valid(value) {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: opCastUnsigned, A: value.id, Bits: bits}), nil
}

// CastNative converts a value to the pointer width of the emitting process.
func (b *Builder) CastNative(value Value) (Value, error) {
	return b.CastUnsigned(value, NativeBits)
}

// Mul multiplies x by y modulo their common integer width.
func (b *Builder) Mul(x, y Value) (Value, error) {
	return b.binary(opMul, x, y)
}

// addScaled is the canonical arithmetic form recognized by instruction
// selection after a general multiply-and-add expression is materialized.
func (b *Builder) addScaled(base, index Value, scale uint64) (Value, error) {
	var err error
	base, err = b.materialize(base)
	if err != nil {
		return Value{}, err
	}
	index, err = b.materialize(index)
	if err != nil {
		return Value{}, err
	}
	if b == nil || b.returned || !b.valid(base) || !b.valid(index) ||
		base.Type().LaneBits != index.Type().LaneBits || base.Type().LaneBits != 64 && base.Type().LaneBits != NativeBits {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: opAddScaled, A: base.id, B: index.id, Imm: scale, Bits: base.Type().LaneBits}), nil
}

// Load64 reads a uint64 from a native address. The caller must supply a
// valid address with the target's required alignment and keep any Go-owned
// memory alive throughout the access. Loads remain ordered with stores in
// the builder's instruction sequence.
func (b *Builder) Load64(address Value) (Value, error) {
	return b.load64Order(address, ir.ByteOrderNative)
}

func (b *Builder) load64Order(address Value, order ir.ByteOrder) (Value, error) {
	if err := requireScalars("Load64", address); err != nil {
		return Value{}, err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if b == nil || b.returned || !b.valid(address) || address.Type().LaneBits != 64 && address.Type().LaneBits != NativeBits {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: opLoad64, A: address.id, ByteOrder: order}), nil
}

// Store64 writes a uint64 to a valid, writable native address with the
// target's required alignment. Keep any Go-owned memory alive throughout
// the access. This emits no write barrier and must not store Go pointers.
func (b *Builder) Store64(address, value Value) error {
	return b.store64Order(address, value, ir.ByteOrderNative)
}

func (b *Builder) store64Order(address, value Value, order ir.ByteOrder) error {
	if err := requireScalars("Store64", address, value); err != nil {
		return err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return err
	}
	value, err = b.materialize(value)
	if err != nil {
		return err
	}
	if b == nil || b.returned || !b.valid(address) || !b.valid(value) ||
		address.Type().LaneBits != 64 && address.Type().LaneBits != NativeBits || value.Type().LaneBits != 64 {
		return ErrInvalidFunction
	}
	b.append(instruction{Op: opStore64, A: address.id, B: value.id, ByteOrder: order})
	return nil
}

// Load8 reads one byte from a native address and zero-extends it to uint64.
func (b *Builder) Load8(address Value) (Value, error) {
	if err := requireScalars("Load8", address); err != nil {
		return Value{}, err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if b == nil || b.returned || !b.valid(address) || address.Type().LaneBits != 64 && address.Type().LaneBits != NativeBits {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: opLoad8, A: address.id}), nil
}

// Store8 writes the low byte of an 8-, 16-, 32-, or 64-bit value to a native
// address. The address must remain valid for the write.
func (b *Builder) Store8(address, value Value) error {
	if err := requireScalars("Store8", address, value); err != nil {
		return err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return err
	}
	value, err = b.materialize(value)
	if err != nil {
		return err
	}
	if b == nil || b.returned || !b.valid(address) || !b.valid(value) ||
		address.Type().LaneBits != 64 && address.Type().LaneBits != NativeBits || !validScalarBits(value.Type().LaneBits) {
		return fmt.Errorf("Store8: address value %d has width %d (want native width), data value %d has width %d (want 8, 16, 32, or 64): %w",
			address.id, address.Type().LaneBits, value.id, value.Type().LaneBits, ErrInvalidFunction)
	}
	b.append(instruction{Op: opStore8, A: address.id, B: value.id, Bits: 8})
	return nil
}

// Syscall emits a raw Linux system call and returns its uint64 result.
// It accepts up to six arguments. Raw syscalls are available through Emit;
// Code.Call does not provide the runtime transition required to invoke them.
func (b *Builder) Syscall(number Value, args ...Value) (Value, error) {
	if err := requireScalars("Syscall", append([]Value{number}, args...)...); err != nil {
		return Value{}, err
	}
	var err error
	number, err = b.materialize(number)
	if err != nil {
		return Value{}, err
	}
	args, err = b.materializeValues(args)
	if err != nil {
		return Value{}, err
	}
	if b == nil || b.returned || !b.valid(number) || number.Type().LaneBits != 64 {
		return Value{}, ErrInvalidFunction
	}
	if len(args) > 6 {
		return Value{}, newLimitError("arguments", len(args), 6, "system call", "system call has %d arguments, limit 6", len(args))
	}
	ids := make([]int, 1, len(args)+1)
	ids[0] = number.id
	for _, arg := range args {
		if !b.valid(arg) || arg.Type().LaneBits != 64 {
			return Value{}, ErrInvalidFunction
		}
		ids = append(ids, arg.id)
	}
	return b.append(instruction{Op: opSyscall, Args: ids}), nil
}

// CallC emits an indirect call through a native function address using the
// platform C integer ABI. Arguments may have scalar widths up to 64 bits.
// Values of 8 or 16 bits are zero-extended; use SignExtend first for signed char/short.
// Integer arguments are extended according to the target psABI: loong64 and
// riscv64 sign-extend 32-bit arguments to 64 bits, including uint32 values.
// Pass a 64-bit IR value (e.g. CastUnsigned(x, 64)) for a 64-bit C parameter.
// On 386, values up to 32 bits take one stack word and 64-bit values take two.
// It accepts the target's argument limit and
// returns one uint64. CompileForeign permits in-process calls to C ABI
// callbacks while executing on the runtime's foreign-call stack.
func (b *Builder) CallC(target Value, args ...Value) (Value, error) {
	return b.call(target, opCallC, nil, args...)
}

// CallGo emits a Go ABI call with scalar arguments up to 64 bits and one
// uint64 result. The target must be a code pointer to a compatible Go function;
// closures, interface methods, and values containing Go pointers are outside
// this scalar contract. PrepareGo can register and execute a pointer-free
// frame on supported Go runtimes. On 386 this uses word-sized Go ABI slots:
// adjacent 8-/16-bit arguments are unsupported because Go ABI0 packs them.
// Use 32-bit arguments and callee parameters for that shape.
// Callable compilation requires a fixed Go function constant or symbol binding.
// Dynamic targets and generated entries are rejected; raw emission is trusted.
func (b *Builder) CallGo(target Value, args ...Value) (Value, error) {
	return b.call(target, opCallGo, nil, args...)
}

// CallCWithEffect emits an indirect C ABI call with a known callee effect.
// Argument extension follows CallC's target psABI width rules.
// The effect may narrow the ABI's conservative clobber and memory sets.
// The caller is responsible for including effects of every transitive call.
func (b *Builder) CallCWithEffect(target Value, effect abi.CallEffect, args ...Value) (Value, error) {
	copyEffect := effect.Clone()
	return b.call(target, opCallC, &copyEffect, args...)
}

// CallGoWithEffect supplies a callee-specific Go ABI effect. The effect must
// include every transitive call's register and memory effects.
func (b *Builder) CallGoWithEffect(target Value, effect abi.CallEffect, args ...Value) (Value, error) {
	copyEffect := effect.Clone()
	return b.call(target, opCallGo, &copyEffect, args...)
}

func (b *Builder) call(target Value, operation op, effect *abi.CallEffect, args ...Value) (Value, error) {
	if err := requireScalars("Call", append([]Value{target}, args...)...); err != nil {
		return Value{}, err
	}
	var err error
	target, err = b.materialize(target)
	if err != nil {
		return Value{}, err
	}
	args, err = b.materializeValues(args)
	if err != nil {
		return Value{}, err
	}
	if b == nil || b.returned || !b.valid(target) || target.Type().LaneBits != NativeBits {
		return Value{}, ErrInvalidFunction
	}
	limit := backendMaxArgs
	if operation == opCallGo {
		limit = abi.MaxGoCallArguments()
	}
	if len(args) > limit {
		return Value{}, newLimitError("arguments", len(args), limit, "call", "call has %d arguments, limit %d", len(args), limit)
	}
	ids := make([]int, 1, len(args)+1)
	ids[0] = target.id
	for _, arg := range args {
		if !b.valid(arg) || arg.Type().LaneBits > 64 {
			return Value{}, ErrInvalidFunction
		}
		ids = append(ids, arg.id)
	}
	if operation == opCallGo && runtime.GOARCH == "386" {
		for i := 1; i < len(args); i++ {
			left, right := args[i-1].Type().LaneBits, args[i].Type().LaneBits
			if left < 32 && right < 32 {
				return Value{}, fmt.Errorf("386 CallGo arguments %d (%d bits) and %d (%d bits) require packed Go ABI0 subword slots; widen the arguments and callee parameters to 32 bits: %w", i-1, left, i, right, ErrBackendLimit)
			}
		}
	}
	return b.append(instruction{Op: operation, Args: ids, Effect: effect}), nil
}

// And returns the bitwise AND of x and y.
func (b *Builder) And(x, y Value) (Value, error) {
	return b.binary(opAnd, x, y)
}

// Or returns the bitwise OR of x and y.
func (b *Builder) Or(x, y Value) (Value, error) {
	return b.binary(opOr, x, y)
}

// Xor returns the bitwise XOR of x and y.
func (b *Builder) Xor(x, y Value) (Value, error) {
	return b.binary(opXor, x, y)
}

// Select returns ifTrue when condition is nonzero and ifFalse otherwise.
func (b *Builder) Select(condition, ifTrue, ifFalse Value) (Value, error) {
	if err := requireScalars("Select", condition, ifTrue, ifFalse); err != nil {
		return Value{}, err
	}
	if condition.expression != nil || ifTrue.expression != nil || ifFalse.expression != nil {
		if !b.validExpression(condition) || !b.validExpression(ifTrue) || !b.validExpression(ifFalse) ||
			ifTrue.Type().LaneBits != ifFalse.Type().LaneBits {
			return Value{}, ErrInvalidFunction
		}
		return condition.Select(ifTrue, ifFalse), nil
	}
	var err error
	condition, err = b.materialize(condition)
	if err != nil {
		return Value{}, err
	}
	ifTrue, err = b.materialize(ifTrue)
	if err != nil {
		return Value{}, err
	}
	ifFalse, err = b.materialize(ifFalse)
	if err != nil {
		return Value{}, err
	}
	if ifTrue.composite != nil || ifFalse.composite != nil {
		return selectWide(b, condition, ifTrue, ifFalse)
	}
	if b == nil || b.returned || !b.valid(condition) || !b.valid(ifTrue) || !b.valid(ifFalse) || ifTrue.Type().LaneBits != ifFalse.Type().LaneBits {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: opSelect, A: condition.id, B: ifTrue.id, C: ifFalse.id, Bits: ifTrue.Type().LaneBits}), nil
}

func (b *Builder) binary(operation op, x, y Value) (Value, error) {
	if b.canAppend() {
		tx, okx := b.operand(x)
		ty, oky := b.operand(y)
		if okx && oky && tx == ty && tx.Kind == ir.ScalarKind && tx.LaneBits <= 64 {
			return b.appendScalar(operation, tx.LaneBits, x.id, y.id, 0, 2), nil
		}
	}
	if x.expression != nil || y.expression != nil {
		if !b.validExpression(x) || !b.validExpression(y) {
			return Value{}, b.invalidOperands(x, y)
		}
		if x.Type() != y.Type() {
			return Value{}, fmt.Errorf("operands have widths %d and %d bits (values %d and %d): %w", x.Type().LaneBits, y.Type().LaneBits, x.id, y.id, ErrInvalidFunction)
		}
		return expressionBinary(operation, x, y), nil
	}
	var err error
	x, err = b.materialize(x)
	if err != nil {
		return Value{}, err
	}
	y, err = b.materialize(y)
	if err != nil {
		return Value{}, err
	}
	if x.Type().Kind == ir.VectorKind || y.Type().Kind == ir.VectorKind {
		return b.vectorBinary(operation, x, y)
	}
	if x.composite != nil || y.composite != nil {
		switch operation {
		case opAdd:
			return addWide(b, x, y)
		case opSub:
			return subWide(b, x, y)
		case opMul:
			return mulWide(b, x, y)
		case opAnd:
			return andWide(b, x, y)
		case opOr:
			return orWide(b, x, y)
		case opXor:
			return xorWide(b, x, y)
		}
		return Value{}, ErrInvalidFunction
	}
	if b == nil || b.returned || !b.valid(x) || !b.valid(y) {
		return Value{}, b.invalidOperands(x, y)
	}
	if x.Type() != y.Type() {
		return Value{}, fmt.Errorf("operands have widths %d and %d bits (values %d and %d): %w", x.Type().LaneBits, y.Type().LaneBits, x.id, y.id, ErrInvalidFunction)
	}
	return b.append(instruction{Op: operation, A: x.id, B: y.id, Bits: x.Type().LaneBits}), nil
}

// Return marks a scalar or 128-bit value as the function result.
func (b *Builder) Return(value Value) error {
	if b != nil && b.returned {
		return fmt.Errorf("Return: function already has a return: %w", ErrInvalidFunction)
	}
	if !b.canAppend() || len(b.scopes) != 1 {
		return fmt.Errorf("Return: current block is closed or nested scope is still open: %w", ErrInvalidFunction)
	}
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return err
	}
	if value.Type().Kind != ir.ScalarKind {
		return fmt.Errorf("Return: vectors and masks use memory/scalar extraction at the ABI boundary: %w", ErrInvalidFunction)
	}
	if value.composite != nil {
		if value.Type().LaneBits != 128 || len(value.composite.parts) != 2 ||
			!b.valid(value.composite.parts[0]) || !b.valid(value.composite.parts[1]) ||
			value.composite.parts[0].Type().LaneBits != 64 || value.composite.parts[1].Type().LaneBits != 64 {
			return ErrInvalidFunction
		}
		parts := []int{value.composite.parts[0].id, value.composite.parts[1].id}
		if b.seg != nil {
			return b.segmentReturn(parts[0], parts)
		}
		if b.control != nil {
			b.endControl(controlEnd{term: builderReturn, result: parts[0], resultParts: parts, to: -1, other: -1})
		} else {
			b.returned = true
			b.result = parts[0]
			b.resultParts = parts
		}
		return nil
	}
	if !b.valid(value) {
		return b.invalidValue(value)
	}
	if b.seg != nil {
		return b.segmentReturn(value.id, nil)
	}
	if b.control != nil {
		b.endControl(controlEnd{term: builderReturn, result: value.id, to: -1, other: -1})
		return nil
	}
	b.returned = true
	b.result = value.id
	return nil
}

func (b *Builder) invalidValue(value Value) error {
descend:
	for {
		if b != nil && value.builder == b && value.expression != nil {
			for _, part := range []Value{value.expression.left, value.expression.right, value.expression.third} {
				if part.builder != nil && !b.validExpression(part) {
					value = part
					continue descend
				}
			}
		}
		if b != nil && value.builder == b && value.composite != nil {
			for _, part := range value.composite.parts {
				if !b.validExpression(part) {
					value = part
					continue descend
				}
			}
		}
		break
	}

	switch {
	case b == nil:
		return fmt.Errorf("value %d: builder is nil: %w", value.id, ErrInvalidFunction)
	case value.builder != b:
		return fmt.Errorf("value %d: belongs to another builder: %w", value.id, ErrInvalidFunction)
	case value.id < 0 || value.id >= b.codeLen():
		return fmt.Errorf("value %d: is invalid: %w", value.id, ErrInvalidFunction)
	default:
		return fmt.Errorf("%w: %w", ErrInvalidFunction, &ir.ValidationError{Index: -1, Rule: fmt.Sprintf("value %d is used after its scope's Leave", value.id)})
	}
}

func (b *Builder) invalidOperands(x, y Value) error {
	if !b.validExpression(x) {
		return b.invalidValue(x)
	}
	return b.invalidValue(y)
}

func (b *Builder) valid(value Value) bool {
	if b == nil || !b.canAppend() {
		return false
	}
	if value.composite != nil {
		return false
	}
	if value.builder != b || value.id < 0 || value.id >= b.codeLen() {
		return false
	}
	if len(b.checkedAddresses) != 0 {
		if borrow, ok := b.checkedAddresses[value.id]; ok && (borrow.block != b.currentBlock() || borrow.epoch != b.dataEpoch) {
			return false
		}
	}
	// With no nested scopes, every current-block value is root-owned.
	if b.nextScope == 0 {
		return true
	}
	owner := int(b.values[value.id].scope)
	return owner >= 0 && owner < len(b.scopeActive) && b.scopeActive[owner]
}

func (b *Builder) validComposite(value Value) bool {
	if value.composite == nil || !value.Type().Valid() {
		return false
	}
	for _, part := range value.composite.parts {
		if !b.valid(part) {
			return false
		}
	}
	return true
}

func wideLow(value Value) Value  { return value.composite.parts[0] }
func wideHigh(value Value) Value { return value.composite.parts[1] }

type wideBuilder struct {
	b   *Builder
	err error
}

func (w *wideBuilder) take(value Value, err error) Value {
	if w.err == nil {
		w.err = err
	}
	return value
}
func (w *wideBuilder) constant(value uint64) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.Const(value))
}
func (w *wideBuilder) add(a, b Value) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.Add(a, b))
}
func (w *wideBuilder) sub(a, b Value) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.Sub(a, b))
}
func (w *wideBuilder) mul(a, b Value) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.Mul(a, b))
}
func (w *wideBuilder) and(a, b Value) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.And(a, b))
}
func (w *wideBuilder) or(a, b Value) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.Or(a, b))
}
func (w *wideBuilder) xor(a, b Value) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.Xor(a, b))
}
func (w *wideBuilder) shr(a Value, count uint8) Value {
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.ShiftRight(a, count))
}
func (w *wideBuilder) shl(a Value, count uint8) Value {
	if count == 0 {
		return a
	}
	if w.err != nil {
		return Value{}
	}
	return w.take(w.b.ShiftLeft(a, count))
}
func (w *wideBuilder) finish(lo, hi Value) (Value, error) {
	if w.err != nil {
		return Value{}, w.err
	}
	return compositeValue(lo, hi), nil
}

// addWide adds two 128-bit integers modulo 2^128.
func addWide(b *Builder, x, y Value) (Value, error) {
	if !b.validComposite(x) || !b.validComposite(y) {
		return Value{}, ErrInvalidFunction
	}
	w := &wideBuilder{b: b}
	lo := w.add(wideLow(x), wideLow(y))
	carry := b.append(instruction{Op: ir.OpCarryAdd, Bits: 64, A: wideLow(x).id, B: wideLow(y).id})
	hi := b.append(instruction{Op: ir.OpAddWithCarry, Bits: 64, A: wideHigh(x).id, B: wideHigh(y).id, C: carry.id})
	return w.finish(lo, hi)
}

// subWide subtracts two 128-bit integers modulo 2^128.
func subWide(b *Builder, x, y Value) (Value, error) {
	if !b.validComposite(x) || !b.validComposite(y) {
		return Value{}, ErrInvalidFunction
	}
	w := &wideBuilder{b: b}
	lo := w.sub(wideLow(x), wideLow(y))
	borrow := b.append(instruction{Op: ir.OpBorrowSub, Bits: 64, A: wideLow(x).id, B: wideLow(y).id})
	hi := b.append(instruction{Op: ir.OpSubWithBorrow, Bits: 64, A: wideHigh(x).id, B: wideHigh(y).id, C: borrow.id})
	return w.finish(lo, hi)
}

// mulWide multiplies two 128-bit integers modulo 2^128.
func mulWide(b *Builder, x, y Value) (Value, error) {
	if !b.validComposite(x) || !b.validComposite(y) {
		return Value{}, ErrInvalidFunction
	}
	w := &wideBuilder{b: b}
	lo := w.mul(wideLow(x), wideLow(y))
	hi := b.append(instruction{Op: ir.OpMulHigh, Bits: 64, A: wideLow(x).id, B: wideLow(y).id})
	hi = w.add(w.add(hi, w.mul(wideHigh(x), wideLow(y))), w.mul(wideLow(x), wideHigh(y)))
	return w.finish(lo, hi)
}

// andWide returns the bitwise conjunction of two 128-bit values.
func andWide(b *Builder, x, y Value) (Value, error) {
	if !b.validComposite(x) || !b.validComposite(y) {
		return Value{}, ErrInvalidFunction
	}
	w := &wideBuilder{b: b}
	return w.finish(w.and(wideLow(x), wideLow(y)), w.and(wideHigh(x), wideHigh(y)))
}

// orWide returns the bitwise disjunction of two 128-bit values.
func orWide(b *Builder, x, y Value) (Value, error) {
	if !b.validComposite(x) || !b.validComposite(y) {
		return Value{}, ErrInvalidFunction
	}
	w := &wideBuilder{b: b}
	return w.finish(w.or(wideLow(x), wideLow(y)), w.or(wideHigh(x), wideHigh(y)))
}

// xorWide returns the bitwise exclusive disjunction of two 128-bit values.
func xorWide(b *Builder, x, y Value) (Value, error) {
	if !b.validComposite(x) || !b.validComposite(y) {
		return Value{}, ErrInvalidFunction
	}
	w := &wideBuilder{b: b}
	return w.finish(w.xor(wideLow(x), wideLow(y)), w.xor(wideHigh(x), wideHigh(y)))
}

// shiftRightWide shifts a 128-bit unsigned integer right. Counts at least 128
// produce zero.
func shiftRightWide(b *Builder, value Value, count uint8) (Value, error) {
	if !b.validComposite(value) {
		return Value{}, ErrInvalidFunction
	}
	if count >= 128 {
		return b.ConstBits(128, 0)
	}
	if count == 0 {
		return value, nil
	}
	w := &wideBuilder{b: b}
	zero := w.constant(0)
	if count >= 64 {
		return w.finish(w.shr(wideHigh(value), count-64), zero)
	}
	lo := w.or(w.shr(wideLow(value), count), w.shl(wideHigh(value), 64-count))
	hi := w.shr(wideHigh(value), count)
	return w.finish(lo, hi)
}

// shiftLeftWide shifts a 128-bit integer left. Counts at least 128 produce zero.
func shiftLeftWide(b *Builder, value Value, count uint8) (Value, error) {
	if !b.validComposite(value) {
		return Value{}, ErrInvalidFunction
	}
	if count >= 128 {
		return b.ConstBits(128, 0)
	}
	if count == 0 {
		return value, nil
	}
	w := &wideBuilder{b: b}
	zero := w.constant(0)
	if count >= 64 {
		return w.finish(zero, w.shl(wideLow(value), count-64))
	}
	lo := w.shl(wideLow(value), count)
	hi := w.or(w.shl(wideHigh(value), count), w.shr(wideLow(value), 64-count))
	return w.finish(lo, hi)
}

// shiftRightArithmeticWide shifts right with sign fill from bit 127. Counts at
// least 128 produce two words of sign bits.
func shiftRightArithmeticWide(b *Builder, value Value, count uint8) (Value, error) {
	if !b.validComposite(value) {
		return Value{}, ErrInvalidFunction
	}
	if count == 0 {
		return value, nil
	}
	w := &wideBuilder{b: b}
	sign := w.take(b.ShiftRightArithmetic(wideHigh(value), 63))
	if count >= 128 {
		return w.finish(sign, sign)
	}
	if count >= 64 {
		return w.finish(w.take(b.ShiftRightArithmetic(wideHigh(value), count-64)), sign)
	}
	lo := w.or(w.shr(wideLow(value), count), w.shl(wideHigh(value), 64-count))
	hi := w.take(b.ShiftRightArithmetic(wideHigh(value), count))
	return w.finish(lo, hi)
}

// rotateLeftWide rotates a 128-bit scalar left by count modulo 128.
func rotateLeftWide(b *Builder, value Value, count uint8) (Value, error) {
	if !b.validComposite(value) {
		return Value{}, ErrInvalidFunction
	}
	count %= 128
	if count == 0 {
		return value, nil
	}
	left, err := shiftLeftWide(b, value, count)
	if err != nil {
		return Value{}, err
	}
	right, err := shiftRightWide(b, value, 128-count)
	if err != nil {
		return Value{}, err
	}
	return orWide(b, left, right)
}

// rotateRightWide rotates a 128-bit scalar right by count modulo 128.
func rotateRightWide(b *Builder, value Value, count uint8) (Value, error) {
	if !b.validComposite(value) {
		return Value{}, ErrInvalidFunction
	}
	count %= 128
	if count == 0 {
		return value, nil
	}
	right, err := shiftRightWide(b, value, count)
	if err != nil {
		return Value{}, err
	}
	left, err := shiftLeftWide(b, value, 128-count)
	if err != nil {
		return Value{}, err
	}
	return orWide(b, left, right)
}

// compareWide compares 128-bit scalar integers. Signed conditions interpret
// the high word as signed; unsigned conditions compare both words unsigned.
func compareWide(b *Builder, x Value, condition ir.BranchCondition, y Value) (Predicate, error) {
	if !b.validComposite(x) || !b.validComposite(y) || condition > ir.BranchAbove {
		return Predicate{}, ErrInvalidFunction
	}
	hiEqual, err := b.Compare(wideHigh(x), ir.BranchEqual, wideHigh(y))
	if err != nil {
		return Predicate{}, err
	}
	loEqual, err := b.Compare(wideLow(x), ir.BranchEqual, wideLow(y))
	if err != nil {
		return Predicate{}, err
	}
	equal := hiEqual.And(loEqual)
	if condition == ir.BranchEqual {
		return equal, nil
	}
	if condition == ir.BranchNotEqual {
		return equal.Not(), nil
	}
	hiCondition := ir.BranchBelow
	if signedCondition(condition) {
		hiCondition = ir.BranchLess
	}
	hiLess, err := b.Compare(wideHigh(x), hiCondition, wideHigh(y))
	if err != nil {
		return Predicate{}, err
	}
	loLess, err := b.Compare(wideLow(x), ir.BranchBelow, wideLow(y))
	if err != nil {
		return Predicate{}, err
	}
	less := hiLess.Or(hiEqual.And(loLess))
	switch condition {
	case ir.BranchLess, ir.BranchBelow:
		return less, nil
	case ir.BranchGreaterEqual, ir.BranchAboveEqual:
		return less.Not(), nil
	case ir.BranchLessEqual, ir.BranchBelowEqual:
		return less.Or(equal), nil
	case ir.BranchGreater, ir.BranchAbove:
		return less.Or(equal).Not(), nil
	default:
		return Predicate{}, ErrInvalidFunction
	}
}

// loadWide reads a little-endian 128-bit scalar from native memory.
func loadWide(b *Builder, address Value) (Value, error) {
	if !b.valid(address) || address.Type().LaneBits != 64 {
		return Value{}, ErrInvalidFunction
	}
	lo, err := b.load64Order(address, ir.ByteOrderLittleEndian)
	if err != nil {
		return Value{}, err
	}
	eight, err := b.Const(8)
	if err != nil {
		return Value{}, err
	}
	next, err := b.Add(address, eight)
	if err != nil {
		return Value{}, err
	}
	hi, err := b.load64Order(next, ir.ByteOrderLittleEndian)
	if err != nil {
		return Value{}, err
	}
	return compositeValue(lo, hi), nil
}

// storeWide writes a little-endian 128-bit scalar to native memory.
func storeWide(b *Builder, address Value, value Value) error {
	if !b.valid(address) || address.Type().LaneBits != 64 || !b.validComposite(value) {
		return ErrInvalidFunction
	}
	if err := b.store64Order(address, wideLow(value), ir.ByteOrderLittleEndian); err != nil {
		return err
	}
	eight, err := b.Const(8)
	if err != nil {
		return err
	}
	next, err := b.Add(address, eight)
	if err != nil {
		return err
	}
	return b.store64Order(next, wideHigh(value), ir.ByteOrderLittleEndian)
}
