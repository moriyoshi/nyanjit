//go:build linux && (arm64 || amd64)

package cell

import (
	"encoding/binary"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestCellObservesAlias(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	address := uintptr(unsafe.Pointer(&memory[0]))
	b := nyanjit.NewBuilder()
	home := b.Param()
	initial := b.Param()
	c, err := New(b, home, initial)
	if err != nil {
		t.Fatal(err)
	}
	current, err := c.Load()
	if err != nil || current == initial {
		t.Fatalf("Load = %v, %v; want a load of the home", current, err)
	}
	one := b.Const(1)
	updated := b.Add(current, one)
	if err := c.Store(updated); err != nil {
		t.Fatal(err)
	}
	alias, err := c.Address()
	if err != nil || alias != home {
		t.Fatalf("address = %v, %v", alias, err)
	}
	if _, err := c.Address(); err != nil {
		t.Fatal(err)
	}
	ninetyNine := b.Const(99)
	b.Store64(alias, ninetyNine)
	observed, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	final := b.Add(observed, one)
	if err := c.Store(final); err != nil {
		t.Fatal(err)
	}
	result, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Return(result)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	stores, loads := 0, 0
	for _, ins := range program.Clone().Instructions {
		if ins.Op == ir.OpStore64 {
			stores++
		}
		if ins.Op == ir.OpLoad64 {
			loads++
		}
	}
	if stores != 4 || loads != 3 {
		t.Fatalf("stores=%d loads=%d, want 4 and 3", stores, loads)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(address, 41)
	if err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(memory)
	if got != 100 || binary.LittleEndian.Uint64(memory[:8]) != 100 {
		t.Fatalf("value=%d memory=%d", got, binary.LittleEndian.Uint64(memory[:8]))
	}
}

func TestCellRejectsHiddenHome(t *testing.T) {
	b := nyanjit.NewBuilder()
	initial := b.Const(7)
	b.Enter()
	home := b.Const(0)
	c, err := New(b, home, initial)
	if err != nil {
		t.Fatal(err)
	}
	b.Leave()
	if _, err := c.Load(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("Load = %v", err)
	}
	if err := c.Store(initial); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("Store = %v", err)
	}
	if _, err := c.Address(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("Address = %v", err)
	}
}

// Exposure in one branch must not leave the home stale on the other path.
func TestCellHomeHoldsValueOnEveryPath(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	address := uintptr(unsafe.Pointer(&memory[0]))
	b := nyanjit.NewBuilder()
	home, initial, flag := b.Param(), b.Param(), b.Param()
	c, err := New(b, home, initial)
	if err != nil {
		t.Fatal(err)
	}
	// The store is built before the exposure, which is built before the path
	// that touches nothing; at run time each executes alone.
	assign, rest, expose, untouched, join := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(flag), assign)
	b.Jump(rest)
	b.Bind(assign)
	if err := c.Store(b.Const(5)); err != nil {
		t.Fatal(err)
	}
	b.Jump(join)
	b.Bind(rest)
	b.Branch(b.CompareImmediate(flag, ir.BranchEqual, 1), expose)
	b.Jump(untouched)
	b.Bind(expose)
	if _, err := c.Address(); err != nil {
		t.Fatal(err)
	}
	b.Jump(join)
	b.Bind(untouched)
	b.Jump(join)
	b.Bind(join)
	result, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	b.Return(result)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, tc := range []struct{ flag, want uintptr }{{0, 5}, {1, 41}, {2, 41}} {
		binary.LittleEndian.PutUint64(memory[:8], 0xdead)
		got, err := code.Call(address, 41, tc.flag)
		if err != nil {
			t.Fatal(err)
		}
		runtime.KeepAlive(memory)
		if stored := uintptr(binary.LittleEndian.Uint64(memory[:8])); got != tc.want || stored != tc.want {
			t.Errorf("flag %d: value=%d memory=%d, want %d", tc.flag, got, stored, tc.want)
		}
	}
}
