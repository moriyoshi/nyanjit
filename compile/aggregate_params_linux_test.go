//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
)

type aggregateItem struct{ A, B, C uint32 } // stride 12 on every host
type aggregateArgs struct {
	Bias  uint32
	Items []aggregateItem
}

func TestAggregateSliceFieldLoop(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		t.Run(fmt.Sprint(flavor), func(t *testing.T) {
			b := n.NewBuilder()
			args := n.ValueParam[aggregateArgs](b)
			out := n.PointerParam[[3]uint64](b)
			items := args.At("Items") // created before the first control-flow boundary
			index := b.Var(b.ConstNative(0))
			sum := b.Var(b.Const(0))
			head, done := b.NewLabel(), b.NewLabel()
			b.Jump(head)
			b.Bind(head)
			row := items.Index(b.Get(index), done)
			total := b.Add(b.Get(sum), b.Add(b.CastUnsigned(row.Field("A"), 64), b.Add(b.CastUnsigned(row.Field("B"), 64), b.CastUnsigned(row.Field("C"), 64))))
			total = b.Add(total, b.CastUnsigned(args.Field("Bias"), 64)) // root after Bind
			b.Set(sum, total)
			row.At("A").Store(b.Add(row.Field("A"), b.ConstBits(32, 1))) // readonly header, writable backing
			out.At("[1]").Store(total)
			out.At("[2]").Store(b.CastUnsigned(items.Len(), 64)) // slice view after Bind
			b.Set(index, b.Add(b.Get(index), b.ConstNative(1)))
			b.Jump(head)
			b.Bind(done)
			b.Return(b.Get(sum))
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := n.Compile(program, compile.TargetSpec{}, structParamOptions(flavor))
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, input := range [][]aggregateItem{nil, make([]aggregateItem, 0), {{1, 2, 3}, {11, 12, 13}, {21, 22, 23}}} {
				argsValue := aggregateArgs{Bias: 7, Items: append([]aggregateItem(nil), input...)}
				if input != nil && len(input) == 0 {
					argsValue.Items = make([]aggregateItem, 0)
				}
				var want uint64
				for _, v := range input {
					want += uint64(v.A) + uint64(v.B) + uint64(v.C) + 7
				}
				outValue := [3]uint64{0x123456789, 0, 0}
				lo, hi, err := code.CallPair(execution.Words(argsValue, &outValue)...)
				if err != nil || execution.Wide(lo, hi) != want || outValue[0] != 0x123456789 || outValue[1] != want || outValue[2] != uint64(len(input)) || argsValue.Bias != 7 {
					t.Fatalf("len %d: result %d output %v err %v, want %d", len(input), execution.Wide(lo, hi), outValue, err, want)
				}
				for i, v := range argsValue.Items {
					if v.A != input[i].A+1 || v.B != input[i].B || v.C != input[i].C {
						t.Fatalf("backing write %d: %v", i, v)
					}
				}
			}
			if unsafe.Sizeof(aggregateItem{}) != 12 {
				t.Fatal("non-power-of-two fixture lost its stride")
			}
		})
	}
}

func TestAggregateNestedSliceParam(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		t.Run(fmt.Sprint(flavor), func(t *testing.T) {
			b := n.NewBuilder()
			root := n.SliceParam[[]uint32](b)
			outerIndex := b.Param()
			innerIndex := b.Var(b.Param())
			bad := b.NewLabel()
			outer := root.Index(outerIndex, bad)
			leaf := outer.Index(b.Get(innerIndex), bad)
			leaf.Store(b.Add(leaf.Load(), b.ConstBits(32, 1)))
			b.Return(b.CastUnsigned(leaf.Load(), 64))
			b.Bind(bad)
			b.Return(b.Const(0xdead))
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := n.Compile(program, compile.TargetSpec{}, structParamOptions(flavor))
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			tests := []struct {
				value        [][]uint32
				outer, inner uint64
				want         uint64
			}{
				{nil, 0, 0, 0xdead}, {make([][]uint32, 0), 0, 0, 0xdead}, {[][]uint32{nil}, 0, 0, 0xdead},
				{[][]uint32{{10, 20}, {30}}, 0, 1, 21}, {[][]uint32{{10, 20}, {30}}, 1, 0, 31},
				{[][]uint32{{1}}, 1 << 32, 0, 0xdead}, {[][]uint32{{1}}, 0, 1 << 32, 0xdead},
			}
			for _, tt := range tests {
				lo, hi, err := code.CallPair(execution.Words(tt.value, tt.outer, tt.inner)...)
				if err != nil || execution.Wide(lo, hi) != tt.want {
					t.Fatalf("%d/%d: %d err %v, want %d", tt.outer, tt.inner, execution.Wide(lo, hi), err, tt.want)
				}
				if tt.want != 0xdead && uint64(tt.value[tt.outer][tt.inner]) != tt.want {
					t.Fatal("slice store did not write back")
				}
			}
		})
	}
}

func TestAggregateArrayRootsAndStride24(t *testing.T) {
	type row struct{ A, B, C uint64 }
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, pointer := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/pointer=%v", flavor, pointer), func(t *testing.T) {
				b := n.NewBuilder()
				var root n.Data
				if pointer {
					root = n.PointerParam[[3]row](b)
				} else {
					root = n.ValueParam[[3]row](b)
				}
				index := b.Param()
				bad := b.NewLabel()
				entry := root.Index(index, bad)
				sum := b.Add(entry.Field("A"), b.Add(entry.Field("B"), entry.Field("C")))
				if pointer {
					entry.At("C").Store(sum)
				}
				b.Return(sum)
				b.Bind(bad)
				b.Return(b.Const(0xdead))
				program, err := b.Finish()
				if err != nil {
					t.Fatal(err)
				}
				code, err := n.Compile(program, compile.TargetSpec{}, structParamOptions(flavor))
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				for _, i := range []uint64{0, 1, 2, 3, 1 << 32} {
					data := [3]row{{1, 2, 3}, {11, 12, 13}, {21, 22, 23}}
					before := data
					var arg any = data
					if pointer {
						arg = &data
					}
					want := uint64(0xdead)
					if i < 3 {
						want = data[i].A + data[i].B + data[i].C
					}
					lo, hi, err := code.CallPair(execution.Words(arg, i)...)
					if err != nil || execution.Wide(lo, hi) != want {
						t.Fatalf("index %d: %#x err %v", i, execution.Wide(lo, hi), err)
					}
					if pointer && i < 3 {
						before[i].C = want
					}
					if data != before {
						t.Fatalf("array write-back %v want %v", data, before)
					}
				}
				if pointer && flavor == abi.Go {
					var f func(*[3]row, uint64) uint64
					err := code.Func(&f)
					if err != nil {
						t.Fatal(err)
					}
					a := [3]row{{}, {4, 5, 6}, {}}
					if got := f(&a, 1); got != 15 || a[1].C != 15 {
						t.Fatalf("typed array pointer got %d array %v", got, a)
					}
					runtime.KeepAlive(&a)
				}
				if unsafe.Sizeof(row{}) != 24 {
					t.Fatal("stride fixture differs")
				}
			})
		}
	}
}

func TestAggregateSliceManagedCallback(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := n.NewBuilder()
	root := n.PointerParam[aggregateArgs](b)
	bad := b.NewLabel()
	result := b.Var(b.CallGo(b.ConstNative(reflect.ValueOf(structParamGC).Pointer()), b.Const(64)))
	elem := root.At("Items").Index(b.ConstNative(0), bad)
	b.Return(b.Add(b.CastUnsigned(elem.Field("A"), 64), b.Get(result))) // fresh lookup after GC
	b.Bind(bad)
	b.Return(b.Const(0))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	fn := structParamOptions(abi.Go)
	fn.Context = compile.ContextGoManaged
	fn.AssumePointerFree = true
	code, err := n.Compile(program, compile.TargetSpec{}, fn)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	var f func(*aggregateArgs) uint64
	err = code.Func(&f)
	if err != nil {
		t.Fatal(err)
	}
	input := &aggregateArgs{Items: []aggregateItem{{A: 35}}}
	var pin runtime.Pinner
	pin.Pin(input)
	defer pin.Unpin()
	resultCh := make(chan uint64, 1)
	go func() { resultCh <- f(input) }()
	if got := <-resultCh; got != 42 {
		t.Fatalf("slice read after GC = %d", got)
	}
	runtime.KeepAlive(input)
	lo, hi, err := code.CallPair(execution.Words(input)...)
	if err != nil || execution.Wide(lo, hi) != 42 {
		t.Fatalf("Words callback result %d err %v", execution.Wide(lo, hi), err)
	}
}

func TestAggregateArraySliceArray(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		t.Run(fmt.Sprint(flavor), func(t *testing.T) {
			b := n.NewBuilder()
			root := n.ValueParam[[2][][3]uint16](b)
			index := b.Param()
			bad := b.NewLabel()
			row := root.At("[1]").Index(index, bad)
			row.At("[2]").Store(b.ConstBits(16, 0xbeef))
			b.Return(b.CastUnsigned(row.Field("[2]"), 64))
			b.Bind(bad)
			b.Return(b.Const(0xdead))
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := n.Compile(program, compile.TargetSpec{}, structParamOptions(flavor))
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, rows := range [][][3]uint16{nil, {{1, 2, 3}, {4, 5, 6}}} {
				data := [2][][3]uint16{nil, rows}
				want := uint64(0xdead)
				if len(rows) > 1 {
					want = 0xbeef
				}
				lo, hi, err := code.CallPair(execution.Words(data, uint64(1))...)
				if err != nil || execution.Wide(lo, hi) != want {
					t.Fatalf("len %d: %#x err %v", len(rows), execution.Wide(lo, hi), err)
				}
				if len(rows) > 1 && (rows[1] != [3]uint16{4, 5, 0xbeef} || len(data[1]) != 2) {
					t.Fatalf("array/slice backing write %v", data)
				}
			}
		})
	}
}
