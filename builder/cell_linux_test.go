//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package builder

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// buildCellSum returns f(i) = values[i] + *extra, or 1000 + len(values) out
// of bounds, through a SliceCell and a ValueCell.
func buildCellSum(t *testing.T, b *Builder, values []uint32, extra *uint16) {
	t.Helper()
	i := cellMust[Value](t)(b.ParamNative())
	slice := cellMust[Data](t)(SliceCell(b, values))
	value := cellMust[Data](t)(ValueCell(b, extra))
	bad := b.NewLabel()
	element := cellMust[Data](t)(slice.Index(i, bad))
	sum := cellMust[Value](t)(b.Add(cellMust[Value](t)(element.Load()), cellMust[Value](t)(b.CastUnsigned(cellMust[Value](t)(value.Load()), 32))))
	if err := b.Return(cellMust[Value](t)(b.CastNative(sum))); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(bad); err != nil {
		t.Fatal(err)
	}
	n := cellMust[Value](t)(slice.Len())
	if err := b.Return(cellMust[Value](t)(b.Add(n, cellMust[Value](t)(b.ConstNative(1000))))); err != nil {
		t.Fatal(err)
	}
}

func checkCellSum(t *testing.T, name string, code *execution.Code) {
	t.Helper()
	for _, c := range [][2]uintptr{{0, 17}, {2, 37}, {3, 1003}, {^uintptr(0), 1003}} {
		if got, err := code.Call(c[0]); err != nil || got != c[1] {
			t.Errorf("%s: f(%d) = %d, %v; want %d", name, c[0], got, err, c[1])
		}
	}
}

// TestCellUnpinsWhenLastCodeCloses binds a finished program at both tiers,
// twice each, and closes the Codes from several goroutines: the pins end
// with the last Close, and binding afterwards fails.
func TestCellUnpinsWhenLastCodeCloses(t *testing.T) {
	values, extra := []uint32{10, 20, 30}, uint16(7)
	b := NewBuilder()
	buildCellSum(t, b, values, &extra)
	pin := b.cells.pin
	program := cellMust[*ir.SealedProgram](t)(b.Finish())
	var codes []*execution.Code
	var templates []*execution.Template
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		template := cellMust[*execution.Template](t)(lowerTemplate(program, compile.TargetSpec{Tier: tier}, compile.Func{ABI: abi.C}))
		templates = append(templates, template)
		for range 2 {
			code := cellMust[*execution.Code](t)(template.Bind(nil))
			checkCellSum(t, fmt.Sprint(tier), code)
			codes = append(codes, code)
		}
	}
	_ = codes[0].Close()
	if !cellPinned(pin) {
		t.Fatal("unpinned with Codes open")
	}
	var wg sync.WaitGroup
	for _, code := range codes[1:] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = code.Close()
		}()
	}
	wg.Wait()
	if cellPinned(pin) {
		t.Fatal("still pinned after the last Close")
	}
	for _, template := range templates {
		if _, err := template.Bind(nil); !errors.Is(err, execution.ErrClosed) {
			t.Fatalf("Bind after the last Close: %v", err)
		}
	}
}

// TestCellUnpinsStream lowers cells through a compile.Stream at both tiers.
func TestCellUnpinsStream(t *testing.T) {
	values, extra := []uint32{10, 20, 30}, uint16(7)
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target := cellMust[compile.Target](t)(compile.NewTarget(compile.TargetSpec{Tier: tier}))
		stream := cellMust[*compile.Stream](t)(compile.NewStream(target, compile.Func{ABI: abi.C}))
		b := NewSegmentBuilder(stream.Segment)
		buildCellSum(t, b, values, &extra)
		pin := b.cells.pin
		if err := b.End(); err != nil {
			t.Fatal(err)
		}
		unit := cellMust[*compile.Unit](t)(stream.Unit())
		template := cellMust[*execution.Template](t)(unit.Template())
		code := cellMust[*execution.Code](t)(template.Bind(nil))
		checkCellSum(t, fmt.Sprint(tier), code)
		extra = 8
		if got, err := code.Call(0); err != nil || got != 18 {
			t.Errorf("%v: after a write, f(0) = %d, %v", tier, got, err)
		}
		extra = 7
		if !cellPinned(pin) {
			t.Fatal("unpinned with a Code open")
		}
		_ = code.Close()
		if cellPinned(pin) {
			t.Fatal("still pinned after Close")
		}
	}
}

// TestCellNestedSlices reads slices within SliceCell elements.
func TestCellNestedSlices(t *testing.T) {
	type row struct {
		Base  uint32
		Cells []uint16
	}
	rows := []row{{Base: 100, Cells: []uint16{1, 2}}, {Base: 200}, {Base: 300, Cells: []uint16{3, 4, 5}}}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		b := NewBuilder()
		i := cellMust[Value](t)(b.ParamNative())
		cell := cellMust[Data](t)(SliceCell(b, rows))
		bad, empty := b.NewLabel(), b.NewLabel()
		r := cellMust[Data](t)(cell.Index(cellMust[Value](t)(b.ShiftRight(i, 4)), bad))
		base := cellMust[Value](t)(r.Field("Base"))
		cells := cellMust[Data](t)(r.At("Cells"))
		n := cellMust[Value](t)(cells.Len())
		value := cellMust[Data](t)(cells.Index(cellMust[Value](t)(b.And(i, cellMust[Value](t)(b.ConstNative(15)))), empty))
		sum := cellMust[Value](t)(b.Add(base, cellMust[Value](t)(b.CastUnsigned(cellMust[Value](t)(value.Load()), 32))))
		if err := b.Return(cellMust[Value](t)(b.CastNative(sum))); err != nil {
			t.Fatal(err)
		}
		if err := b.Bind(empty); err != nil {
			t.Fatal(err)
		}
		if err := b.Return(cellMust[Value](t)(b.Add(n, cellMust[Value](t)(b.ConstNative(1000))))); err != nil {
			t.Fatal(err)
		}
		if err := b.Bind(bad); err != nil {
			t.Fatal(err)
		}
		if err := b.Return(cellMust[Value](t)(b.ConstNative(0))); err != nil {
			t.Fatal(err)
		}
		program := cellMust[*ir.SealedProgram](t)(b.Finish())
		template := cellMust[*execution.Template](t)(lowerTemplate(program, compile.TargetSpec{Tier: tier}, compile.Func{ABI: abi.C}))
		code := cellMust[*execution.Code](t)(template.Bind(nil))
		for _, c := range [][2]uintptr{{0x00, 101}, {0x01, 102}, {0x02, 1002}, {0x10, 1000}, {0x22, 305}, {0x23, 1003}, {0x30, 0}} {
			if got, err := code.Call(c[0]); err != nil || got != c[1] {
				t.Errorf("%v: f(%#x) = %d, %v; want %d", tier, c[0], got, err, c[1])
			}
		}
		_ = code.Close()
	}
}
