package builder

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"reflect"
	"strconv"
)

// Data views a scalar, struct, array or slice in host Go layout. Root parameters
// and their At views use an address Var, so they work across blocks. Index views
// cannot cross a block boundary: take a fresh Index in the destination block.
// Index emits its own bounds branch. Indexed views expire at calls/syscalls;
// obtain a fresh Index afterwards. Raw saved addresses obey host lifetime rules.
// Reads are emitted at use without caching; keep snapshots in your own Vars.
// A Data and its Builder must not be used concurrently.
type Data struct {
	builder    *Builder
	root       Var
	local      Value
	blockStart int
	indexed    bool
	epoch      uint64
	rooted     bool
	layout     *dataLayout
	offset     uintptr
	writable   bool
}

// ValueParam adds one native-width parameter addressing a read-only boxed
// struct or array supplied by execution.Words. T must consist of integers,
// bools, floats, structs, arrays and slices of those types. Slice backing arrays
// remain writable, even when their header belongs to a read-only region.
func ValueParam[T any](b *Builder) (Data, error) {
	return newDataParam(b, reflect.TypeFor[T](), "ValueParam", false, false)
}

// PointerParam adds one native-width parameter for a writable *T, where T is
// a transferable struct or array. Checked typed calls reject nil pointers.
func PointerParam[T any](b *Builder) (Data, error) {
	return newDataParam(b, reflect.TypeFor[T](), "PointerParam", true, false)
}

// SliceParam adds one native-width parameter for a []E header supplied by
// execution.Words. Elements are writable; headers cannot be written with Store.
// A nil slice has length zero and no valid element index.
func SliceParam[E any](b *Builder) (Data, error) {
	return newDataParam(b, reflect.TypeFor[[]E](), "SliceParam", false, true)
}
func newDataParam(b *Builder, t reflect.Type, name string, writable, slice bool) (Data, error) {
	if !slice && t.Kind() != reflect.Struct && t.Kind() != reflect.Array {
		return Data{}, fmt.Errorf("%s: type %s must be a struct or array: %w", name, t, ErrInvalidFunction)
	}
	layout, invalid := cachedDataLayout(t)
	if invalid != nil {
		return Data{}, fmt.Errorf("%s: field %q has unsupported type %s: %w", name, invalid.path, invalid.typ, ErrInvalidFunction)
	}
	base, err := b.ParamNative()
	if err != nil {
		return Data{}, fmt.Errorf("%s: %w", name, err)
	}
	if b.parameterTypes == nil {
		b.parameterTypes = make(map[int]reflect.Type)
	}
	parameterType := t
	if writable {
		parameterType = reflect.PointerTo(t)
	}
	b.parameterTypes[b.arity-1] = parameterType
	root, err := b.Var(base)
	if err != nil {
		return Data{}, fmt.Errorf("%s: %w", name, err)
	}
	return Data{builder: b, root: root, rooted: true, layout: layout, writable: writable}, nil
}
func (d Data) viewError(method, reason string) error {
	return fmt.Errorf("Data.%s: %s: %w", method, reason, ErrInvalidFunction)
}
func (d Data) valid() bool {
	if d.layout == nil || d.builder == nil {
		return false
	}
	if d.indexed && d.epoch != d.builder.dataEpoch {
		return false
	}
	if d.rooted {
		return d.builder.validVar(d.root)
	}
	return d.blockStart == d.currentBlock() && d.builder.Valid(d.local) && d.local.Type() == ir.Scalar(NativeBits)
}
func (d Data) currentBlock() int {
	if d.builder != nil && d.builder.control != nil {
		return d.builder.control.start
	}
	return 0
}

func (d Data) baseValue() (Value, error) {
	if !d.valid() {
		return Value{}, d.viewError("Base", "uninitialized or expired view; use a fresh Index in this block")
	}
	if d.rooted {
		return d.builder.Get(d.root)
	}
	return d.builder.materialize(d.local)
}

// Base returns the underlying native-width base without the static At offset.
// Roots emit Get on their address Var. Invalid views return an invalid Value.
// Indexed bases cannot be passed to control edges or stored in Vars.
func (d Data) Base() Value { base, _ := d.baseValue(); return base }

// Rebase makes a block-local view, preserving type, offset and writability.
// Indexed views remain confined to their original block, even after Rebase.
// The replacement base must retain the original Go type's alignment.
// The new base must have native width and belong to this Builder's current
// block. An invalid base is reported by the next operation on the returned view.
func (d Data) Rebase(base Value) Data {
	d.rooted, d.root, d.local = false, Var{}, base
	if !d.indexed {
		d.blockStart = d.currentBlock()
	}
	return d
}

// At selects dotted fields and constant array indexes, e.g. "Rows[1].Cells"
// or "[2].ID" on an array. A slice ends a path: use Index for its elements.
// At emits no load, preserving region writability and root-Var addressing.
func (d Data) At(path string) (Data, error) {
	bad := func(reason string) (Data, error) { return Data{}, d.viewError("At("+strconv.Quote(path)+")", reason) }
	if !d.valid() {
		return bad("view is uninitialized or unavailable in this scope/block")
	}
	if path == "" {
		return bad("empty field path; use Load for the current view")
	}
	for pos := 0; pos < len(path); {
		if d.layout.typ.Kind() == reflect.Slice {
			return bad("slice paths end at the header; use Index")
		}
		if path[pos] == '[' {
			if d.layout.typ.Kind() != reflect.Array {
				return bad("indexing a non-array view")
			}
			pos++
			start := pos
			for pos < len(path) && path[pos] >= '0' && path[pos] <= '9' {
				pos++
			}
			if pos == start || pos == len(path) || path[pos] != ']' {
				return bad("expected a constant nonnegative array index followed by ]")
			}
			index, err := strconv.ParseUint(path[start:pos], 10, 64)
			if err != nil || index >= uint64(d.layout.typ.Len()) {
				return bad("array index out of bounds")
			}
			d.offset += uintptr(index) * d.layout.elem.typ.Size()
			d.layout = d.layout.elem
			pos++
		} else {
			start := pos
			for pos < len(path) && path[pos] != '.' && path[pos] != '[' && path[pos] != ']' {
				pos++
			}
			if pos == start || d.layout.typ.Kind() != reflect.Struct {
				return bad("expected a struct field name")
			}
			f, ok := d.layout.fields[path[start:pos]]
			if !ok {
				return bad("unknown field " + path[start:pos])
			}
			d.layout, d.offset = f.layout, d.offset+f.offset
		}
		if pos == len(path) {
			return d, nil
		}
		if path[pos] == '[' {
			continue
		}
		if path[pos] != '.' || pos+1 == len(path) || path[pos+1] == '[' {
			return bad("expected . followed by a field name")
		}
		pos++
	}
	return bad("invalid path")
}

// Addr returns the view's native address including its static offset. Raw stores
// bypass Store's checks: writing to a read-only region is undefined, and Go
// pointer words and slice headers must not be stored.
func (d Data) Addr() (Value, error) {
	base, err := d.baseValue()
	if err != nil {
		return Value{}, err
	}
	if d.offset == 0 {
		return base, nil
	}
	delta, err := d.builder.ConstNative(d.offset)
	if err != nil {
		return Value{}, err
	}
	return d.builder.Add(base, delta)
}
func (d Data) load(address Value, bits uint8) (Value, error) {
	b := d.builder
	if bits == 64 {
		return b.Load64(address)
	}
	return b.loadNarrow(address, bits, true)
}

// Load reads a scalar leaf at its declared width, zero-extending integers.
// Use SignExtend for signed fields. Bools have width 8; floats use the IEEE
// bit-pattern Values expected by Float32/Float64 and floating-point operations.
func (d Data) Load() (Value, error) {
	if !d.valid() || d.layout.bits == 0 {
		return Value{}, d.viewError("Load", "view must be an available scalar leaf")
	}
	address, err := d.Addr()
	if err != nil {
		return Value{}, err
	}
	return d.load(address, d.layout.bits)
}

// Field is At(path) followed by Load, emitting a fresh read on every invocation.
func (d Data) Field(path string) (Value, error) {
	field, err := d.At(path)
	if err != nil {
		return Value{}, err
	}
	return field.Load()
}

// Store writes a matching-width scalar in a writable region; floats use IEEE
// bits. Aggregates and slice headers cannot be stored. Slice backing arrays are
// writable even when their headers belong to read-only ValueParam regions.
func (d Data) Store(value Value) error {
	if !d.valid() || !d.writable || d.layout.bits == 0 {
		return d.viewError("Store", "view must be a writable scalar leaf")
	}
	if value.Type() != ir.Scalar(d.layout.bits) || !d.builder.Valid(value) {
		return d.viewError("Store", "value must be in scope and match the scalar leaf width")
	}
	address, err := d.Addr()
	if err != nil {
		return err
	}
	switch d.layout.bits {
	case 8:
		return d.builder.Store8(address, value)
	case 16:
		return d.builder.storeNarrow(address, value, 16, true)
	case 32:
		return d.builder.storeNarrow(address, value, 32, true)
	default:
		return d.builder.Store64(address, value)
	}
}

// Len returns a native-width constant array length or freshly loads slice length.
func (d Data) Len() (Value, error) {
	if !d.valid() {
		return Value{}, d.viewError("Len", "view is uninitialized or unavailable")
	}
	if d.layout.typ.Kind() == reflect.Array {
		return d.builder.ConstNative(uintptr(d.layout.typ.Len()))
	}
	if d.layout.typ.Kind() != reflect.Slice {
		return Value{}, d.viewError("Len", "view must be an array or slice")
	}
	header, err := d.Addr()
	if err != nil {
		return Value{}, err
	}
	at, err := d.builder.nativeByteAddress(header, int(NativeBits/8))
	if err != nil {
		return Value{}, err
	}
	return d.load(at, NativeBits)
}

// Index checks unsigned bounds and branches to outOfBounds on failure, then
// resumes in a fresh block with an element view. Values defined before the call
// dominate that block and stay usable there. Indexed views expire at
// calls/syscalls; take a fresh Index afterwards, including after callbacks.
// Array elements inherit writability; slice elements are writable.
func (d Data) Index(index Value, outOfBounds Label) (Data, error) {
	if !d.valid() || d.layout.typ.Kind() != reflect.Array && d.layout.typ.Kind() != reflect.Slice {
		return Data{}, d.viewError("Index", "view must be an available array or slice")
	}
	if !d.builder.Valid(index) || index.Type().Kind != ir.ScalarKind || index.Bits() > 64 {
		return Data{}, d.viewError("Index", "index must be an in-scope scalar of at most 64 bits")
	}
	b := d.builder
	address, err := d.Addr()
	if err != nil {
		return Data{}, err
	}
	var count Value
	writable := d.writable
	if d.layout.typ.Kind() == reflect.Slice {
		at, e := b.nativeByteAddress(address, int(NativeBits/8))
		if e != nil {
			return Data{}, e
		}
		count, err = d.load(at, NativeBits)
		if err == nil {
			address, err = d.load(address, NativeBits)
		}
		writable = true
	} else {
		count, err = b.ConstNative(uintptr(d.layout.typ.Len()))
	}
	if err != nil {
		return Data{}, err
	}
	bits := max(NativeBits, index.Type().LaneBits)
	index, err = b.CastUnsigned(index, bits)
	if err != nil {
		return Data{}, err
	}
	address, err = b.CastUnsigned(address, bits)
	if err != nil {
		return Data{}, err
	}
	count, err = b.CastUnsigned(count, bits)
	if err != nil {
		return Data{}, err
	}
	address, err = b.IndexedAddress(address, index, count, uint64(d.layout.elem.typ.Size()), outOfBounds)
	if err != nil {
		return Data{}, err
	}
	address, err = b.CastNative(address)
	if err != nil {
		return Data{}, err
	}
	return Data{builder: b, local: address, blockStart: d.currentBlock(), layout: d.layout.elem, writable: writable, indexed: true, epoch: b.dataEpoch}, nil
}
