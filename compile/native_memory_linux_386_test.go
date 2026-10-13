//go:build linux && 386

package compile

import (
	"bytes"
	"runtime"
	"strconv"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestNativeWidthTypedMemory386(t *testing.T) {
	for _, tc := range []struct {
		bits  uint8
		value uint64
		bytes []byte
	}{
		{8, 0xab, []byte{0xab}},
		{16, 0xbabe, []byte{0xbe, 0xba}},
		{32, 0xcafebabe, []byte{0xbe, 0xba, 0xfe, 0xca}},
	} {
		t.Run(strconv.Itoa(int(tc.bits)), func(t *testing.T) {
			data := bytes.Repeat([]byte{0x5a}, 8)
			var pinner runtime.Pinner
			pinner.Pin(&data[0])
			defer pinner.Unpin()
			b := builder.NewBuilder()
			address, err := b.ConstNative(uintptr(unsafe.Pointer(&data[1])))
			if err != nil {
				t.Fatal(err)
			}
			value, err := b.ConstBits(tc.bits, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.StoreBits(address, value); err != nil {
				t.Fatal(err)
			}
			loaded, err := b.LoadBits(address, tc.bits)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Return(loaded); err != nil {
				t.Fatal(err)
			}
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			lo, hi, err := code.CallPair()
			got := execution.Wide(lo, hi)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.value || !bytes.Equal(data[1:1+len(tc.bytes)], tc.bytes) || data[0] != 0x5a || data[1+len(tc.bytes)] != 0x5a {
				t.Fatalf("got value %#x, memory %x; want %#x, %x", got, data, tc.value, tc.bytes)
			}
		})
	}
}

func TestNativeWidthMemory64386(t *testing.T) {
	memory, err := execution.Scratch(16)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.FreeScratch(memory)
	b := builder.NewBuilder()
	address, err := b.ConstNative(uintptr(unsafe.Pointer(&memory[0])))
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.ConstBits(64, 0x1122334455667788)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.StoreBits(address, value); err != nil {
		t.Fatal(err)
	}
	loaded, err := b.LoadBits(address, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(loaded); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	lo, hi, err := code.CallPair()
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.Wide(lo, hi); got != 0x1122334455667788 || !bytes.Equal(memory[:8], []byte{0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11}) {
		t.Fatalf("loaded %#x from %x", got, memory[:8])
	}
}
