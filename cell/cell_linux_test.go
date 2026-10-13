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
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestCellSpillsOnAddressAndObservesAlias(t *testing.T) {
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
	if err != nil || current != initial {
		t.Fatalf("virtual value = %v, %v", current, err)
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
	for _, ins := range program.Instructions {
		if ins.Op == ir.OpStore64 {
			stores++
		}
		if ins.Op == ir.OpLoad64 {
			loads++
		}
	}
	if stores != 3 || loads != 2 {
		t.Fatalf("stores=%d loads=%d, want 3 and 2", stores, loads)
	}
	code, err := compile.Compile(program, abi.CompileOptions{ABI: abi.C})
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

func TestCellRejectsHiddenCurrentValue(t *testing.T) {
	b := nyanjit.NewBuilder()
	home := b.Const(0)
	b.Enter()
	initial := b.Const(7)
	c, err := New(b, home, initial)
	if err != nil {
		t.Fatal(err)
	}
	b.Leave()
	if _, err := c.Load(); !errors.Is(err, nyanjit.ErrInvalidFunction) {
		t.Fatalf("Load = %v", err)
	}
	if _, err := c.Address(); !errors.Is(err, nyanjit.ErrInvalidFunction) {
		t.Fatalf("Address = %v", err)
	}
}
