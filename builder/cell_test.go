package builder

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func cellPinned(p *cellPin) bool {
	_, ok := cellPinSet.Load(p)
	return ok && !p.released.Load()
}

// cellMust returns a helper that fails t on an error and returns the value.
func cellMust[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestCellRejectsStore(t *testing.T) {
	type nested struct{ Values []uint32 }
	value := new(struct{ X uint32 })
	for name, store := range map[string]func(b *Builder) error{
		"value": func(b *Builder) error {
			d := cellMust[Data](t)(ValueCell(b, value))
			x := cellMust[Data](t)(d.At("X"))
			return x.Store(cellMust[Value](t)(b.ConstBits(32, 1)))
		},
		"element": func(b *Builder) error {
			d := cellMust[Data](t)(SliceCell(b, []uint32{1}))
			e := cellMust[Data](t)(d.Index(cellMust[Value](t)(b.ConstNative(0)), b.NewLabel()))
			return e.Store(cellMust[Value](t)(b.ConstBits(32, 1)))
		},
		"nested element": func(b *Builder) error {
			d := cellMust[Data](t)(SliceCell(b, []nested{{Values: []uint32{1}}}))
			outer := cellMust[Data](t)(d.Index(cellMust[Value](t)(b.ConstNative(0)), b.NewLabel()))
			values := cellMust[Data](t)(outer.At("Values"))
			inner := cellMust[Data](t)(values.Index(cellMust[Value](t)(b.ConstNative(0)), b.NewLabel()))
			return inner.Store(cellMust[Value](t)(b.ConstBits(32, 1)))
		},
		"rebased": func(b *Builder) error {
			d := cellMust[Data](t)(ValueCell(b, value))
			x := cellMust[Data](t)(d.Rebase(d.Base()).At("X"))
			return x.Store(cellMust[Value](t)(b.ConstBits(32, 1)))
		},
	} {
		if err := store(NewBuilder()); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("%s: Store = %v", name, err)
		}
	}
}

func TestCellRejectsTypes(t *testing.T) {
	finished := NewBuilder()
	finished.Return(cellMust[Value](t)(finished.Const(0)))
	if _, err := finished.Finish(); err != nil {
		t.Fatal(err)
	}
	for name, capture := range map[string]func(b *Builder) error{
		"nil":        func(b *Builder) error { _, err := ValueCell[uint32](b, nil); return err },
		"slice":      func(b *Builder) error { _, err := ValueCell(b, &struct{ S []int }{}); return err },
		"zero size":  func(b *Builder) error { _, err := ValueCell(b, &struct{}{}); return err },
		"pointer":    func(b *Builder) error { _, err := ValueCell(b, &struct{ P *int }{}); return err },
		"string":     func(b *Builder) error { _, err := SliceCell(b, []string{"x"}); return err },
		"nested ptr": func(b *Builder) error { _, err := SliceCell(b, []struct{ S [][]*int }{}); return err },
		"nil builder": func(*Builder) error {
			_, err := ValueCell(nil, new(uint32))
			return err
		},
		"finished": func(*Builder) error { _, err := SliceCell(finished, []uint8{1}); return err },
	} {
		b := NewBuilder()
		if err := capture(b); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("%s: %v", name, err)
		}
		if b.cells != nil {
			t.Errorf("%s: pinned after an error", name)
		}
	}
}

func TestCellViewErrors(t *testing.T) {
	value := new([2]uint32)
	for name, use := range map[string]func(b *Builder) error{
		"field of array": func(b *Builder) error { _, err := cellMust[Data](t)(ValueCell(b, value)).Field("X"); return err },
		"out of range":   func(b *Builder) error { _, err := cellMust[Data](t)(ValueCell(b, value)).Field("[2]"); return err },
		"aggregate load": func(b *Builder) error { _, err := cellMust[Data](t)(ValueCell(b, value)).Load(); return err },
		"slice path":     func(b *Builder) error { _, err := cellMust[Data](t)(SliceCell(b, []uint8{1})).At("[0]"); return err },
		"invalid index": func(b *Builder) error {
			_, err := cellMust[Data](t)(SliceCell(b, []uint8{1})).Index(Value{}, b.NewLabel())
			return err
		},
	} {
		if err := use(NewBuilder()); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestCellFinishFinalizers checks that Finish records one Finalizer for all
// of a Builder's cells, and that a copy of the Builder pins the memory again
// for its own program, so neither releases the other's pins.
func TestCellFinishFinalizers(t *testing.T) {
	b := NewBuilder()
	values := []uint32{1, 2}
	first := cellMust[Data](t)(SliceCell(b, values))
	second := cellMust[Data](t)(ValueCell(b, &values[1]))
	sum := cellMust[Value](t)(b.Add(cellMust[Value](t)(first.Len()), cellMust[Value](t)(b.CastNative(cellMust[Value](t)(second.Load())))))
	if err := b.Return(sum); err != nil {
		t.Fatal(err)
	}
	pin := b.cells.pin
	copied := *b
	copied.detachCopy()
	otherPin := copied.cells.pin
	if otherPin == pin || !cellPinned(otherPin) {
		t.Fatal("the copy shares the pins")
	}
	program := cellMust[*ir.SealedProgram](t)(b.Finish())
	other := cellMust[*ir.SealedProgram](t)(copied.Finish())
	fs, otherFs := program.Finalizers(), other.Finalizers()
	if len(fs) != 1 || len(otherFs) != 1 || &fs[0] == &otherFs[0] {
		t.Fatalf("Finalizers %d and %d, shared %v", len(fs), len(otherFs), len(fs) == 1 && &fs[0] == &otherFs[0])
	}
	if b.cells != nil || copied.cells != nil || !cellPinned(pin) {
		t.Fatalf("Finish kept the pins, or released them")
	}
	fs.Finalize()
	if cellPinned(pin) || !cellPinned(otherPin) {
		t.Fatalf("pinned %v and %v after one Finalize", cellPinned(pin), cellPinned(otherPin))
	}
	otherFs.Finalize()
	fs.Finalize() // a second run is a no-op
	if cellPinned(otherPin) {
		t.Fatal("the copy is still pinned after Finalize")
	}
}

// TestCellSegmentFinalizers checks that a segment Builder hands its cells'
// Finalizer over once, with the first segment after the first cell.
func TestCellSegmentFinalizers(t *testing.T) {
	r := NewSegmentRecorder()
	b := NewSegmentBuilder(r.Emit)
	i := cellMust[Value](t)(b.ParamNative())
	next := b.NewLabel()
	if err := b.Jump(next); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(next); err != nil {
		t.Fatal(err)
	}
	cell := cellMust[Data](t)(SliceCell(b, []uint64{5, 6}))
	pin := b.cells.pin
	bad := b.NewLabel()
	element := cellMust[Data](t)(cell.Index(i, bad))
	value := cellMust[Value](t)(element.Load())
	cellMust[Data](t)(ValueCell(b, new(uint8)))
	if err := b.Return(cellMust[Value](t)(b.CastNative(value))); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(bad); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(cellMust[Value](t)(b.ConstNative(0))); err != nil {
		t.Fatal(err)
	}
	if err := b.End(); err != nil {
		t.Fatal(err)
	}
	var with []int
	for index, s := range r.Segments {
		if len(s.Finalizers) != 0 {
			with = append(with, index)
		}
	}
	if len(r.Segments) < 3 || len(with) != 1 || with[0] != 1 || len(r.Segments[1].Finalizers) != 1 {
		t.Fatalf("%d segments, Finalizers in %v", len(r.Segments), with)
	}
	if b.cells != nil || !cellPinned(pin) {
		t.Fatal("End kept the pins, or released them")
	}
	r.Segments[1].Finalizers.Finalize()
	if cellPinned(pin) {
		t.Fatal("still pinned after Finalize")
	}
}

// TestCellUnpinsUnusedProgram drops programs that were never bound, and a
// Builder that was never finished: collecting them must release their pins,
// not panic in runtime.Pinner.
func TestCellUnpinsUnusedProgram(t *testing.T) {
	pins := make([]*cellPin, 0, 2)
	func() {
		b := NewBuilder()
		cell := cellMust[Data](t)(ValueCell(b, new(uint32)))
		b.Return(cellMust[Value](t)(b.CastNative(cellMust[Value](t)(cell.Load()))))
		pins = append(pins, b.cells.pin)
		cellMust[*ir.SealedProgram](t)(b.Finish())
		unfinished := NewBuilder()
		cellMust[Data](t)(SliceCell(unfinished, []uint8{1}))
		pins = append(pins, unfinished.cells.pin)
	}()
	for range 50 {
		runtime.GC()
		if !cellPinned(pins[0]) && !cellPinned(pins[1]) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pins not released: %v %v", cellPinned(pins[0]), cellPinned(pins[1]))
}

type cellLayoutItem struct {
	A uint8
	B uint16
	C uint32
	D uint64
}

// cellLoads lists the load mnemonics of a listing, in order.
func cellLoads(listing string, loads map[string]bool) []string {
	var found []string
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		for _, field := range fields {
			if loads[field] {
				found = append(found, field)
				break
			}
		}
	}
	return found
}

// TestCellAlignedLoads checks that cell fields load at their natural
// alignment, with the same loads as the same fields of a ValueParam, on
// targets where unaligned narrow loads expand to byte sequences.
func TestCellAlignedLoads(t *testing.T) {
	item := new(cellLayoutItem)
	items := make([]cellLayoutItem, 2)
	build := func(t *testing.T, view func(b *Builder) (Data, error), index bool) *ir.SealedProgram {
		b := NewBuilder()
		i := cellMust[Value](t)(b.ParamNative())
		d := cellMust[Data](t)(view(b))
		var bad Label
		if index {
			bad = b.NewLabel()
			d = cellMust[Data](t)(d.Index(i, bad))
		}
		sum := cellMust[Value](t)(d.Field("D"))
		for _, field := range []string{"A", "B", "C"} {
			v := cellMust[Value](t)(b.CastUnsigned(cellMust[Value](t)(d.Field(field)), 64))
			sum = cellMust[Value](t)(b.Add(sum, v))
		}
		if err := b.Return(cellMust[Value](t)(b.CastNative(sum))); err != nil {
			t.Fatal(err)
		}
		if index {
			if err := b.Bind(bad); err != nil {
				t.Fatal(err)
			}
			if err := b.Return(cellMust[Value](t)(b.ConstNative(0))); err != nil {
				t.Fatal(err)
			}
		}
		return cellMust[*ir.SealedProgram](t)(b.Finish())
	}
	loads := map[string]map[string]bool{
		"riscv64": {"lb": true, "lbu": true, "lh": true, "lhu": true, "lw": true, "lwu": true, "ld": true},
		"loong64": {"ld.b": true, "ld.bu": true, "ld.h": true, "ld.hu": true, "ld.w": true, "ld.wu": true, "ld.d": true},
		"ppc64le": {"lbz": true, "lhz": true, "lha": true, "lwz": true, "lwa": true, "ld": true},
	}
	views := []struct {
		name        string
		param, cell func(b *Builder) (Data, error)
		index       bool
	}{
		{"value", ValueParam[cellLayoutItem], func(b *Builder) (Data, error) { return ValueCell(b, item) }, false},
		{"slice", SliceParam[cellLayoutItem], func(b *Builder) (Data, error) { return SliceCell(b, items) }, true},
	}
	for _, arch := range []string{"riscv64", "loong64", "ppc64le"} {
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			for _, v := range views {
				spec := compile.TargetSpec{Architecture: arch, Tier: tier}
				f := compile.Func{ABI: abi.C}
				param := cellMust[string](t)(lowerListing(build(t, v.param, v.index), spec, f))
				cell := cellMust[string](t)(lowerListing(build(t, v.cell, v.index), spec, f))
				want, got := cellLoads(param, loads[arch]), cellLoads(cell, loads[arch])
				// A SliceParam also loads its header's pointer and length.
				if v.index && len(want) >= 2 {
					want = want[2:]
				}
				if len(got) != 4 || strings.Join(got, " ") != strings.Join(want, " ") {
					t.Errorf("%s/%v/%s: cell loads %v, ValueParam loads %v\n%s\n%s", arch, tier, v.name, got, want, cell, param)
				}
			}
		}
	}
}
