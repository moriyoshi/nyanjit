//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package nyanjit_test

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// cellModes builds a function with a Builder from NewBuilder (compiled with
// Compile) and from NewBuilderFor (Lower), at both tiers.
func cellModes(t *testing.T, build func(b *nyanjit.Builder)) map[string]*execution.Code {
	t.Helper()
	codes := make(map[string]*execution.Code)
	f := compile.Func{ABI: abi.C}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		b := nyanjit.NewBuilder()
		build(b)
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		code, err := nyanjit.Compile(program, compile.TargetSpec{Tier: tier}, f)
		if err != nil {
			t.Fatal(err)
		}
		codes[fmt.Sprintf("%v/Finish", tier)] = code
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		bound := nyanjit.NewBuilderFor(target, f)
		build(bound)
		unit, err := bound.Lower()
		if err != nil {
			t.Fatal(err)
		}
		template, err := unit.Template()
		if err != nil {
			t.Fatal(err)
		}
		if code, err = template.Bind(nil); err != nil {
			t.Fatal(err)
		}
		codes[fmt.Sprintf("%v/Lower", tier)] = code
	}
	t.Cleanup(func() {
		for _, code := range codes {
			_ = code.Close()
		}
	})
	return codes
}

func checkCalls(t *testing.T, codes map[string]*execution.Code, cases [][2]uintptr) {
	t.Helper()
	for name, code := range codes {
		for _, c := range cases {
			runtime.GC()
			if got, err := code.Call(c[0]); err != nil || got != c[1] {
				t.Errorf("%s: f(%d) = %d, %v; want %d", name, c[0], got, err, c[1])
			}
		}
	}
}

type cellItem struct {
	ID    uint32
	Flag  bool
	Score uint64
	Tags  [3]uint16
}

func TestValueCellScalar(t *testing.T) {
	value := new(uint32)
	*value = 123456
	codes := cellModes(t, func(b *nyanjit.Builder) {
		b.ParamNative()
		b.Return(b.CastNative(nyanjit.ValueCell(b, value).Load()))
	})
	checkCalls(t, codes, [][2]uintptr{{0, 123456}})
	*value = 7 // the view reads the memory, not a copy
	checkCalls(t, codes, [][2]uintptr{{0, 7}})
}

func TestValueCellStruct(t *testing.T) {
	item := &cellItem{ID: 70000, Flag: true, Score: 1 << 40, Tags: [3]uint16{5, 600, 65535}}
	codes := cellModes(t, func(b *nyanjit.Builder) {
		selector := b.ParamNative()
		cell := nyanjit.ValueCell(b, item)
		id, tags, score := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(selector), id)
		b.Branch(b.Compare(selector, ir.BranchEqual, b.ConstNative(1)), score)
		b.Jump(tags)
		b.Bind(id)
		b.Return(b.CastNative(b.Add(cell.Field("ID"), b.CastUnsigned(cell.At("Flag").Load(), 32))))
		b.Bind(score)
		b.Return(b.CastNative(b.ShiftRight(cell.Field("Score"), 32)))
		b.Bind(tags)
		bad := b.NewLabel()
		tag := cell.At("Tags").Index(b.Sub(selector, b.ConstNative(2)), bad)
		b.Return(b.CastNative(tag.Load()))
		b.Bind(bad)
		b.Return(b.CastNative(b.Add(cell.At("Tags").Len(), b.ConstNative(1000))))
	})
	checkCalls(t, codes, [][2]uintptr{{0, 70001}, {1, 256}, {2, 5}, {3, 600}, {4, 65535}, {5, 1003}})
}

func TestValueCellArray(t *testing.T) {
	grid := &[2][3]uint8{{1, 2, 3}, {4, 5, 6}}
	codes := cellModes(t, func(b *nyanjit.Builder) {
		i := b.ParamNative()
		cell := nyanjit.ValueCell(b, grid)
		bad := b.NewLabel()
		row := cell.Index(i, bad)
		b.Return(b.CastNative(b.Add(row.Field("[2]"), cell.Field("[0][1]"))))
		b.Bind(bad)
		b.Return(b.CastNative(cell.Len()))
	})
	checkCalls(t, codes, [][2]uintptr{{0, 5}, {1, 8}, {2, 2}, {^uintptr(0), 2}})
}

func TestSliceCell(t *testing.T) {
	values := []uint16{10, 500, 65535}
	codes := cellModes(t, func(b *nyanjit.Builder) {
		i := b.ParamNative()
		cell := nyanjit.SliceCell(b, values)
		bad := b.NewLabel()
		b.Return(b.CastNative(cell.Index(i, bad).Load()))
		b.Bind(bad)
		b.Return(b.Add(cell.Len(), b.ConstNative(1000)))
	})
	checkCalls(t, codes, [][2]uintptr{{0, 10}, {1, 500}, {2, 65535}, {3, 1003}, {^uintptr(0), 1003}})
	values[1] = 777
	checkCalls(t, codes, [][2]uintptr{{1, 777}})
}

func TestSliceCellStructElements(t *testing.T) {
	items := []cellItem{{ID: 1, Score: 10, Tags: [3]uint16{1, 2, 3}}, {ID: 2, Flag: true, Score: 20, Tags: [3]uint16{4, 5, 6}}}
	codes := cellModes(t, func(b *nyanjit.Builder) {
		i := b.ParamNative()
		cell := nyanjit.SliceCell(b, items)
		bad := b.NewLabel()
		item := cell.Index(i, bad)
		sum := b.Add(b.CastUnsigned(item.Field("ID"), 64), item.Field("Score"))
		sum = b.Add(sum, b.CastUnsigned(item.Field("Tags[2]"), 64))
		sum = b.Add(sum, b.CastUnsigned(item.Field("Flag"), 64))
		b.Return(b.CastNative(sum))
		b.Bind(bad)
		b.Return(b.ConstNative(0))
	})
	checkCalls(t, codes, [][2]uintptr{{0, 14}, {1, 29}, {2, 0}})
}

func TestSliceCellNestedSlices(t *testing.T) {
	type row struct {
		Base  uint32
		Cells []uint32
	}
	rows := []row{{Base: 100, Cells: []uint32{1, 2}}, {Base: 200}, {Base: 300, Cells: []uint32{3, 4, 5}}}
	codes := cellModes(t, func(b *nyanjit.Builder) {
		i := b.ParamNative()
		cell := nyanjit.SliceCell(b, rows)
		bad := b.NewLabel()
		r := cell.Index(b.ShiftRight(i, 4), bad)
		base := r.Field("Base")
		cells := r.At("Cells")
		// Indexed views stay in their block: read the length before the
		// inner Index, whose bounds branch leaves the block.
		n := cells.Len()
		empty := b.NewLabel()
		value := cells.Index(b.And(i, b.ConstNative(15)), empty)
		b.Return(b.CastNative(b.Add(base, value.Load())))
		b.Bind(empty)
		b.Return(b.Add(b.ConstNative(1000), n))
		b.Bind(bad)
		b.Return(b.ConstNative(0))
	})
	checkCalls(t, codes, [][2]uintptr{{0x00, 101}, {0x01, 102}, {0x02, 1002}, {0x10, 1000}, {0x22, 305}, {0x23, 1003}, {0x30, 0}})
}

func TestSliceCellEmpty(t *testing.T) {
	for _, values := range [][]uint64{nil, make([]uint64, 0, 4)} {
		codes := cellModes(t, func(b *nyanjit.Builder) {
			i := b.ParamNative()
			cell := nyanjit.SliceCell(b, values)
			bad := b.NewLabel()
			b.Return(b.CastNative(cell.Index(i, bad).Load()))
			b.Bind(bad)
			b.Return(b.Add(cell.Len(), b.ConstNative(1000)))
		})
		checkCalls(t, codes, [][2]uintptr{{0, 1000}, {1, 1000}})
	}
}

// TestCellFinalizersAcrossLowerings lowers one program twice at each tier
// and checks that its Finalizers run once, when the last Code closes.
func TestCellFinalizersAcrossLowerings(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.ParamNative()
	value := new(uint64)
	*value = 42
	b.Return(b.CastNative(nyanjit.ValueCell(b, value).Load()))
	built, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Finalizers()) != 1 {
		t.Fatalf("program has %d Finalizers", len(built.Finalizers()))
	}
	// Count the runs of a list that wraps the cell's Finalizer.
	var runs atomic.Int32
	raw := built.Clone()
	raw.Finalizers = backend.Finalizers{raw.Finalizers[0], backend.FinalizerFunc(func() { runs.Add(1) })}
	program, err := ir.Seal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var codes []*execution.Code
	var templates []*execution.Template
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			template, err := unit.Template()
			if err != nil {
				t.Fatal(err)
			}
			templates = append(templates, template)
			for range 2 {
				code, err := template.Bind(nil)
				if err != nil {
					t.Fatal(err)
				}
				codes = append(codes, code)
			}
		}
	}
	for i, code := range codes {
		if got, err := code.Call(0); err != nil || got != 42 {
			t.Fatalf("code %d: %d, %v", i, got, err)
		}
		if runs.Load() != 0 {
			t.Fatalf("Finalizers ran with %d of %d Codes open", len(codes)-i, len(codes))
		}
		_ = code.Close()
	}
	if runs.Load() != 1 {
		t.Fatalf("Finalizers ran %d times", runs.Load())
	}
	for _, template := range templates {
		if _, err := template.Bind(nil); !errors.Is(err, execution.ErrClosed) {
			t.Fatalf("Bind after Finalizers: %v", err)
		}
	}
	// A later lowering of the same program cannot bind either.
	if _, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C}); !errors.Is(err, execution.ErrClosed) {
		t.Fatalf("Compile after Finalizers: %v", err)
	}
}

// TestCellLowerFinalizers checks that a Builder from NewBuilderFor hands its
// cells' Finalizers to the Unit: closing the only Code runs them, and the
// Template cannot bind again.
func TestCellLowerFinalizers(t *testing.T) {
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
		b.ParamNative()
		value := new(uint16)
		*value = 9
		cell := nyanjit.ValueCell(b, value)
		done := b.NewLabel()
		b.Jump(done)
		b.Bind(done)
		b.Return(b.CastNative(cell.Load()))
		unit, err := b.Lower()
		if err != nil {
			t.Fatal(err)
		}
		template, err := unit.Template()
		if err != nil {
			t.Fatal(err)
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := code.Call(0); err != nil || got != 9 {
			t.Fatalf("%v: got %d, %v", tier, got, err)
		}
		_ = code.Close()
		if _, err := template.Bind(nil); !errors.Is(err, execution.ErrClosed) {
			t.Fatalf("%v: Bind after Close: %v", tier, err)
		}
	}
}
