package irconform

import (
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// stackCases cover stack areas (nyanjit.StackData, ir.StackArea). No result
// depends on an area's address, only on its contents, size and alignment.
func stackCases() []Case {
	var cases []Case
	add := func(name string, inputs [][]uint64, build func() (*ir.SealedProgram, error)) {
		cases = append(cases, Case{Name: "stack/" + name, Inputs: inputs, Build: build})
	}
	// A [64]uint32 filled by one loop and summed by another.
	add("sum-array", binary(32), built(func(b *nyanjit.Builder) {
		x, y := b.ParamBits(32), b.ParamBits(32)
		array := nyanjit.StackData[[64]uint32](b)
		fail := b.NewLabel()
		i := b.Var(b.ConstNative(0))
		sum, j := b.Var(b.ConstBits(32, 0)), b.Var(b.ConstNative(0))
		fill := b.NewLabel()
		b.Jump(fill)
		b.Bind(fill)
		index := b.Get(i)
		element := array.Index(index, fail)
		element.Store(b.Add(b.Mul(x, b.CastUnsigned(index, 32)), y))
		next := b.Add(b.Get(i), b.ConstNative(1))
		b.Set(i, next)
		b.Branch(b.CompareImmediate(next, ir.BranchBelow, 64), fill)
		total := b.NewLabel()
		b.Jump(total)
		b.Bind(total)
		value := array.Index(b.Get(j), fail).Load()
		b.Set(sum, b.Add(b.Get(sum), value))
		after := b.Add(b.Get(j), b.ConstNative(1))
		b.Set(j, after)
		b.Branch(b.CompareImmediate(after, ir.BranchBelow, 64), total)
		b.Return(b.Get(sum))
		b.Bind(fail)
		b.Return(b.ConstBits(32, 0xdead))
	}))
	// Every field reads as zero before the first write, although the call
	// before stored its argument there, at every field width.
	type record struct {
		A uint8
		B uint16
		C uint32
		D uint64
		E [5]uint8
		F [3]uint64
		G bool
	}
	add("zero-on-entry", unary(64), built(func(b *nyanjit.Builder) {
		x := b.Param()
		r := nyanjit.StackData[record](b)
		var seen nyanjit.Value
		for k, path := range []string{"A", "B", "C", "D", "E[0]", "E[4]", "F[0]", "F[1]", "F[2]", "G"} {
			field := r.At(path)
			old := b.CastUnsigned(field.Load(), 64)
			if k == 0 {
				seen = old
			} else {
				seen = b.Or(seen, old)
			}
		}
		for _, store := range []struct {
			path string
			bits uint8
		}{{"A", 8}, {"B", 16}, {"C", 32}, {"D", 64}, {"E[0]", 8}, {"E[4]", 8}, {"F[0]", 64}, {"F[1]", 64}, {"F[2]", 64}, {"G", 8}} {
			r.At(store.path).Store(b.CastUnsigned(x, store.bits))
		}
		b.Return(b.Add(seen, r.Field("F[2]")))
	}))
	// A large area: the zeroing loops past one base register's reach.
	add("zero-large", unary(64), built(func(b *nyanjit.Builder) {
		x := b.Param()
		big := nyanjit.StackData[[600]uint64](b)
		fail := b.NewLabel()
		first := big.Index(b.ConstNative(0), fail)
		old := first.Load()
		first.Store(x)
		last := big.Index(b.ConstNative(599), fail)
		old = b.Or(old, last.Load())
		last.Store(x)
		middle := big.Index(b.ConstNative(300), fail)
		old = b.Or(old, middle.Load())
		middle.Store(x)
		b.Return(b.Add(old, big.Index(b.ConstNative(599), fail).Load()))
		b.Bind(fail)
		b.Return(b.Const(0xdead))
	}))
	// Index bounds checks at both edges and beyond.
	add("index-bounds", [][]uint64{{0}, {1}, {7}, {8}, {9}, {1 << 31}, {1 << 63}, {^uint64(0)}}, built(func(b *nyanjit.Builder) {
		i := b.Param()
		array := nyanjit.StackData[[8]uint64](b)
		fail := b.NewLabel()
		element := array.Index(i, fail)
		element.Store(b.Add(i, b.Const(100)))
		b.Return(array.Index(i, fail).Load())
		b.Bind(fail)
		b.Return(b.Const(0xdead))
	}))
	// Two Builder areas and their addresses' alignment: the low bits below
	// the Go alignment are zero, and stores to one area leave the other
	// intact.
	add("two-areas", binary(64), built(func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		bytes := nyanjit.StackData[[3]uint8](b)
		words := nyanjit.StackData[[2]uint64](b)
		fail := b.NewLabel()
		for k := uint64(0); k < 3; k++ {
			bytes.Index(b.ConstNative(uintptr(k)), fail).Store(b.CastUnsigned(b.Add(x, b.Const(k)), 8))
		}
		words.At("[0]").Store(y)
		words.At("[1]").Store(b.Xor(x, y))
		misaligned := b.CastUnsigned(b.And(words.Addr(), b.ConstNative(unsafe.Alignof(uint64(0))-1)), 64)
		sum := b.Add(words.Field("[0]"), words.Field("[1]"))
		for k := uint64(0); k < 3; k++ {
			sum = b.Add(sum, b.CastUnsigned(bytes.Index(b.ConstNative(uintptr(k)), fail).Load(), 64))
		}
		b.Return(b.Or(b.ShiftLeft(misaligned, 56), sum))
		b.Bind(fail)
		b.Return(b.Const(0xdead))
	}))
	// Raw areas with every alignment, each checked and written through.
	add("aligned-raw", binary(64), stackAligned)
	return cases
}

// stackAligned declares areas of alignment 1 to 16. It returns the OR of
// every address's misaligned low bits, shifted to the top byte, plus the
// sum of what it stores and loads back through each area.
func stackAligned() (*ir.SealedProgram, error) {
	bits := nativeBits
	p := &ir.Program{Arity: 2}
	add := func(ins ir.Instruction) int {
		p.Instructions = append(p.Instructions, ins)
		return len(p.Instructions) - 1
	}
	x := add(ir.Instruction{Op: ir.OpParam})
	y := add(ir.Instruction{Op: ir.OpParam})
	widen := func(v int) int {
		if bits == 64 {
			return v
		}
		return add(ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: v})
	}
	misaligned, sum := -1, -1
	for k, align := range []int{16, 1, 8, 2, 16, 4} {
		p.StackAreas = append(p.StackAreas, ir.StackArea{Bytes: 3 + 8*k, Align: align})
		address := add(ir.Instruction{Op: ir.OpStackAddress, Bits: bits, Imm: uint64(k)})
		low := widen(add(ir.Instruction{Op: ir.OpAnd, Bits: bits, A: address, B: add(ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: uint64(align - 1)})}))
		value := add(ir.Instruction{Op: ir.OpAdd, A: x, B: add(ir.Instruction{Op: ir.OpConst, Imm: uint64(k)})})
		add(ir.Instruction{Op: ir.OpStore8, A: address, B: value})
		end := add(ir.Instruction{Op: ir.OpAdd, Bits: bits, A: address, B: add(ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: uint64(2 + 8*k)})})
		add(ir.Instruction{Op: ir.OpStore8, A: end, B: y})
		loaded := add(ir.Instruction{Op: ir.OpAdd,
			A: add(ir.Instruction{Op: ir.OpLoad8, Bits: 64, A: address}),
			B: add(ir.Instruction{Op: ir.OpLoad8, Bits: 64, A: end})})
		if misaligned < 0 {
			misaligned, sum = low, loaded
			continue
		}
		misaligned = add(ir.Instruction{Op: ir.OpOr, A: misaligned, B: low})
		sum = add(ir.Instruction{Op: ir.OpAdd, A: sum, B: loaded})
	}
	shifted := add(ir.Instruction{Op: ir.OpShiftLeft, A: misaligned, Imm: 56})
	p.Result = add(ir.Instruction{Op: ir.OpOr, A: shifted, B: sum})
	return ir.SealOwned(p)
}
