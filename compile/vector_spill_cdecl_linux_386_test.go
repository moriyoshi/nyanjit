//go:build linux && 386

package compile_test

import (
	"bytes"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func Test386VectorSpillPreservesCalleeRegisters(t *testing.T) {
	// Independent cdecl caller: save its caller's registers, seed EBP/EDI,
	// forward two pointer arguments, then return a bit per damaged register.
	callerBytes := []byte{
		0x53, 0x56, 0x57, 0x55, 0x83, 0xec, 4,
		0x8b, 0x44, 0x24, 24, // mov eax,[esp+24] (target)
		0xbd, 0xdf, 0x9b, 0x57, 0x13, // mov ebp,13579bdf
		0xbf, 0xe0, 0xac, 0x68, 0x24, // mov edi,2468ace0
		0xff, 0x74, 0x24, 32, 0xff, 0x74, 0x24, 32, // push out; push in
		0xff, 0xd0, 0x83, 0xc4, 8, // call eax; discard args
		0x31, 0xc9, 0x81, 0xfd, 0xdf, 0x9b, 0x57, 0x13, 0x0f, 0x95, 0xc1,
		0x31, 0xd2, 0x81, 0xff, 0xe0, 0xac, 0x68, 0x24, 0x0f, 0x95, 0xc2,
		0xd1, 0xe2, 0x09, 0xd1, 0x89, 0xc8, 0x31, 0xd2,
		0x83, 0xc4, 4, 0x5d, 0x5f, 0x5e, 0x5b, 0xc3,
	}
	caller, err := execution.NewCode(callerBytes, execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	leaf, err := execution.NewCode([]byte{0xb8, 42, 0, 0, 0, 0x31, 0xd2, 0xc3}, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer leaf.Close()
	leafEntry, err := leaf.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	for live := 6; live <= 9; live++ {
		mem, ptr := simdMemory(t)
		for k := 0; k < live*16; k++ {
			mem[k] = byte(k*37 + 13)
		}
		b := n.NewBuilder()
		in, out := b.ParamNative(), b.ParamNative()
		values := make([]n.Value, live)
		for k := range values {
			values[k] = b.VLoad(ir.Vec(4, 32), b.Add(in, b.ConstNative(uintptr(k*16))))
		}
		called := b.CallC(b.ConstNative(leafEntry))
		for k, v := range values {
			b.VStore(b.Add(out, b.ConstNative(uintptr(k*16))), v)
		}
		b.Return(called)
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
		if err != nil {
			t.Fatal(err)
		}
		entry, err := c.EntryAddress()
		if err != nil {
			t.Fatal(err)
		}
		got, err := caller.Call(entry, ptr, ptr+1024)
		c.Close()
		if err != nil || got != 0 {
			t.Fatalf("live%d damaged cdecl registers: mask%d err%v", live, got, err)
		}
		if !bytes.Equal(mem[:live*16], mem[1024:1024+live*16]) {
			t.Fatalf("live%d vector data corrupted", live)
		}
	}
}
